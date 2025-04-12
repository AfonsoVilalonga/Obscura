package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/ivfreader"
	"github.com/pion/webrtc/v3/pkg/media/oggreader"
	"github.com/xtaci/kcp-go"
	"github.com/xtaci/smux"
	pt "gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/goptlib"
	"gopkg.in/yaml.v2"
)

const (
	CLIENT               = 2
	SIZE_OF_HEADER       = 11
	PICTURE_ID_MAX_VALUE = 32767
	PINGDEADLINE         = time.Second * 10

	WEB   = 3
	PROXY = 1
)

type state struct {
	connSignaling  *websocket.Conn
	peerConnection *webrtc.PeerConnection
	conn           *rOConn

	pendingCandidate []*webrtc.ICECandidateInit

	doneSingalingBool bool
	doneSignaling     chan int
	signalingConnMux  sync.Mutex

	iceConnectedCtx       context.Context
	iceConnectedCtxCancel context.CancelFunc

	sequenceNumer     uint32
	pictureIDTwoBytes uint64

	fragmentedPackets map[uint32]*FragmentedPacket

	closed chan int
}

type FragmentedPacket struct {
	data      map[uint8][]byte
	lastChunk uint8
}

var (
	configs   map[string]interface{}
	proxyType int
)

func (s *state) encapsulateWeb(remaing, frame []byte, chunkNumber uint8, conn *rOConn) ([]byte, []byte, uint8) {
	var lenFrame int = len(frame)
	var result []byte = make([]byte, 0)
	var bypassBytes = 0

	frame[0] = frame[0] & 0b11111110
	lenFrame = lenFrame - 10
	result = append(result, frame[:10]...)

	if (conn.lenOutgoingQueue() == 0 && len(remaing) == 0) || lenFrame <= SIZE_OF_HEADER {
		frame[bypassBytes] = 0
		return frame, nil, 0
	}

	var data []byte = remaing
	var reamaingArray []byte = nil

	for lenFrame > SIZE_OF_HEADER {
		if len(data) == 0 {
			data = conn.OutgoingQueue()
			if len(data) == 0 {
				break
			}
		}

		//FLAG PACKET HAS CONTENT
		result = append(result, byte(1))

		//SEQUENCE NUMBER OF THE PACKET
		bytes := make([]byte, 4)
		binary.BigEndian.PutUint32(bytes, uint32(s.sequenceNumer))
		result = append(result, bytes...)

		//CHUNK OF THE PACKET
		result = append(result, byte(chunkNumber))
		chunkNumber++

		//LEN OF THE DATA IN THE PACKET
		lenData := uint32(min(len(data), max(lenFrame-SIZE_OF_HEADER, 0)))
		bytes = make([]byte, 4)
		binary.BigEndian.PutUint32(bytes, lenData)
		result = append(result, bytes...)

		//FLAG LAST CHUNK
		finalChunk := 0
		if len(data) == int(lenData) {
			finalChunk = 1
		}
		result = append(result, byte(finalChunk))

		//DATA
		result = append(result, data[:lenData]...)

		data = data[lenData:]
		lenFrame = lenFrame - int(lenData) - SIZE_OF_HEADER

		if len(data) == 0 {
			s.sequenceNumer = s.sequenceNumer + 1
			chunkNumber = 0
			data = nil
		}
	}

	if len(result) < len(frame) {
		aux := make([]byte, len(frame)-len(result))
		result = append(result, aux...)
	}

	if len(data) > 0 {
		reamaingArray = data
	}

	s.pictureIDTwoBytes++
	return result, reamaingArray, chunkNumber
}

func (s *state) desencapsulateWeb(frame []byte, conn *rOConn) {
	var lenData uint32 = 0
	var sequenceNumber uint32 = 0
	var chunk uint8 = 0
	var finalChunk uint8 = 0
	var data []byte = make([]byte, 0)

	for i := 4; i < len(frame) && frame[i] == 1; i += (int(lenData) + SIZE_OF_HEADER) {
		sequenceNumber = binary.BigEndian.Uint32([]byte{frame[i+1], frame[i+2], frame[i+3], frame[i+4]})
		chunk = frame[i+5]
		lenData = binary.BigEndian.Uint32([]byte{frame[i+6], frame[i+7], frame[i+8], frame[i+9]})
		finalChunk = frame[i+10]
		data = frame[i+SIZE_OF_HEADER : i+SIZE_OF_HEADER+int(lenData)]

		packet := s.reconstructPacket(sequenceNumber, chunk, finalChunk, data)
		if len(packet) != 0 {
			conn.QueueIncoming(packet)
		}
	}
}

func (s *state) encapsulate(remaing, frame []byte, chunkNumber uint8, conn *rOConn) ([]byte, []byte, uint8) {
	var value_inc int
	if s.pictureIDTwoBytes > PICTURE_ID_MAX_VALUE {
		s.pictureIDTwoBytes = 0
	}

	//FRAMES ARE DIVIDED INTO CHUNCKS OF DIFFERENT SIZES DEPNDING ON THE PICTURE ID (A FLAG ON THE FRAME). EACH CHUNK WILL BE SENT IN AN INDIVIDUAL RTP PACKET.
	if s.pictureIDTwoBytes == 0 {
		value_inc = 1187
	} else if s.pictureIDTwoBytes > 0 && s.pictureIDTwoBytes < 128 {
		value_inc = 1185
	} else {
		value_inc = 1184
	}

	//IF FRAME IS TO SMALL OR NO CONTENT NEEDS TO BE SENT, SEND FRAME AS IS BUT WITH FLAG PACKET HAS CONTENT FALSE
	if (conn.lenOutgoingQueue() == 0 && len(remaing) == 0) || len(frame) <= SIZE_OF_HEADER {

		for i := 0; i < len(frame); i += value_inc {
			frame[i] = 0
		}

		s.pictureIDTwoBytes++
		return frame, nil, 0
	}

	//LOOP TO ADD CONTENT TO FRAMES
	var data []byte = remaing
	var reamaingArray []byte = nil
	var result []byte = make([]byte, 0)
	var remaingSize int = value_inc
	var lenFrame int = len(frame)
	for lenFrame > SIZE_OF_HEADER {
		if len(data) == 0 {
			data = conn.OutgoingQueue()
			if len(data) == 0 {
				break
			}
		}

		//FLAG PACKET HAS CONTENT
		result = append(result, byte(1))

		//SEQUENCE NUMBER OF THE PACKET
		bytes := make([]byte, 4)
		binary.BigEndian.PutUint32(bytes, uint32(s.sequenceNumer))
		result = append(result, bytes...)

		//CHUNK OF THE PACKET
		result = append(result, byte(chunkNumber))
		chunkNumber++

		//LEN OF THE DATA IN THE PACKET
		lenData := uint32(min(len(data), max(remaingSize-SIZE_OF_HEADER, 0), max(lenFrame-SIZE_OF_HEADER, 0)))
		bytes = make([]byte, 4)
		binary.BigEndian.PutUint32(bytes, lenData)
		result = append(result, bytes...)

		//FLAG LAST CHUNK
		finalChunk := 0
		if len(data) == int(lenData) {
			finalChunk = 1
		}
		result = append(result, byte(finalChunk))

		//DATA
		result = append(result, data[:lenData]...)

		//UPDATES FOR NEXT ITERATION
		data = data[lenData:]
		lenFrame = lenFrame - int(lenData) - SIZE_OF_HEADER
		remaingSize = remaingSize - int(lenData) - SIZE_OF_HEADER

		if remaingSize <= SIZE_OF_HEADER {
			filler := make([]byte, remaingSize)
			result = append(result, filler...)
			remaingSize = value_inc
			lenFrame = lenFrame - SIZE_OF_HEADER
		}

		if len(data) == 0 {
			s.sequenceNumer = s.sequenceNumer + 1
			chunkNumber = 0
			data = nil
		}
	}

	if len(result) < len(frame) {
		aux := make([]byte, len(frame)-len(result))
		result = append(result, aux...)
	}

	if len(data) > 0 {
		reamaingArray = data
	}

	s.pictureIDTwoBytes++
	return result, reamaingArray, chunkNumber
}

func (s *state) decapsulate(frame []byte, conn *rOConn) {
	var headerSizeBytes = 0

	//VP8 FRAME HEADER, THE VALUE TOTAL VALUE OF THE PAYLOAD DEPENDS ON SOME FLAGS
	if frame[0]&0b10010000 == 0b00010000 {
		headerSizeBytes++
	}

	if frame[0]&0b10000000 == 0b10000000 {
		headerSizeBytes = headerSizeBytes + 2

		if frame[1]&0b10000000 == 0b10000000 {
			headerSizeBytes++

			if frame[2] >= 128 {
				headerSizeBytes++
			}
		}

		if frame[1]&0b01000000 == 0b01000000 {
			headerSizeBytes++
		}

		if frame[1]&0b00100000 == 0b00100000 || frame[1]&0b00010000 == 0b00010000 {
			headerSizeBytes++
		}
	}

	var lenData uint32 = 0
	var sequenceNumber uint32 = 0
	var chunk uint8 = 0
	var finalChunk uint8 = 0
	var data []byte = make([]byte, 0)

	for i := headerSizeBytes; i < len(frame) && frame[i] == 1; i += (int(lenData) + SIZE_OF_HEADER) {
		sequenceNumber = binary.BigEndian.Uint32([]byte{frame[i+1], frame[i+2], frame[i+3], frame[i+4]})
		chunk = frame[i+5]
		lenData = binary.BigEndian.Uint32([]byte{frame[i+6], frame[i+7], frame[i+8], frame[i+9]})
		finalChunk = frame[i+10]
		data = frame[i+SIZE_OF_HEADER : i+SIZE_OF_HEADER+int(lenData)]

		packet := s.reconstructPacket(sequenceNumber, chunk, finalChunk, data)
		if len(packet) != 0 {
			conn.QueueIncoming(packet)
		}
	}
}

func (s *state) reconstructPacket(sequenceNumber uint32, chunk, finalChunk uint8, data []byte) []byte {
	if chunk == 0 && finalChunk == 1 {
		return data
	}

	result := make([]byte, 0)
	packet, exists := s.fragmentedPackets[sequenceNumber]
	if !exists {
		fragmentedP := FragmentedPacket{
			data:      make(map[uint8][]byte),
			lastChunk: 0,
		}
		fragmentedP.data[chunk] = data
		s.fragmentedPackets[sequenceNumber] = &fragmentedP

		//Keep cleaning up stuff
		maxValueSequenceNumber := uint32(uint64(1<<32) - 1)
		getSymmetricPosition := maxValueSequenceNumber - sequenceNumber
		delete(s.fragmentedPackets, getSymmetricPosition)

		result = nil
	} else {
		_, exists = packet.data[chunk]
		if !exists {
			packet.data[chunk] = data

			if finalChunk == 1 {
				packet.lastChunk = chunk
			}

			if packet.lastChunk != 0 {
				for i := 0; i <= int(packet.lastChunk); i++ {
					fragment, has := packet.data[uint8(i)]
					if has {
						result = append(result, fragment...)
					} else {
						result = nil
						break
					}
				}
			}
		}
	}

	if len(result) != 0 {
		delete(s.fragmentedPackets, sequenceNumber)
	}

	return result
}

func (c *state) startAudioStream() {
	audioTrack, audioTrackErr := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", "pion")
	if audioTrackErr != nil {
		panic(audioTrackErr)
	}

	rtpSender, audioTrackErr := c.peerConnection.AddTrack(audioTrack)
	if audioTrackErr != nil {
		panic(audioTrackErr)
	}

	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				return
			}
		}
	}()

	go func() {
		file, oggErr := os.Open(fmt.Sprintf("%s", configs["audioPath"]))
		if oggErr != nil {
			panic(oggErr)
		}

		ogg, _, oggErr := oggreader.NewWith(file)
		if oggErr != nil {
			panic(oggErr)
		}

		<-c.iceConnectedCtx.Done()

		var lastGranule uint64

		ticker := time.NewTicker(time.Millisecond * 20)
		for ; true; <-ticker.C {
			select {
			case <-c.closed:
				return
			default:
				pageData, pageHeader, oggErr := ogg.ParseNextPage()

				if errors.Is(oggErr, io.EOF) {

					file, oggErr = os.Open(fmt.Sprintf("%s", configs["audioPath"]))
					if oggErr != nil {
						panic(oggErr)
					}

					ogg, _, oggErr = oggreader.NewWith(file)
					if oggErr != nil {
						panic(oggErr)
					}

					pageData, pageHeader, oggErr = ogg.ParseNextPage()
				}

				if errors.Is(oggErr, io.EOF) {
					panic(oggErr)
				}

				if oggErr != nil {
					panic(oggErr)
				}

				sampleCount := float64(pageHeader.GranulePosition - lastGranule)
				lastGranule = pageHeader.GranulePosition
				sampleDuration := time.Duration((sampleCount/48000)*1000) * time.Millisecond

				audioTrack.WriteSample(media.Sample{Data: pageData, Duration: sampleDuration})
			}
		}
	}()
}

func (c *state) startVideoStream() {
	file, openErr := os.Open(fmt.Sprintf("%s", configs["videoPath"]))
	if openErr != nil {
		panic(openErr)
	}

	ivf, header, openErr := ivfreader.NewWith(file)
	if openErr != nil {
		panic(openErr)
	}

	videoTrack, videoTrackErr := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video", "pion")
	if videoTrackErr != nil {
		panic(videoTrackErr)
	}

	rtpSender, videoTrackErr := c.peerConnection.AddTrack(videoTrack)
	if videoTrackErr != nil {
		panic(videoTrackErr)
	}

	go func() {
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := rtpSender.Read(rtcpBuf); rtcpErr != nil {
				return
			}
		}
	}()

	var remaingData []byte = nil
	var newFrame []byte = make([]byte, 0)
	var chunkNumber uint8 = 0

	go func() {
		<-c.iceConnectedCtx.Done()
		ticker := time.NewTicker(time.Millisecond * time.Duration((float32(header.TimebaseNumerator)/float32(header.TimebaseDenominator))*1000))
		for ; true; <-ticker.C {
			select {
			case <-c.closed:
				return
			default:
				frame, _, ivfErr := ivf.ParseNextFrame()

				if errors.Is(ivfErr, io.EOF) {

					file, ivfErr = os.Open(fmt.Sprintf("%s", configs["videoPath"]))
					if ivfErr != nil {
						panic(ivfErr)
					}

					ivf, _, ivfErr = ivfreader.NewWith(file)
					if ivfErr != nil {
						panic(ivfErr)
					}
				}

				if ivfErr != nil {
					panic(ivfErr)
				}

				if len(frame) > 0 {
					if proxyType == WEB {
						newFrame, remaingData, chunkNumber = c.encapsulateWeb(remaingData, frame, chunkNumber, c.conn)
					} else {
						newFrame, remaingData, chunkNumber = c.encapsulate(remaingData, frame, chunkNumber, c.conn)
					}

					videoTrack.WriteSample(media.Sample{Data: newFrame, Duration: time.Second})
				}
			}
		}
	}()
}

func (c *state) writePing() {
	ticker := time.NewTicker(PINGDEADLINE)
	for {
		select {
		case <-c.doneSignaling:
			c.connSignaling.Close()
			return
		case <-ticker.C:
			c.signalingConnMux.Lock()
			c.connSignaling.WriteMessage(websocket.BinaryMessage, []byte("ping"))
			c.signalingConnMux.Unlock()
		}
	}
}

func (c *state) setupConnection() {
	url := fmt.Sprintf("%s", configs["brokerAddr"])
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
		},
	}

	conn_signaling, _, err := dialer.Dial(url, nil)
	if err != nil {
		panic(err)
	}

	data := []byte{CLIENT}
	data = append(data, []byte(fmt.Sprintf("%s", configs["bridgeaddr"]))...)

	c.signalingConnMux.Lock()
	conn_signaling.WriteMessage(websocket.BinaryMessage, data)
	c.signalingConnMux.Unlock()

	c.connSignaling = conn_signaling

	go c.writePing()

	// m := &webrtc.MediaEngine{}

	// // Setup the codecs you want to use.
	// // We'll use a VP8 and Opus but you can also define your own
	// if err := m.RegisterCodec(webrtc.RTPCodecParameters{
	// 	RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, Channels: 0, SDPFmtpLine: "", RTCPFeedback: nil},
	// 	PayloadType:        96,
	// }, webrtc.RTPCodecTypeVideo); err != nil {
	// 	panic(err)
	// }
	// if err := m.RegisterCodec(webrtc.RTPCodecParameters{
	// 	RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1", RTCPFeedback: nil},
	// 	PayloadType:        111,
	// }, webrtc.RTPCodecTypeAudio); err != nil {
	// 	panic(err)
	// }

	// // Create a InterceptorRegistry. This is the user configurable RTP/RTCP Pipeline.
	// // This provides NACKs, RTCP Reports and other features. If you use `webrtc.NewPeerConnection`
	// // this is enabled by default. If you are manually managing You MUST create a InterceptorRegistry
	// // for each PeerConnection.
	// i := &interceptor.Registry{}

	// // Use the default set of Interceptors
	// if err = webrtc.RegisterDefaultInterceptors(m, i); err != nil {
	// 	panic(err)
	// }

	// // Create the API object with the MediaEngine
	// api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i))

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	})
	if err != nil {
		panic(err)
	}

	c.peerConnection = peerConnection

	messageType, payload, err := c.connSignaling.ReadMessage()
	if err != nil {
		return
	}

	if messageType == websocket.BinaryMessage || messageType == websocket.TextMessage {
		if payload[0] == WEB {
			proxyType = WEB
		} else if payload[0] == PROXY {
			proxyType = PROXY
		} else {
			panic("Invalid type of proxy.")
		}
	}

	c.peerConnection.OnICECandidate(func(cand *webrtc.ICECandidate) {
		if cand == nil {
			return
		}

		candBytes, err := json.Marshal(cand.ToJSON())
		if err != nil {
			panic(err)
		}

		c.signalingConnMux.Lock()
		c.connSignaling.WriteMessage(websocket.BinaryMessage, candBytes)
		c.signalingConnMux.Unlock()
	})

	c.peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {

			c.iceConnectedCtxCancel()

			if !c.doneSingalingBool {
				close(c.doneSignaling)
			}

			c.doneSingalingBool = true
			fmt.Println("connected")
		}

		if s == webrtc.PeerConnectionStateFailed {
			fmt.Println("Peer Connection has gone to failed exiting")
			close(c.closed)
		}

		if s == webrtc.PeerConnectionStateClosed {
			fmt.Println("Peer Connection has gone to closed exiting")
		}
	})

	c.peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		if strings.EqualFold(codec.MimeType, webrtc.MimeTypeVP8) {
			for {
				packet, _, err := track.ReadRTP()
				if err != nil {
					return
				}

				//fmt.Println(packet)
				//fmt.Println(packet.MarshalSize())

				if proxyType == WEB {
					c.desencapsulateWeb(packet.Payload, c.conn)
				} else {
					c.decapsulate(packet.Payload, c.conn)
				}
			}
		} else {
			for {
				_, _, err := track.ReadRTP()
				if err != nil {
					fmt.Println(err)
					return
				}
			}
		}
	})
}

// Acabar esta função quando acaba a cena
func (c *state) handleWSMessages() {
	defer func() {
		c.connSignaling.Close()
	}()

	for {
		messageType, payload, err := c.connSignaling.ReadMessage()
		if err != nil {
			return
		}

		if messageType == websocket.BinaryMessage || messageType == websocket.TextMessage {
			var (
				candidate webrtc.ICECandidateInit
				offer     webrtc.SessionDescription
			)

			switch {
			case json.Unmarshal(payload, &offer) == nil && offer.SDP != "":
				if sdpErr := c.peerConnection.SetRemoteDescription(offer); sdpErr != nil {
					panic(sdpErr)
				}

				if len(c.pendingCandidate) > 0 {
					for _, cand := range c.pendingCandidate {
						if candidateErr := c.peerConnection.AddICECandidate(*cand); candidateErr != nil {
							panic(candidateErr)
						}
					}
				}

				answer, err := c.peerConnection.CreateAnswer(nil)
				if err != nil {
					panic(err)
				}

				payload, err := json.Marshal(answer)
				if err != nil {
					panic(err)
				}

				c.signalingConnMux.Lock()
				c.connSignaling.WriteMessage(websocket.BinaryMessage, payload)
				c.signalingConnMux.Unlock()

				err = c.peerConnection.SetLocalDescription(answer)
				if err != nil {
					panic(err)
				}

			case json.Unmarshal(payload, &candidate) == nil && candidate.Candidate != "":
				if c.peerConnection.RemoteDescription() == nil {
					c.pendingCandidate = append(c.pendingCandidate, &candidate)
				} else {
					if candidateErr := c.peerConnection.AddICECandidate(candidate); candidateErr != nil {
						panic(candidateErr)
					}
				}
			default:
				panic("Unknown message")
			}
		}
	}
}

// Config file
func readConfig() {
	f, err := os.ReadFile("Config/config.yml")

	if err != nil {
		panic(err)
	}

	var data map[string]interface{}
	err = yaml.Unmarshal(f, &data)

	if err != nil {
		panic(err)
	}
	configs = data
}

// TURBO TUNNEL
type clientID []byte

func newClientID() clientID {
	buf := make([]byte, 8)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}
	return buf
}

func (addr clientID) Network() string {
	return "clientid"
}

func (addr clientID) String() string {
	return hex.EncodeToString(addr)
}

type rOConn struct {
	remoteAddr net.Addr
	localAddr  net.Addr
	closed     chan struct{}
	recvQueue  chan []byte
	sendQueue  chan []byte
}

func newrOConn() *rOConn {
	id := newClientID()
	conn := &rOConn{
		remoteAddr: id,
		localAddr:  id,
		closed:     make(chan struct{}),
		recvQueue:  make(chan []byte, 100),
		sendQueue:  make(chan []byte, 100),
	}

	go conn.dialAndExchange()

	return conn
}

func (conn *rOConn) dialAndExchange() {
	for {
		iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())
		state := state{
			connSignaling:         nil,
			peerConnection:        nil,
			conn:                  conn,
			sequenceNumer:         0,
			pictureIDTwoBytes:     0,
			fragmentedPackets:     make(map[uint32]*FragmentedPacket),
			doneSignaling:         make(chan int),
			doneSingalingBool:     false,
			iceConnectedCtx:       iceConnectedCtx,
			iceConnectedCtxCancel: iceConnectedCtxCancel,
			pendingCandidate:      make([]*webrtc.ICECandidateInit, 0),

			closed: make(chan int),
		}

		state.setupConnection()

		state.startAudioStream()
		state.startVideoStream()

		go state.handleWSMessages()
		<-state.closed
	}
}

func (conn *rOConn) QueueIncoming(p []byte) {
	conn.recvQueue <- p
}

func (conn *rOConn) OutgoingQueue() []byte {
	select {
	case packet := <-conn.sendQueue:
		return packet
	default:
		return nil
	}
}

func (conn *rOConn) lenOutgoingQueue() int {
	return len(conn.sendQueue)
}

func (conn *rOConn) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case data := <-conn.recvQueue:
		return copy(p, data), conn.RemoteAddr(), nil
	case <-conn.closed:
		return 0, nil, &net.OpError{Op: "read", Net: conn.RemoteAddr().Network(), Source: conn.LocalAddr(), Addr: conn.RemoteAddr(), Err: errors.New("closed conn")}
	}
}

func (conn *rOConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	c := make([]byte, len(p))
	copy(c, p)
	select {
	case <-conn.closed:
		return 0, &net.OpError{Op: "write", Net: conn.RemoteAddr().Network(), Source: conn.LocalAddr(), Addr: conn.RemoteAddr(), Err: errors.New("closed conn")}
	case conn.sendQueue <- c:
	default:
	}
	return len(c), nil
}

func (conn *rOConn) Close() error {
	select {

	case <-conn.closed:
		return &net.OpError{Op: "close", Net: conn.LocalAddr().Network(), Addr: conn.LocalAddr(), Err: errors.New("closed conn")}
	default:
		close(conn.closed)
		return nil
	}
}

func (conn *rOConn) LocalAddr() net.Addr {
	return conn.localAddr
}

func (conn *rOConn) RemoteAddr() net.Addr {
	return conn.remoteAddr
}

func (conn *rOConn) SetDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (conn *rOConn) SetReadDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (conn *rOConn) SetWriteDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func run() *smux.Session {
	conn := newrOConn()

	kcpConn, err := kcp.NewConn2(nil, nil, 0, 0, conn)
	if err != nil {
		return nil
	}

	kcpConn.SetNoDelay(1, 10, 2, 1)
	sess, err := smux.Client(kcpConn, &smux.Config{
		Version:           1,
		KeepAliveInterval: 10 * time.Second,
		KeepAliveTimeout:  100 * time.Second,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  4194304,
		MaxStreamBuffer:   65536})

	if err != nil {
		return nil
	}

	return sess
}

var ptInfo pt.ClientInfo

func copyLoop(a net.Conn, b *smux.Stream) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(b, a)
		wg.Done()
	}()
	go func() {
		io.Copy(a, b)
		wg.Done()
	}()

	wg.Wait()
}

func handler(conn *pt.SocksConn, sess *smux.Session) error {
	stream, err := sess.OpenStream()
	if err != nil {
		conn.Reject()
		return err
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("localhost%s", configs["kcpListener"]))
	if err != nil {
		fmt.Println("Error:", err)
		return err
	}

	err = conn.Grant(tcpAddr)
	if err != nil {
		return err
	}

	copyLoop(conn, stream)

	return nil
}

func acceptLoop(ln *pt.SocksListener, sess *smux.Session) error {
	defer ln.Close()
	for {
		conn, err := ln.AcceptSocks()
		if err != nil {
			if e, ok := err.(net.Error); ok && e.Temporary() {
				continue
			}
			return err
		}

		go handler(conn, sess)
	}
}

func main() {
	proxyType = 0
	readConfig()
	sess := run()

	var err error

	ptInfo, err = pt.ClientSetup(nil)
	if err != nil {
		os.Exit(1)
	}

	pt.ReportVersion("dummy-client", "0.1")

	if ptInfo.ProxyURL != nil {
		pt.ProxyError("proxy is not supported")
		os.Exit(1)
	}

	listeners := make([]net.Listener, 0)
	for _, methodName := range ptInfo.MethodNames {
		switch methodName {
		case "dummy":
			ln, err := pt.ListenSocks("tcp", "127.0.0.1:0")
			if err != nil {
				pt.CmethodError(methodName, err.Error())
				break
			}

			go acceptLoop(ln, sess)
			pt.Cmethod(methodName, ln.Version(), ln.Addr())

			listeners = append(listeners, ln)
		default:
			pt.CmethodError(methodName, "no such method")
		}
	}
	pt.CmethodsDone()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM)

	if os.Getenv("TOR_PT_EXIT_ON_STDIN_CLOSE") == "1" {
		go func() {
			io.Copy(ioutil.Discard, os.Stdin)
			sigChan <- syscall.SIGTERM
		}()
	}

	<-sigChan

	for _, ln := range listeners {
		ln.Close()
	}

}

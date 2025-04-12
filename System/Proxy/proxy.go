package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
	"gopkg.in/yaml.v3"
)

const (
	RECEIVED_PROXY       = 1
	READY_PION           = 0
	READY_WEB            = 5
	SIZE_OF_HEADER       = 11
	PICTURE_ID_MAX_VALUE = 32767
	PINGDEADLINE         = time.Second * 10
)

type FragmentedPacket struct {
	data      map[uint8][]byte
	lastChunk uint8
}

type peer_conn struct {
	conn_broker    *websocket.Conn
	connBridge     *websocket.Conn
	peerConnection *webrtc.PeerConnection

	pendingCandidate []*webrtc.ICECandidateInit

	signalingConnMux      sync.Mutex
	iceConnectedCtx       context.Context
	iceConnectedCtxCancel context.CancelFunc

	pictureIDTwoBytes uint64
	sequenceNumer     uint32

	recvQueue chan []byte
	sendQueue chan []byte

	doneSignaling     chan int
	doneSingalingBool bool

	fragmentedPackets map[uint32]*FragmentedPacket
}

var (
	configs map[string]interface{}
)

var first = true

func (c *peer_conn) encapsulateWeb(remaing, frame []byte, chunkNumber uint8) ([]byte, []byte, uint8) {
	var lenFrame int = len(frame)
	var result []byte = make([]byte, 0)
	var bypassBytes = 10

	if first {
		frame[0] = frame[0] & 0b11111110
		first = false
	}
	lenFrame = lenFrame - 10
	result = append(result, frame[:10]...)

	if (len(c.sendQueue) == 0 && len(remaing) == 0) || lenFrame <= SIZE_OF_HEADER {
		frame[bypassBytes] = 0
		return frame, nil, 0
	}

	var data []byte = remaing
	var reamaingArray []byte = nil

	for lenFrame > SIZE_OF_HEADER {
		if len(data) == 0 {
			select {
			case data = <-c.sendQueue:
			default:
				break
			}

			if len(data) == 0 {
				break
			}
		}

		//FLAG PACKET HAS CONTENT
		result = append(result, byte(1))

		//SEQUENCE NUMBER OF THE PACKET
		bytes := make([]byte, 4)
		binary.BigEndian.PutUint32(bytes, uint32(c.sequenceNumer))
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
			c.sequenceNumer = c.sequenceNumer + 1
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

	c.pictureIDTwoBytes++
	return result, reamaingArray, chunkNumber
}

func (c *peer_conn) encapsulate(remaing, frame []byte, chunkNumber uint8) ([]byte, []byte, uint8) {
	var value_inc int
	if c.pictureIDTwoBytes > PICTURE_ID_MAX_VALUE {
		c.pictureIDTwoBytes = 0
	}

	//FRAMES ARE DIVIDED INTO CHUNCKS OF DIFFERENT SIZES DEPNDING ON THE PICTURE ID (A FLAG ON THE FRAME). EACH CHUNK WILL BE SENT IN AN INDIVIDUAL RTP PACKET.
	if c.pictureIDTwoBytes == 0 {
		value_inc = 1187
	} else if c.pictureIDTwoBytes > 0 && c.pictureIDTwoBytes < 128 {
		value_inc = 1185
	} else {
		value_inc = 1184
	}

	//IF FRAME IS TO SMALL OR NO CONTENT NEEDS TO BE SENT, SEND FRAME AS IS BUT WITH FLAG PACKET HAS CONTENT FALSE
	if (len(c.sendQueue) == 0 && len(remaing) == 0) || len(frame) <= SIZE_OF_HEADER {

		for i := 0; i < len(frame); i += value_inc {
			frame[i] = 0
		}
		c.pictureIDTwoBytes++
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
			select {
			case data = <-c.sendQueue:
			default:
				break
			}

			if len(data) == 0 {
				break
			}
		}

		//FLAG PACKET HAS CONTENT
		result = append(result, byte(1))

		//SEQUENCE NUMBER OF THE PACKET
		bytes := make([]byte, 4)
		binary.BigEndian.PutUint32(bytes, uint32(c.sequenceNumer))
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
			lenFrame = lenFrame - SIZE_OF_HEADER
			remaingSize = value_inc
		}

		if len(data) == 0 {
			c.sequenceNumer = c.sequenceNumer + 1
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

	c.pictureIDTwoBytes++
	return result, reamaingArray, chunkNumber
}

func (c *peer_conn) desencapsulateWeb(frame []byte) {
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

		packet := c.reconstructPacket(sequenceNumber, chunk, finalChunk, data)
		if len(packet) != 0 {
			c.recvQueue <- packet
		}
	}
}

func (c *peer_conn) decapsulate(frame []byte) {
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

		packet := c.reconstructPacket(sequenceNumber, chunk, finalChunk, data)

		if len(packet) != 0 {

			c.recvQueue <- packet
		}
	}

}

func (c *peer_conn) reconstructPacket(sequenceNumber uint32, chunk, finalChunk uint8, data []byte) []byte {
	if chunk == 0 && finalChunk == 1 {
		return data
	}

	result := make([]byte, 0)
	packet, exists := c.fragmentedPackets[sequenceNumber]
	if !exists {
		fragmentedP := FragmentedPacket{
			data:      make(map[uint8][]byte),
			lastChunk: 0,
		}
		fragmentedP.data[chunk] = data
		c.fragmentedPackets[sequenceNumber] = &fragmentedP

		//Keep cleaning up stuff
		maxValueSequenceNumber := uint32(uint64(1<<32) - 1)
		getSymmetricPosition := maxValueSequenceNumber - sequenceNumber
		delete(c.fragmentedPackets, getSymmetricPosition)
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
		delete(c.fragmentedPackets, sequenceNumber)
	}

	return result
}

func (c *peer_conn) startAudioStream() {
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

			if oggErr = audioTrack.WriteSample(media.Sample{Data: pageData, Duration: sampleDuration}); oggErr != nil {
				panic(oggErr)
			}
		}
	}()
}

func (c *peer_conn) startVideoStream(clientType byte) {
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
				if clientType == READY_PION {
					newFrame, remaingData, chunkNumber = c.encapsulate(remaingData, frame, chunkNumber)
				} else {
					newFrame, remaingData, chunkNumber = c.encapsulateWeb(remaingData, frame, chunkNumber)
				}

				if ivfErr = videoTrack.WriteSample(media.Sample{Data: newFrame, Duration: time.Second}); ivfErr != nil {
					panic(ivfErr)
				}
			}
		}
	}()
}

func (c *peer_conn) handleWSMessages() {
	defer func() {
		c.conn_broker.Close()
	}()

	for {
		messageType, payload, err := c.conn_broker.ReadMessage()
		if err != nil {
			return
		}

		if messageType == websocket.BinaryMessage || messageType == websocket.TextMessage {
			var (
				candidate webrtc.ICECandidateInit
				answer    webrtc.SessionDescription
			)

			switch {
			case json.Unmarshal(payload, &answer) == nil && answer.SDP != "":
				if sdpErr := c.peerConnection.SetRemoteDescription(answer); sdpErr != nil {
					panic(sdpErr)
				}

				if len(c.pendingCandidate) > 0 {
					for _, cand := range c.pendingCandidate {
						if candidateErr := c.peerConnection.AddICECandidate(*cand); candidateErr != nil {
							panic(candidateErr)
						}
					}
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

func (c *peer_conn) connectToBridge() {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			messageType, payload, err := c.connBridge.ReadMessage()
			if err != nil {
				return
			}

			if messageType == websocket.BinaryMessage {
				c.sendQueue <- payload
			}

		}
	}()

	go func() {
		defer wg.Done()
		for {
			packet := <-c.recvQueue
			c.connBridge.WriteMessage(websocket.BinaryMessage, packet)
		}
	}()
	wg.Wait()
}

func (client *peer_conn) handleConnections(bridgeAdd string, clientType byte) {
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
		},
	}
	connBridge, _, err := dialer.Dial(bridgeAdd, nil)
	if err != nil {
		fmt.Println(err)
		return
	}

	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		},
	}

	// i := &interceptor.Registry{}
	// m := &webrtc.MediaEngine{}
	// if err := m.RegisterDefaultCodecs(); err != nil {
	// 	panic(err)
	// }

	// Create a Congestion Controller. This analyzes inbound and outbound data and provides
	// suggestions on how much we should be sending.
	//
	// Passing `nil` means we use the default Estimation Algorithm which is Google Congestion Control.
	// You can use the other ones that Pion provides, or write your own!
	// congestionController, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
	// 	return gcc.NewSendSideBWE(gcc.SendSideBWEInitialBitrate(500_000))
	// })
	// if err != nil {
	// 	panic(err)
	// }

	// estimatorChan := make(chan cc.BandwidthEstimator, 1)
	// congestionController.OnNewPeerConnection(func(id string, estimator cc.BandwidthEstimator) { //nolint: revive
	// 	estimatorChan <- estimator
	// })

	// i.Add(congestionController)
	// if err = webrtc.ConfigureTWCCHeaderExtensionSender(m, i); err != nil {
	// 	panic(err)
	// }

	// if err = webrtc.RegisterDefaultInterceptors(m, i); err != nil {
	// 	panic(err)
	// }

	// api := webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(i))

	peerConnection, err := webrtc.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}

	// estimator := <-estimatorChan

	// go func() {
	// 	for {
	// 		targetBitrate := estimator.GetTargetBitrate()
	// 		fmt.Println(targetBitrate)
	// 		time.Sleep(10 * time.Second)
	// 	}

	// }()

	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())

	client.connBridge = connBridge
	client.peerConnection = peerConnection
	client.iceConnectedCtx = iceConnectedCtx
	client.iceConnectedCtxCancel = iceConnectedCtxCancel
	client.fragmentedPackets = make(map[uint32]*FragmentedPacket)
	client.doneSingalingBool = false

	client.recvQueue = make(chan []byte, 100)
	client.sendQueue = make(chan []byte, 100)

	client.peerConnection.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}

		candBytes, err := json.Marshal(c.ToJSON())
		if err != nil {
			panic(err)
		}

		client.signalingConnMux.Lock()
		client.conn_broker.WriteMessage(websocket.BinaryMessage, candBytes)
		client.signalingConnMux.Unlock()
	})

	client.peerConnection.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			if !client.doneSingalingBool {
				close(client.doneSignaling)
			}

			client.iceConnectedCtxCancel()
			client.doneSingalingBool = true
			fmt.Println("connected")
		}

		if s == webrtc.PeerConnectionStateFailed {
			fmt.Println("Peer Connection has gone to failed exiting.")
		}

		if s == webrtc.PeerConnectionStateClosed {
			fmt.Println("Peer Connection has gone to closed exiting.")
		}
	})

	client.peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		codec := track.Codec()
		if strings.EqualFold(codec.MimeType, webrtc.MimeTypeVP8) {
			for {
				packet, _, err := track.ReadRTP()

				if err != nil {
					panic(err)
				}

				if clientType == READY_PION {
					client.decapsulate(packet.Payload)
				} else {
					client.desencapsulateWeb(packet.Payload)
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

	client.startAudioStream()
	client.startVideoStream(clientType)
	go client.handleWSMessages()
	go client.connectToBridge()

	offer, err := client.peerConnection.CreateOffer(nil)
	if err != nil {
		panic(err)
	}

	if err = client.peerConnection.SetLocalDescription(offer); err != nil {
		panic(err)
	}

	payload, err := json.Marshal(offer)
	if err != nil {
		panic(err)
	}

	client.signalingConnMux.Lock()
	client.conn_broker.WriteMessage(websocket.BinaryMessage, payload)
	client.signalingConnMux.Unlock()
}

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

func (client *peer_conn) writePing() {
	ticker := time.NewTicker(PINGDEADLINE)
	for {
		select {
		case <-client.doneSignaling:
			client.conn_broker.Close()
			return
		case <-ticker.C:
			client.signalingConnMux.Lock()
			client.conn_broker.WriteMessage(websocket.BinaryMessage, []byte("ping"))
			client.signalingConnMux.Unlock()
		}
	}
}

func handleRequests() {
	url := fmt.Sprintf("%s", configs["brokerAddr"])
	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
		},
	}

	for {
		connSignaling, _, err := dialer.Dial(url, nil)
		if err == nil {
			client := &peer_conn{
				conn_broker:      connSignaling,
				doneSignaling:    make(chan int),
				pendingCandidate: make([]*webrtc.ICECandidateInit, 0),
			}

			connSignaling.WriteMessage(websocket.BinaryMessage, []byte{RECEIVED_PROXY})

			go client.writePing()

			messageType, payload, err := connSignaling.ReadMessage()

			if err == nil && messageType == websocket.BinaryMessage {
				if payload[0] == READY_PION || payload[0] == READY_WEB {
					bridgeAddr := string(payload[1:])
					client.handleConnections(bridgeAddr, payload[0])
				} else {
					connSignaling.Close()
				}
			}
		}
	}
}

func main() {
	readConfig()
	handleRequests()
}

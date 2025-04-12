package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	mathr "math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/kcp-go"
	"github.com/xtaci/smux"
	pt "gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/goptlib"
	"gopkg.in/yaml.v3"
)

var (
	configs map[string]interface{}
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
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

func copyStream(clientConn *websocket.Conn, st *state) {
	for {
		messageType, payload, err := clientConn.ReadMessage()

		if err != nil {
			return
		}

		if messageType == websocket.BinaryMessage {
			st.QueueIncoming(payload)
		}
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request, st *state) {
	conn_client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
		return
	}

	st.lock.Lock()
	st.conn = conn_client
	st.lock.Unlock()

	if st.first {
		close(st.receivedConn)
		st.first = false
	}

	go copyStream(conn_client, st)
}

// TURBO TUNNEL
type clientID uint32

func newClientID() clientID {
	return clientID(mathr.Uint32())
}

func (addr clientID) Network() string {
	return "clientid"
}

func (addr clientID) String() string {
	return fmt.Sprintf("%08x", uint32(addr))
}

type state struct {
	closed       chan struct{}
	recvQueue    chan []byte
	localAddr    net.Addr
	conn         *websocket.Conn
	receivedConn chan int
	lock         sync.Mutex
	first        bool
}

func newQueuePacketConn() *state {
	id := newClientID()
	return &state{
		localAddr:    id,
		recvQueue:    make(chan []byte, 100),
		closed:       make(chan struct{}),
		receivedConn: make(chan int),
		first:        true,
		conn:         nil,
	}
}

func (st *state) QueueIncoming(p []byte) {
	st.recvQueue <- p
}

func (st *state) ReadFrom(p []byte) (int, net.Addr, error) {
	select {
	case packet := <-st.recvQueue:
		return copy(p, packet), st.localAddr, nil
	case <-st.closed:
		return 0, nil, &net.OpError{Op: "read", Net: st.LocalAddr().Network(), Source: st.LocalAddr(), Err: errors.New("closed conn")}
	}
}

func (st *state) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-st.closed:
		return 0, &net.OpError{Op: "write", Net: addr.Network(), Source: st.LocalAddr(), Addr: addr, Err: errors.New("closed conn")}
	default:
	}

	if st.conn != nil {
		st.lock.Lock()
		st.conn.WriteMessage(websocket.BinaryMessage, p)
		st.lock.Unlock()
	}

	return len(p), nil
}

func (st *state) Close() error {
	select {
	case <-st.closed:
		return &net.OpError{Op: "close", Net: st.LocalAddr().Network(), Addr: st.LocalAddr(), Err: errors.New("closed conn")}
	default:
		close(st.closed)
		return nil
	}
}

func (st *state) LocalAddr() net.Addr {
	return st.localAddr
}

func (st *state) SetDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (st *state) SetReadDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func (st *state) SetWriteDeadline(t time.Time) error {
	return errors.New("not implemented")
}

func run() *smux.Session {
	conn := newQueuePacketConn()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleConnections(w, r, conn)
	})

	go http.ListenAndServe(fmt.Sprintf(":%s", configs["localport"]), nil)

	pt.Log(pt.LogSeverityError, fmt.Sprintf(":%s", configs["localport"]))

	kcpConn, err := kcp.NewConn2(nil, nil, 0, 0, conn)
	if err != nil {
		panic(err)
	}

	kcpConn.SetNoDelay(1, 10, 2, 1)
	sess, err := smux.Client(kcpConn, &smux.Config{
		Version:           1,
		KeepAliveInterval: 10 * time.Second,
		KeepAliveTimeout:  60 * time.Second,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  4194304,
		MaxStreamBuffer:   65536})

	if err != nil {
		panic(err)
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

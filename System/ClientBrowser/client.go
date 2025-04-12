package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	mathr "math/rand"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xtaci/kcp-go"
	"github.com/xtaci/smux"
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

type Queue struct {
	err      bool
	end      bool
	nextItem chan []byte
	items    [][]byte
	lock     sync.Mutex
}

func NewQueue() *Queue {
	return &Queue{
		items:    make([][]byte, 0),
		nextItem: make(chan []byte, 1),
		end:      false,
		err:      false,
	}
}

func (q *Queue) Enqueue(item []byte) {
	q.lock.Lock()
	defer q.lock.Unlock()

	if len(q.nextItem) != 0 {
		q.items = append(q.items, item)
	} else {
		if len(q.items) == 0 {
			q.nextItem <- item
		} else {
			item_aux := q.items[0]
			q.items = q.items[1:]

			q.items = append(q.items, item)

			q.nextItem <- item_aux
		}
	}
}

func (q *Queue) Dequeue() ([]byte, bool) {
	if q.err {
		return nil, false
	}

	q.lock.Lock()
	if len(q.nextItem) == 0 {
		if len(q.items) != 0 {
			item_aux := q.items[0]
			q.items = q.items[1:]

			q.nextItem <- item_aux
		} else {
			if q.end {
				close(q.nextItem)
			}
		}
	}
	q.lock.Unlock()

	select {
	case item, ok := <-q.nextItem:
		return item, ok
	case <-time.After(5 * time.Second):
		return nil, true
	}
}

func (q *Queue) error() {
	q.err = true
}

func (q *Queue) close() {
	q.end = true
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

func handleLocalConn(conn *net.TCPConn, sess *smux.Session) error {
	stream, err := sess.OpenStream()
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Add(3)

	q := NewQueue()

	go func() {
		defer wg.Done()
		_, err := io.Copy(conn, stream)

		if err != nil {
			log.Printf("recv error: %v", err)
			stream.Close()
			conn.Close()
			q.error()
		}
		log.Printf("recv done")
		err = conn.CloseWrite()
		if err != nil {
			log.Printf("conn shutdown error: %v", err)
		}
	}()

	go func() {
		defer wg.Done()
		var err error
		var n int
		for {
			buf := make([]byte, 32*1024)
			n, err = conn.Read(buf)

			if err != nil {
				if err != io.EOF {
					stream.Close()
					conn.Close()
					q.error()
					log.Printf("stream %v (session %v) send err: %v", stream.ID(), stream.RemoteAddr(), err)
				}
				break
			}
			q.Enqueue(buf[:n])
		}

		q.close()
		log.Printf("stream %v (session %v) send done", stream.ID(), stream.RemoteAddr())
		err = conn.CloseRead()
		if err != nil {
			log.Printf("stream %v (session %v) CloseRead err: %v", stream.ID(), stream.RemoteAddr(), err)
		}
	}()

	go func() {
		defer wg.Done()
		for {
			p, ok := q.Dequeue()
			if ok {
				if p != nil {
					_, err := stream.Write(p)
					if err != nil {
						break
					}
				}
			} else {
				break
			}
		}
		stream.Close()
	}()

	wg.Wait()

	return nil
}

func acceptLocalConns(ln *net.TCPListener, sess *smux.Session) error {
	for {
		conn, err := ln.AcceptTCP()
		if err != nil {
			if err, ok := err.(*net.OpError); ok && err.Temporary() {
				log.Printf("temporary error in ln.Accept: %v", err)
				continue
			}
			return err
		}

		go func() {
			defer conn.Close()
			err := handleLocalConn(conn, sess)
			if err != nil {
				log.Printf("error in handleLocalConn: %v", err)
			}
		}()
	}
}

func run() error {
	conn := newQueuePacketConn()
	defer conn.Close()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleConnections(w, r, conn)
	})

	go http.ListenAndServe(fmt.Sprintf(":%s", configs["localport"]), nil)

	<-conn.receivedConn

	listenAddr := fmt.Sprintf(":%s", configs["kcpListener"])
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	defer ln.Close()

	kcpConn, err := kcp.NewConn2(nil, nil, 0, 0, conn)
	if err != nil {
		return err
	}
	defer kcpConn.Close()

	kcpConn.SetNoDelay(1, 10, 2, 1)
	sess, err := smux.Client(kcpConn, &smux.Config{
		Version:           1,
		KeepAliveInterval: 10 * time.Second,
		KeepAliveTimeout:  60 * time.Second,
		MaxFrameSize:      32768,
		MaxReceiveBuffer:  4194304,
		MaxStreamBuffer:   65536})

	if err != nil {
		return err
	}
	defer sess.Close()

	return acceptLocalConns(ln.(*net.TCPListener), sess)
}

func main() {
	readConfig()
	run()
}

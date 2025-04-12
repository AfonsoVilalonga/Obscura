package main

import (
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v2"
)

const (
	RECEIVED_PROXY = 1
	RECEIVED_WEB   = 3
	CLIENT_PION    = 2
	CLIENT_WEB     = 4
	READY_PION     = 0
	READY_WEB      = 5
	REDO           = 82

	PINGDEADLINE = time.Second * 60
)

type connInfo struct {
	conn *websocket.Conn
	recv chan []byte
	t    byte
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var (
	configs map[string]interface{}
	proxies chan *connInfo
)

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

func copyLoop(proxy *connInfo, client *connInfo) {
	var wg sync.WaitGroup
	wg.Add(2)

	err := make(chan int)

	go func() {
		defer wg.Done()
		for {
			select {
			case payload, ok := <-proxy.recv:
				if !ok {
					close(err)
					return
				}

				client.conn.WriteMessage(websocket.BinaryMessage, payload)
			case _, ok := <-err:
				if !ok {
					return
				}
			}

		}

	}()

	go func() {
		defer wg.Done()
		for {
			select {
			case payload, ok := <-client.recv:
				if !ok {
					close(err)
					return
				}

				proxy.conn.WriteMessage(websocket.BinaryMessage, payload)
			case _, ok := <-err:
				if !ok {
					return
				}
			}
		}
	}()
	wg.Wait()
}

func readWSMessages(conn *connInfo) {
	for {
		messageType, payload, err := conn.conn.ReadMessage()
		if err != nil {
			conn.conn.Close()
			close(conn.recv)
			return
		}
		if messageType == websocket.BinaryMessage || messageType == websocket.TextMessage {
			ping := string(payload)

			if ping == "ping" {
				conn.conn.SetReadDeadline(time.Now().Add(PINGDEADLINE))
			} else {
				conn.recv <- payload
			}
		}
	}
}

func handleProtocol(c *connInfo) {
	payload, ok := <-c.recv

	if !ok {
		c.conn.Close()
		return
	}

	if payload[0] == CLIENT_PION || payload[0] == CLIENT_WEB {
		c.t = 0
		p := <-proxies

		bridgeAddr := payload[1:]

		data := []byte{READY_PION}
		if payload[0] == CLIENT_WEB {
			data = []byte{READY_WEB}
		}

		data = append(data, bridgeAddr...)

		for {
			err := p.conn.WriteMessage(websocket.BinaryMessage, data)
			offer, ok := <-p.recv

			if err != nil || !ok {
				p = <-proxies
			} else {
				err1 := c.conn.WriteMessage(websocket.BinaryMessage, []byte{p.t})
				err2 := c.conn.WriteMessage(websocket.BinaryMessage, offer)
				if err1 != nil || err2 != nil {
					if p.t == RECEIVED_WEB {
						p.conn.WriteMessage(websocket.BinaryMessage, []byte{REDO})
					}
				} else {
					break
				}
			}
		}
		copyLoop(c, p)
	} else if payload[0] == RECEIVED_PROXY || payload[0] == RECEIVED_WEB {
		c.t = payload[0]
		proxies <- c
	}
}

func handleConnections(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Fatal(err)
		return
	}

	conn.SetReadDeadline(time.Now().Add(PINGDEADLINE))

	c := &connInfo{conn: conn, recv: make(chan []byte, 100)}

	go readWSMessages(c)
	go handleProtocol(c)
}

func main() {
	proxies = make(chan *connInfo, 10000)
	readConfig()
	http.HandleFunc("/ws", handleConnections)

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	server := &http.Server{
		Addr:      fmt.Sprintf("0.0.0.0:%s", configs["localport"]),
		TLSConfig: tlsConfig,
	}
	err := server.ListenAndServeTLS("Config/server.crt", "Config/server.key")
	fmt.Println(err)
}

package main

import (
	"net"
	"sync"

	"github.com/gorilla/websocket"
)

type clientMap struct {
	byAddr map[string]*client
	lock   sync.Mutex
}

type client struct {
	id   net.Addr
	conn *websocket.Conn
}

func newClientMap() *clientMap {
	m := &clientMap{
		byAddr: make(map[string]*client),
	}

	return m
}

func (m *clientMap) get(ID net.Addr) *websocket.Conn {
	m.lock.Lock()
	defer m.lock.Unlock()
	c, ok := m.byAddr[ID.String()]
	if !ok {
		return nil
	} else {
		return c.conn
	}
}

func (m *clientMap) update(ID net.Addr, conn *websocket.Conn) {
	m.lock.Lock()
	defer m.lock.Unlock()
	c, ok := m.byAddr[ID.String()]
	if !ok {
		m.byAddr[ID.String()] = &client{id: ID, conn: conn}
	} else {
		c.conn = conn
	}
}

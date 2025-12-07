package server

import (
	"net"
	"errors"
	"sync"
)

type ChannelMap struct {
	keyConn map[string]net.Conn
	connKey map[net.Conn]string
	mu sync.Mutex
}

func NewChannelMap() *ChannelMap {
	return &ChannelMap{
		keyConn: make(map[string]net.Conn),
		connKey: make(map[net.Conn]string),
	}
}

func (cm *ChannelMap) add(key string, conn net.Conn) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.keyConn[key]; exists {
		return errors.New("key already exists")
	}
	if _, exists := cm.connKey[conn]; exists {
		return errors.New("connection already registered")
	}
	cm.keyConn[key] = conn
	cm.connKey[conn] = key
	return nil
}

func (cm *ChannelMap) rem(key string) (net.Conn, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	conn, exists := cm.keyConn[key]
	if !exists {
		return nil, errors.New("no connection with this key")
	}
	delete(cm.keyConn, key)
	delete(cm.connKey, conn)
	return conn, nil
}

func (cm *ChannelMap) getKey(conn net.Conn) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	key, exists := cm.connKey[conn]
	if !exists {
		return "", errors.New("no key with this connection")
	}
	return key, nil
}

func (cm *ChannelMap) get(key string) (net.Conn, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	conn, exists := cm.keyConn[key]
	if !exists {
		return nil, errors.New("no connection with this key")
	}
	return conn, nil
}
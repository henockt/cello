package client

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/henockt/cello/internal/config"
)

const (
	connectTimeout = 10 * time.Second
)

type Client struct {
	ClientId    string
	LocalPort   string
	channelAddr string // e.g. "host:9000"
	dataAddr    string // e.g. "host:9001"
}

func NewClient(name, port, channelAddr, dataAddr string) *Client {
	return &Client{
		ClientId:    name,
		LocalPort:   port,
		channelAddr: channelAddr,
		dataAddr:    dataAddr,
	}
}

// connect to server
func (c *Client) ConnectServer() {
	dialer := net.Dialer{Timeout: connectTimeout}
	conn, err := dialer.Dial("tcp", c.channelAddr)
	if err != nil {
		log.Fatalf("Failed to connect to server at %s:\n%v", c.channelAddr, err)
	}
	defer conn.Close()
	log.Printf("Connected to server at %s", c.channelAddr)

	// send SUB:<ClientId> and wait for response
	request := fmt.Sprintf("%s:%s\n", config.ChannelRequest, c.ClientId)
	if _, err := conn.Write([]byte(request)); err != nil {
		log.Fatalf("Failed to send registration request: %v", err)
	}

	reader := bufio.NewReader(conn)
	for {
		data, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("Error reading server response: %v", err)
			return
		}

		if len(data) < 3 {
			continue
		}

		msg := data[:3]

		switch msg {
		case config.ChannelSuccess:
			log.Printf("Successfully registered client '%s'", c.ClientId)
		case config.ChannelTaken:
			log.Printf("Client name '%s' is not available, exiting", c.ClientId)
			return
		case config.ChannelPublish:
			log.Printf("Received publish request: %s", strings.TrimSpace(data))
			go handlePublish(data, c.LocalPort, c.dataAddr)
		default:
			log.Printf("Unknown message type: %s", msg)
		}
	}
}

// connects to server and sends request id, PUB:<RequestId>
// receives payload then proxies to local server
func handlePublish(pub string, localPort string, dataAddr string) {
	dialer := net.Dialer{Timeout: connectTimeout}

	// Parse request ID from publish message: "PUB:requestId\n"
	reqId := strings.TrimPrefix(pub, config.ChannelPublish+":")
	reqId = strings.TrimSuffix(reqId, "\n")

	if len(reqId) == 0 {
		log.Println("Invalid publish message: empty request id")
		return
	}

	// try several local addresses when caller provided only a port (":5173")
	var localConn net.Conn
	var err error
	if strings.HasPrefix(localPort, ":") {
		addrs := []string{"localhost", "127.0.0.1", "[::1]"}
		for _, h := range addrs {
			addr := h + localPort
			localConn, err = dialer.Dial("tcp", addr)
			if err == nil {
				localPort = addr
				break
			}
		}
	} else {
		localConn, err = dialer.Dial("tcp", localPort)
	}
	if err != nil {
		log.Printf("Failed to connect to local server at %s: %v", localPort, err)
		// Notify server that local connection failed so it can respond with error
		notifyConnectionFailure(reqId, dataAddr)
		return
	}
	defer localConn.Close()

	// Now connect to data server after confirming local server exists
	servConn, err := dialer.Dial("tcp", dataAddr)
	if err != nil {
		log.Printf("Failed to connect to data listener at %s: %v", dataAddr, err)
		return
	}
	defer servConn.Close()

	// Send request ID to server
	if _, err := servConn.Write([]byte(reqId + "\n")); err != nil {
		log.Printf("Failed to send request id to data listener: %v", err)
		return
	}

	servReader := bufio.NewReader(servConn)
	ack, err := servReader.ReadString('\n')
	if err != nil {
		log.Printf("Failed to read ACK from server: %v", err)
		return
	}

	if strings.TrimSpace(ack) != config.ChannelSuccess {
		log.Printf("Server rejected request (id: %s), response: %s", reqId, strings.TrimSpace(ack))
		return
	}

	log.Printf("Proxying request %s to local server at %s", reqId, localPort)

	var wg sync.WaitGroup

	// Bidirectional copy using CloseWrite for proper EOF signaling
	wg.Go(func() {
		if _, err := io.Copy(servConn, localConn); err != nil && err != io.EOF {
			log.Printf("Error copying local->server for request %s: %v", reqId, err)
		}
		// Signal EOF to server reader by closing write side
		if tc, ok := servConn.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
	})

	// Copy from server to local (use servReader to avoid losing bytes
	// already buffered after the ACK line).
	if _, err := io.Copy(localConn, servReader); err != nil && err != io.EOF {
		log.Printf("Error copying server->local for request %s: %v", reqId, err)
	}
	// Signal EOF to local reader by closing write side
	if tc, ok := localConn.(interface{ CloseWrite() error }); ok {
		tc.CloseWrite()
	}

	wg.Wait()

	log.Printf("Request %s completed", reqId)
}

// notifyConnectionFailure connects to data server and signals that local connection failed
func notifyConnectionFailure(reqId string, dataAddr string) {
	dialer := net.Dialer{Timeout: connectTimeout}
	conn, err := dialer.Dial("tcp", dataAddr)
	if err != nil {
		log.Printf("Failed to notify server of connection failure for request %s: %v", reqId, err)
		return
	}
	defer conn.Close()

	// Send error marker to data server
	if _, err := conn.Write([]byte(config.ChannelError + ":" + reqId + "\n")); err != nil {
		log.Printf("Failed to send error notification: %v", err)
	}
}

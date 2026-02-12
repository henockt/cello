package client

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"github.com/henockt/cello/internal/config"
)

const (
	connectTimeout = 5 * time.Second
)

type Client struct {
	ClientId string
	LocalPort string
}

func NewClient(name, port string) *Client {
	return &Client{
		ClientId: name,
		LocalPort: port,
	}
}

// connect to server
func (c *Client) ConnectServer() {
	dialer := net.Dialer{Timeout: connectTimeout}
	conn, err := dialer.Dial("tcp", config.ChannelPort)
	if err != nil {
		log.Fatalf("Failed to connect to server at %s: %v", config.ChannelPort, err)
	}
	defer conn.Close()
	log.Printf("Connected to server at %s", config.ChannelPort)

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
			go handlePublish(data, c.LocalPort)
		default:
			log.Printf("Unknown message type: %s", msg)
		}
	}
}

// connects to server and sends request id, PUB:<RequestId>
// receives payload then proxies to local server
func handlePublish(pub string, localPort string) {
	dialer := net.Dialer{Timeout: connectTimeout}

	servConn, err := dialer.Dial("tcp", config.DataPort)
	if err != nil {
		log.Printf("Failed to connect to data listener at %s: %v", config.DataPort, err)
		return
	}
	defer servConn.Close()

	localConn, err := dialer.Dial("tcp", localPort)
	if err != nil {
		log.Printf("Failed to connect to local server at %s: %v", localPort, err)
		return
	}
	defer localConn.Close()

	// Parse request ID from publish message: "PUB:requestId\n"
	reqId := strings.TrimPrefix(pub, config.ChannelPublish+":")
	reqId = strings.TrimSuffix(reqId, "\n")

	if len(reqId) == 0 {
		log.Println("Invalid publish message: empty request id")
		return
	}

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

	// Bidirectional copy with error handling
	go func() {
		if _, err := io.Copy(localConn, servReader); err != nil && err != io.EOF {
			log.Printf("Error copying server->local for request %s: %v", reqId, err)
		}
	}()

	if _, err := io.Copy(servConn, localConn); err != nil && err != io.EOF {
		log.Printf("Error copying local->server for request %s: %v", reqId, err)
	}

	log.Printf("Request %s completed", reqId)
}
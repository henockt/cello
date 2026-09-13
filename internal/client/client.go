package client

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/henockt/cello/internal/config"
)

const (
	connectTimeout = 10 * time.Second
)

type Client struct {
	ClientId    string // channel name, empty until the server assigns one
	TunnelURL   string // public URL of this tunnel, set on registration
	LocalPort   string
	channelAddr string // e.g. "host:9000"
	dataAddr    string // e.g. "host:9001"
}

func NewClient(name, port, channelAddr, dataAddr string) *Client {
	return &Client{
		// Fold here so a requested "MyApp" matches the "myapp" the server
		// registers, and is not mistaken for the server overriding the name.
		ClientId:    strings.ToLower(strings.TrimSpace(name)),
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

	// Ask for a name, or send none and let the server assign one.
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

		line := strings.TrimRight(data, "\r\n")
		if len(line) < 3 {
			continue
		}
		verb := line[:3]

		// Everything after the first ':' is payload. Splitting only on the
		// first one matters: a tunnel URL contains colons of its own.
		payload := ""
		if len(line) > 3 && line[3] == ':' {
			payload = line[4:]
		}

		switch verb {
		case config.ChannelSuccess:
			c.onRegistered(payload)
		case config.ChannelReject:
			code, msg, _ := strings.Cut(payload, ":")
			log.Printf("Server refused registration (%s): %s", code, msg)
			return
		case config.ChannelEnd:
			log.Printf("Server ended the session: %s", payload)
			return
		case config.ChannelPublish:
			go handlePublish(payload, c.LocalPort, c.dataAddr)
		default:
			log.Printf("Unknown message type: %s", verb)
		}
	}
}

// onRegistered records the tunnel URL the server assigned and reports it.
func (c *Client) onRegistered(rawURL string) {
	if rawURL == "" {
		log.Println("Server confirmed registration but sent no tunnel URL")
		return
	}
	c.TunnelURL = rawURL

	name := tunnelName(rawURL)
	if c.ClientId != "" && name != "" && name != c.ClientId {
		log.Printf("This server assigns channel names. %q was not used", c.ClientId)
	}
	if name != "" {
		c.ClientId = name
	}
	log.Printf("Tunnel live: %s -> %s", rawURL, localDisplay(c.LocalPort))
}

// tunnelName is the channel name embedded in a tunnel URL: the leftmost label
// of its host. Returns "" if the URL cannot be parsed.
func tunnelName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host, _, _ := strings.Cut(u.Hostname(), ".")
	return host
}

// localDisplay renders the local address for logs, filling in the host when
// only a port was given.
func localDisplay(localPort string) string {
	if strings.HasPrefix(localPort, ":") {
		return "localhost" + localPort
	}
	return localPort
}

// handlePublish opens a data connection for one request id, then proxies it
// to the local server.
func handlePublish(reqId string, localPort string, dataAddr string) {
	dialer := net.Dialer{Timeout: connectTimeout}

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

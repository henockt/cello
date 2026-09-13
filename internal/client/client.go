package client

import (
	"bufio"
	"crypto/tls"
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

	// cap the upgrade response scan
	maxHeaderLines = 100
)

type Client struct {
	ClientId  string // channel name, empty until the server assigns one
	TunnelURL string // public URL of this tunnel, set on registration
	LocalPort string
	server    *serverAddr
}

// serverAddr is how the client reaches the server. one host and port carry
// both control endpoints.
type serverAddr struct {
	host    string // e.g. "cello.example.com", also the TLS server name
	addr    string // e.g. "cello.example.com:443"
	useTLS  bool
	tlsConf *tls.Config
}

// NewServerAddr describes how to reach a cello server.
func NewServerAddr(host, port string, useTLS, skipVerify bool) *serverAddr {
	s := &serverAddr{
		host:   host,
		addr:   net.JoinHostPort(host, port),
		useTLS: useTLS,
	}
	if useTLS {
		s.tlsConf = &tls.Config{
			ServerName: host,
			// HTTP/2 has no Upgrade, and a proxy offers h2 by default
			NextProtos: []string{"http/1.1"},
			// each request opens a new data conn, so resume rather than
			// pay a full handshake every time
			ClientSessionCache: tls.NewLRUClientSessionCache(64),
			InsecureSkipVerify: skipVerify,
		}
	}
	return s
}

// dial connects and upgrades, returning a reader set just past the handshake.
func (s *serverAddr) dial(path string) (net.Conn, *bufio.Reader, error) {
	dialer := &net.Dialer{Timeout: connectTimeout}

	var conn net.Conn
	var err error
	if s.useTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", s.addr, s.tlsConf)
	} else {
		conn, err = dialer.Dial("tcp", s.addr)
	}
	if err != nil {
		return nil, nil, err
	}

	reader, err := upgrade(conn, s.host, path)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, reader, nil
}

// upgrade performs the handshake and returns the reader that consumed the
// response. keep using it: it may hold bytes the server sent after the 101.
func upgrade(conn net.Conn, host, path string) (*bufio.Reader, error) {
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: %s\r\nConnection: Upgrade\r\n\r\n",
		path, host, config.UpgradeToken)
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, fmt.Errorf("sending upgrade request: %w", err)
	}

	reader := config.NewFrameReader(conn)
	status, err := config.ReadFrame(reader)
	if err != nil {
		return nil, fmt.Errorf("reading upgrade response: %w", err)
	}
	if !strings.Contains(status, "101") {
		return nil, fmt.Errorf("server refused the upgrade: %s", strings.TrimSpace(status))
	}

	// drain the response headers
	for range maxHeaderLines {
		line, err := config.ReadFrame(reader)
		if err != nil {
			return nil, fmt.Errorf("reading upgrade response headers: %w", err)
		}
		if strings.TrimSpace(line) == "" {
			return reader, nil
		}
	}
	return nil, fmt.Errorf("upgrade response headers too long")
}

func NewClient(name, port string, server *serverAddr) *Client {
	return &Client{
		// fold so "MyApp" matches the "myapp" the server registers
		ClientId:  strings.ToLower(strings.TrimSpace(name)),
		LocalPort: port,
		server:    server,
	}
}

// connect to server
func (c *Client) ConnectServer() {
	conn, reader, err := c.server.dial(config.ChannelPath)
	if err != nil {
		log.Fatalf("Failed to connect to server at %s:\n%v", c.server.addr, err)
	}
	defer conn.Close()
	log.Printf("Connected to server at %s", c.server.addr)

	// Ask for a name, or send none and let the server assign one.
	request := fmt.Sprintf("%s:%s\n", config.ChannelRequest, c.ClientId)
	if _, err := conn.Write([]byte(request)); err != nil {
		log.Fatalf("Failed to send registration request: %v", err)
	}

	for {
		data, err := config.ReadFrame(reader)
		if err != nil {
			log.Printf("Error reading server response: %v", err)
			return
		}

		line := strings.TrimRight(data, "\r\n")
		if len(line) < 3 {
			continue
		}
		verb := line[:3]

		// everything after the first ':' is payload, a URL has colons too
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
			go handlePublish(payload, c.LocalPort, c.server)
		default:
			log.Printf("Unknown message type: %s", verb)
		}
	}
}

// onRegistered records the tunnel URL and reports it.
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

// tunnelName is the leftmost label of a tunnel URL's host.
func tunnelName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host, _, _ := strings.Cut(u.Hostname(), ".")
	return host
}

// localDisplay fills in the host when only a port was given.
func localDisplay(localPort string) string {
	if strings.HasPrefix(localPort, ":") {
		return "localhost" + localPort
	}
	return localPort
}

// handlePublish opens a data connection for one request and proxies it local.
func handlePublish(reqId string, localPort string, server *serverAddr) {
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
		notifyConnectionFailure(reqId, server)
		return
	}
	defer localConn.Close()

	// Now connect to the data endpoint after confirming local server exists
	servConn, servReader, err := server.dial(config.DataPath)
	if err != nil {
		log.Printf("Failed to open a data connection to %s: %v", server.addr, err)
		return
	}
	defer servConn.Close()

	// Send request ID to server
	if _, err := servConn.Write([]byte(reqId + "\n")); err != nil {
		log.Printf("Failed to send request id to data listener: %v", err)
		return
	}

	ack, err := config.ReadFrame(servReader)
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
func notifyConnectionFailure(reqId string, server *serverAddr) {
	conn, _, err := server.dial(config.DataPath)
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

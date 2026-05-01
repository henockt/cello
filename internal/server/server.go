package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"net/http"

	"github.com/henockt/cello/internal/config"
)

// Ports holds the listen addresses for each server listener.
// Each value is a full listen address, e.g. ":9000" or "0.0.0.0:9000".
type Ports struct {
	ChannelPort string
	DataPort    string
	PublicPort  string
}

type Server struct {
	cfg            Ports
	cm             ChannelMap // registered client channels, by channel name
	rm             ChannelMap // public request connections, by request ID
	DefaultChannel string     // fallback channel name for localhost/dev environments
}

func NewServer(cfg Ports, defaultChannel string) *Server {
	return &Server{
		cfg:            cfg,
		cm:             *NewChannelMap(),
		rm:             *NewChannelMap(),
		DefaultChannel: defaultChannel,
	}
}

// Setups and starts listener for a client connection
func (s *Server) StartChannel() {
	listener, err := net.Listen("tcp", s.cfg.ChannelPort)
	if err != nil {
		log.Fatal("Error starting client listener on ", s.cfg.ChannelPort)
	}
	defer listener.Close()
	log.Println("Client listener active on", s.cfg.ChannelPort)

	for {
		conn, err := listener.Accept()

		if err != nil {
			log.Println("Failed to accept connection: ", err)
			continue
		}
		log.Println("Client connected")
		go s.handleClient(conn)
	}
}

func (s *Server) handleClient(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		data, err := reader.ReadString('\n')
		if err != nil {
			key, err := s.cm.getKey(conn)
			if err != nil {
				log.Println("Client disconnected - not registered")
				return
			}
			log.Printf("Client %s disconnected: %v", key, err)
			s.cm.rem(key)
			return
		}
		if len(data) < 4 {
			continue
		}

		msg := data[:3]

		if msg == config.ChannelRequest {
			// Bounds check before slicing
			if len(data) < 5 {
				fmt.Fprintf(conn, "%s\n", config.ChannelTaken)
				log.Println("Invalid frame format")
				continue
			}
			key := data[4 : len(data)-1]
			if err := s.cm.add(key, conn); err != nil {
				fmt.Fprintf(conn, "%s\n", config.ChannelTaken)
				log.Printf("Failed to register client: %v", err)
			} else {
				fmt.Fprintf(conn, "%s\n", config.ChannelSuccess)
				log.Printf("Client %s registered", key)
			}
		}
	}
}

// public connection listener
// sends PUB:<RequestId>
func (s *Server) StartPublic() {
	listener, err := net.Listen("tcp", s.cfg.PublicPort)
	if err != nil {
		log.Fatal("Error starting public listener on ", s.cfg.PublicPort)
	}
	defer listener.Close()
	log.Println("Public listener active on", s.cfg.PublicPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Failed to accept public request: ", err)
			continue
		}
		log.Println("Received new public request")

		go s.handlePublic(conn)
	}
}

func (s *Server) handlePublic(conn net.Conn) {
	buf := new(bytes.Buffer)
	bufReader := bufio.NewReader(io.TeeReader(conn, buf))
	key := extractSubdomain(bufReader, s.DefaultChannel)

	clientConn, err := s.cm.get(key)
	if err != nil {
		sendHTTPResp(conn, 502, "Client not active")
		conn.Close()
		return
	}

	requestId := fmt.Sprintf("%d", time.Now().UnixNano())
	bufferedConn := &BufferedConn{Conn: conn, buffer: bytes.NewReader(buf.Bytes())}

	if err := s.rm.add(requestId, bufferedConn); err != nil {
		sendHTTPResp(bufferedConn, 500, "Internal server error")
		bufferedConn.Close()
		return
	}

	clientConn.Write([]byte(fmt.Sprintf("%s:%s\n", config.ChannelPublish, requestId)))

	go func(id string) {
		time.Sleep(config.RequestTimeout * time.Second)
		// rem is used as an atomic ownership claim: if it succeeds, the data
		// handler has not yet claimed this request and we own the connection.
		if expConn, err := s.rm.rem(id); err == nil {
			log.Printf("Request %s timed out, sending 504", id)
			sendHTTPResp(expConn, 504, "Client agent timed out")
			expConn.Close()
		}
	}(requestId)
}

// extractSubdomain reads HTTP headers and extracts subdomain from Host header.
// For localhost/127.0.0.1 (dev environments), it returns defaultChannel.
func extractSubdomain(reader *bufio.Reader, defaultChannel string) string {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return defaultChannel
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultChannel // No Host header found
		}
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			host := strings.TrimPrefix(line[5:], " ")
			if idx := strings.IndexByte(host, ':'); idx >= 0 {
				host = host[:idx] // Remove port
			}
			// no subdomain to extract
			if host == "localhost" || host == "127.0.0.1" {
				log.Printf("Localhost detected, using default channel: %s", defaultChannel)
				return defaultChannel
			}
			subdomain := strings.Split(host, ".")[0]
			log.Printf("Extracted subdomain: %s", subdomain)
			return subdomain
		}
	}
}

// BufferedConn wraps a net.Conn and replays buffered data before reading from underlying connection
type BufferedConn struct {
	net.Conn
	buffer          *bytes.Reader
	bufferExhausted bool
}

func (bc *BufferedConn) Read(p []byte) (int, error) {
	if !bc.bufferExhausted {
		n, err := bc.buffer.Read(p)
		// bytes.Reader's EOF is about the buffer, not the conn. drop it
		if err == io.EOF {
			bc.bufferExhausted = true
			err = nil
		}
		if n > 0 {
			return n, err
		}
	}
	return bc.Conn.Read(p)
}

func sendHTTPResp(conn net.Conn, code int, msg string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: %d\r\n\r\n%s", code, http.StatusText(code), len(msg), msg)
	if _, err := conn.Write([]byte(resp)); err != nil {
		log.Printf("Failed to send HTTP response: %v", err)
	}
}

func (s *Server) StartData() {
	listener, err := net.Listen("tcp", s.cfg.DataPort)
	if err != nil {
		log.Fatal("Error starting data listener on ", s.cfg.DataPort)
	}
	defer listener.Close()
	log.Println("Data listener active on", s.cfg.DataPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Error accepting data request: ", err)
			continue
		}
		go s.handleData(conn)
	}
}

func (s *Server) handleData(conn net.Conn) {
	defer conn.Close()

	clientReader := bufio.NewReader(conn)
	msg, err := clientReader.ReadString('\n')
	if err != nil {
		log.Printf("Error reading request id: %v", err)
		return
	}

	msg = strings.TrimSuffix(msg, "\n")
	if len(msg) == 0 {
		log.Println("Empty message")
		return
	}

	// Check for error marker from client
	if strings.HasPrefix(msg, config.ChannelError+":") {
		reqId := strings.TrimPrefix(msg, config.ChannelError+":")
		log.Printf("Client reported connection failure for request %s", reqId)
		// Atomically claim ownership so the timeout goroutine can't race us.
		if pubConn, err := s.rm.rem(reqId); err == nil {
			sendHTTPResp(pubConn, 502, "Local server not responding")
			pubConn.Close()
		}
		return
	}

	reqId := msg

	// if the timeout goroutine already fired and called rem(), this returns an
	// error
	pubConn, err := s.rm.rem(reqId)
	if err != nil {
		log.Printf("Request %s already timed out or unknown, dropping data connection", reqId)
		return
	}
	defer pubConn.Close()

	// Send ACK
	if _, err := conn.Write([]byte(config.ChannelSuccess + "\n")); err != nil {
		log.Printf("Error sending ACK for request %s: %v", reqId, err)
		return
	}

	// go io.Copy(io.MultiWriter(pubConn, os.Stdout), clientReader)
	// io.Copy(io.MultiWriter(conn, os.Stdout), pubConn)

	var wg sync.WaitGroup

	// Response path (client agent → HTTP client).
	// When done, close pubConn entirely so the request goroutine below — which
	// is blocked on pubConn.Read() waiting for the HTTP client to send more
	// data — is unblocked and can exit cleanly.
	wg.Go(func() {
		if _, err := io.Copy(pubConn, conn); err != nil && err != io.EOF {
			log.Printf("Error copying dataConn->pubConn for request %s: %v", reqId, err)
		}
		// Half-close the HTTP client side to flush the response.
		if tc, ok := pubConn.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
		// Close pubConn entirely so the request goroutine's Read unblocks.
		pubConn.Close()
	})

	// Request path (HTTP client → client agent).
	// HTTP clients never close their write side — they wait for the response.
	// This goroutine is unblocked by pubConn.Close() above once the response
	// is fully delivered.
	wg.Go(func() {
		if _, err := io.Copy(conn, pubConn); err != nil && err != io.EOF {
			log.Printf("Error copying pubConn->dataConn for request %s: %v", reqId, err)
		}
		// Half-close so the client agent's Read returns EOF.
		if tc, ok := conn.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
	})

	wg.Wait()

	log.Printf("Request %s completed", reqId)
}

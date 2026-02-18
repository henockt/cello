package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sync"
	"log"
	"net"
	"strings"
	"time"

	"net/http"

	"github.com/henockt/cello/internal/config"
)

type Server struct {
	cm ChannelMap
}

func NewServer() *Server {
	return &Server{
		cm: *NewChannelMap(),
	}
}

// Setups and starts listener for a client connection
func (s *Server) StartChannel() {
	listener, err := net.Listen("tcp", config.ChannelPort)
	if err != nil {
		log.Fatal("Error starting client listener on ", config.ChannelPort)
	}
	defer listener.Close()
	log.Println("Client listener active on", config.ChannelPort)

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
	listener, err := net.Listen("tcp", config.PublicPort)
	if err != nil {
		log.Fatal("Error starting public listener on ", config.PublicPort)
	}
	defer listener.Close()
	log.Println("Public listener active on", config.PublicPort)

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
	key := extractSubdomain(bufReader)

	clientConn, err := s.cm.get(key)
	if err != nil {
		sendHTTPResp(conn, 502, "Client not active")
		conn.Close()
		return
	}

	requestId := fmt.Sprintf("%d", time.Now().UnixNano())
	bufferedConn := &BufferedConn{Conn: conn, buffer: bytes.NewReader(buf.Bytes())}

	if err := s.cm.add(requestId, bufferedConn); err != nil {
		sendHTTPResp(bufferedConn, 500, "Internal server error")
		bufferedConn.Close()
		return
	}

	clientConn.Write([]byte(fmt.Sprintf("%s:%s\n", config.ChannelPublish, requestId)))

	// go func(id string) {
	// 	time.Sleep(15 * time.Second)
	// 	if expConn, _ := s.cm.get(id); expConn != nil {
	// 		sendHTTPResp(expConn, 504, "Client agent timed out")
	// 		expConn.Close()
	// 		s.cm.rem(id)
	// 	}
	// }(requestId)
}

// extractSubdomain reads HTTP headers and extracts subdomain from Host header
func extractSubdomain(reader *bufio.Reader) string {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "myapp"
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return "myapp" // No Host header found
		}
		if strings.HasPrefix(strings.ToLower(line), "host:") {
			host := strings.TrimPrefix(line[5:], " ")
			if idx := strings.IndexByte(host, ':'); idx >= 0 {
				host = host[:idx] // Remove port
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

func (bc *BufferedConn) Read(p []byte) (n int, err error) {
	if !bc.bufferExhausted {
		n, err := bc.buffer.Read(p)
		if n > 0 {
			return n, err
		}
		bc.bufferExhausted = true
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
	listener, err := net.Listen("tcp", config.DataPort)
	if err != nil {
		log.Fatal("Error starting data listener on ", config.DataPort)
	}
	defer listener.Close()
	log.Println("Data listener active on", config.DataPort)

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
		if pubConn, err := s.cm.get(reqId); err == nil {
			sendHTTPResp(pubConn, 502, "Local server not responding")
			pubConn.Close()
		}
		s.cm.rem(reqId)
		return
	}

	reqId := msg
	defer s.cm.rem(reqId)

	// Send ACK
	if _, err := conn.Write([]byte(config.ChannelSuccess + "\n")); err != nil {
		log.Printf("Error sending ACK for request %s: %v", reqId, err)
		return
	}

	pubConn, err := s.cm.get(reqId)
	if err != nil {
		log.Printf("Error finding public connection for request %s: %v", reqId, err)
		return
	}
	defer pubConn.Close()

	// go io.Copy(io.MultiWriter(pubConn, os.Stdout), clientReader)
	// io.Copy(io.MultiWriter(conn, os.Stdout), pubConn)

	var wg sync.WaitGroup

	// Bidirectional copy using CloseWrite for proper EOF signaling
	wg.Go(func() {
		if _, err := io.Copy(pubConn, conn); err != nil && err != io.EOF {
			log.Printf("Error copying dataConn->pubConn for request %s: %v", reqId, err)
		}
		if tc, ok := pubConn.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
	})

	if _, err := io.Copy(conn, pubConn); err != nil && err != io.EOF {
		log.Printf("Error copying pubConn->dataConn for request %s: %v", reqId, err)
	}
	if tc, ok := conn.(interface{ CloseWrite() error }); ok {
		tc.CloseWrite()
	}

	wg.Wait()

	log.Printf("Request %s completed", reqId)
}

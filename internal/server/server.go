package server

import (
	"bufio"
	// "bytes"
	"fmt"
	"io"
	// "os"
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
		log.Println("Received public request from client")

		go s.handlePublic(conn)
	}
}

func (s *Server) handlePublic(conn net.Conn) {
	// defer conn.Close() // must be closed by data handler after processing

	// send PUB:<requestId> to 'subdomains' conn
	// buf := new(bytes.Buffer)
	// tee := io.TeeReader(conn, buf)

	// reader := bufio.NewReader(tee)
	// req, err := http.ReadRequest(reader)

	// reader := bufio.NewReader(conn)
	// req, err := http.ReadRequest(reader)
	// if err != nil {
	// 	log.Println("Error parsing request")
	// 	return
	// }

	// key := strings.Split(req.Host, ".")[0]

	/*
		DEFAULT KEY
	*/
	key := "myapp"

	// check for client conn existence
	clientConn, err := s.cm.get(key)
	if err != nil {
		log.Printf("no client connection found for key '%s'", key)
		sendHTTPResp(conn, 502, "Client not active")
		return
	}

	// assign a new unique key for the public request
	requestId := fmt.Sprintf("%d", time.Now().UnixNano())

	// save (conn, requestId) pair for data listener to use
	if err := s.cm.add(requestId, conn); err != nil {
		log.Printf("Failed to add request %s: %v", requestId, err)
		sendHTTPResp(conn, 500, "Internal server error")
		return
	}

	pub := fmt.Sprintf("%s:%s\n", config.ChannelPublish, requestId)
	clientConn.Write([]byte(pub))

	go func(id string) {
		time.Sleep(10 * time.Second)
		if expiredConn, _ := s.cm.get(id); expiredConn != nil {
			log.Printf("Request %s timed out waiting for client dial-back", id)
			sendHTTPResp(expiredConn, 504, "Client agent timed out")
			expiredConn.Close()
			s.cm.rem(id)
		}
	}(requestId)
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

	go io.Copy(pubConn, clientReader)
	io.Copy(conn, pubConn)
}

package server

import (
	"bufio"
	"fmt"
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

// Setups and starts listener for a client connection
func (s *Server) StartChannel() {
	listener, err := net.Listen("tcp", config.ChannelPort)
	if err != nil {
		log.Println("Error starting client listener on ", config.ChannelPort)
	}
	defer listener.Close()
	log.Println("Client listener active on", config.ChannelPort)

	// create and initialize a new channel map
	s.cm = *NewChannelMap()

	for {
		conn, err := listener.Accept()

		if err != nil {
			log.Println("Failed to accept connection: ", err)
			continue
		}
		log.Println("Client connected")
		// Handle the connection concurrently
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
				log.Print("Client disconnected")
				return
			}
			log.Printf("Client %s disconnected", key)
			s.cm.rem(key)
			return
		}
		if len(data) <= 5 {
			continue
		}

		msg := data[:3]
		
		if msg == config.ChannelRequest {
			key := data[4 : len(data) - 1]
			if err := s.cm.add(key, conn); err != nil {
				fmt.Fprintf(conn, "%v\n", config.ChannelTaken)
				log.Println(err)
			} else {
				fmt.Fprintf(conn, "%v\n", config.ChannelSuccess)
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
	// defer conn.Close()
	
	// send PUB:<requestId> to 'subdomains' conn
	reader := bufio.NewReader(conn)
    req, err := http.ReadRequest(reader)
	if err != nil {
		log.Println("Error parsing request")
		return
	}

	key := strings.Split(req.Host, ".")[0]
	
	/*
		FOR DEVELOPMENT
	*/
	key = "myapp"
	log.Println("key:", key)

	// check for client conn existence
	clientConn, err := s.cm.get(key)
	if err != nil {
		log.Println("no client connection found")
		sendHTTPResp(conn, 502, "Client not active")
		conn.Close()
		return
	}

	// assign a new unique key for the public request
	requestId := fmt.Sprintf("%d", time.Now().UnixNano())

	// save (conn, requestId) pair for data listener to use
	s.cm.add(requestId, conn)

	pub := fmt.Sprintf("%s:%s\n", config.ChannelPublish, requestId)
	clientConn.Write([]byte(pub))

	go func(id string) {
		time.Sleep(5 * time.Second)
		if expiredConn, _ := s.cm.get(id); expiredConn != nil {
			log.Printf("Request %s timed out waiting for client dial-back", id)
			sendHTTPResp(expiredConn, 504, "Client agent timed out")
			expiredConn.Close()
		}
	}(requestId)
}

func sendHTTPResp(conn net.Conn, code int, msg string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nConnection: close\r\n\r\n%s\n", code, http.StatusText(code), msg)
	conn.Write([]byte(resp))
}
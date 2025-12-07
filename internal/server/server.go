package server

import (
	"net"
	"bufio"
	"log"

	"github.com/henockt/cello/internal/config"
)

type Server struct {
	cm ChannelMap
}

// Setups and starts listener for a client connection
func (s *Server) StartChannel() {
	listener, err := net.Listen("tcp", config.ChannelPort)
	if err != nil {
		log.Println("Error starting listener on ", config.ChannelPort)
	}
	defer listener.Close()
	log.Println("Client listener active on", config.ChannelPort)

	s.cm = NewChannelMap()

	for {
		conn, err := listener.Accept()

		if err != nil {
			log.Println("Failed to accept connection: ", err)
			continue
		}
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
			log.Println("Client disconnected") // figure which client if possible
			return
		}
		if len(data) <= 5 {
			continue
		}

		msg := data[:3]
		
		if msg == config.ChannelRequest {
			key := data[4 : len(data) - 1]
			if err := s.cm.add(key, conn); err != nil {
				conn.Write([]byte(config.ChannelTaken))
			} else {
				conn.Write([]byte(config.ChannelSuccess))
			}
		}
	}
}

func (s Server) StartPublic() {
	log.Println("TODO")
}

// Sample echo handler
func handler(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)

	for {
		data, err := reader.ReadString('\n')
		if err != nil {
			log.Println("Client disconnected")
			return
		}
		log.Print(data)
		conn.Write([]byte("Echo: " + data))
	}
}
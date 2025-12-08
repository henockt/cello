package server

import (
	"net"
	"bufio"
	"log"
	"fmt"

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

func (s *Server) StartPublic() {
	log.Println("TODO")
}
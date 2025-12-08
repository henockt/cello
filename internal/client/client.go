package client

import (
	"fmt"
	"log"
	"net"
	"bufio"

	"github.com/henockt/cello/internal/config"
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
	conn, err := net.Dial("tcp", config.ChannelPort)
	if err != nil {
		log.Fatal("Error connecting to server")
	}
	defer conn.Close()
	log.Println("connected to server")

	// send SUB:<ClientId> and wait for SUC
	fmt.Fprintf(conn, "%v:%v\n", config.ChannelRequest, c.ClientId)
	reader := bufio.NewReader(conn)
	for {
		data, err := reader.ReadString('\n')
		if err != nil {
			log.Print("error reading server response")
			return
		}
		if len(data) < 3 {
			continue
		}
		msg := data[:3]
		
		switch msg {
		case config.ChannelSuccess:
			log.Printf("successfully registered as %v", c.ClientId)
		case config.ChannelTaken:
			log.Print("name is not available, quitting...")
			return
		case config.ChannelPublish:
			go c.handlePublish(data)
		}
	}
}

// connects to server and sends request id, PUB:<RequestId>:<length>
// receives payload then proxies to local server
func (c *Client) handlePublish(pub string) {
	log.Println(pub)
}
package client

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"

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
			log.Print(data)
			go handlePublish(data, c.LocalPort)
		}
	}
}

// connects to server and sends request id, PUB:<RequestId>
// receives payload then proxies to local server
func handlePublish(pub string, localPort string) {
	servConn, err := net.Dial("tcp", config.DataPort)
	if err != nil {
		log.Println("Error connecting to data listener")
		return
	}
	defer servConn.Close()
	
	localConn, err := net.Dial("tcp", localPort)
	if err != nil {
		log.Println("Error connecting to local server")
		return
	}
	defer localConn.Close()

	reqId := strings.TrimPrefix(pub, config.ChannelPublish + ":")
	_, err = servConn.Write([]byte(reqId + "\n"))
	if err != nil {
		log.Println("Error sending request id")
		return
	}

	servReader := bufio.NewReader(servConn)
	data, err := servReader.ReadString('\n')
	if err != nil {
		log.Println("Error reading ACK")
		return
	}
	if strings.TrimSpace(data) != config.ChannelSuccess {
		log.Println("Server rejected request", data)
		return
	}
	
	go io.Copy(localConn, servReader)
	io.Copy(servConn, localConn)
}
package main

import (
	"github.com/henockt/cello/internal/server"
)

func main() {
	myServer := server.NewServer()

	go myServer.StartPublic()
	go myServer.StartData()
	myServer.StartChannel()

	// select {}
}

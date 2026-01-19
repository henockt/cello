package main

import (
	"github.com/henockt/cello/internal/server"
)

func main() {

	server := server.Server{}


	go server.StartPublic()
	server.StartChannel()
	
	select {}
}

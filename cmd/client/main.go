package main

import (
	"flag"
	"fmt"

	"github.com/henockt/cello/internal/client"
)

func main() {
	name := flag.String("name", "myapp", "a name for your channel")
	port := flag.Int("port", 3000, "port number for your local server")

	flag.Parse()

	myClient := client.NewClient(*name, fmt.Sprintf(":%v", *port))
	myClient.ConnectServer()
}

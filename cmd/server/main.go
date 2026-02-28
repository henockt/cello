package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/henockt/cello/internal/config"
	"github.com/henockt/cello/internal/server"
)

// envOrDefault returns the value of the named environment variable, or def if
// the variable is unset or empty.
func envOrDefault(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}

func main() {
	// Priority: flag > env var > built-in default
	channelPort := flag.String("channel-port", envOrDefault("CELLO_CHANNEL_PORT", config.DefaultChannelPort), "port for client channel connections")
	publicPort := flag.String("public-port", envOrDefault("CELLO_PUBLIC_PORT", config.DefaultPublicPort), "port for public HTTP connections")
	dataPort := flag.String("data-port", envOrDefault("CELLO_DATA_PORT", config.DefaultDataPort), "port for data transfer connections")

	flag.Parse()

	cfg := server.Ports{
		ChannelPort: fmt.Sprintf(":%s", *channelPort),
		PublicPort:  fmt.Sprintf(":%s", *publicPort),
		DataPort:    fmt.Sprintf(":%s", *dataPort),
	}

	myServer := server.NewServer(cfg)

	go myServer.StartPublic()
	go myServer.StartData()
	myServer.StartChannel()

	// select {}
}

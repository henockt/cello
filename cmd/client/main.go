package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/henockt/cello/internal/client"
	"github.com/henockt/cello/internal/config"
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
	name := flag.String("name", envOrDefault("CELLO_DEFAULT_CHANNEL", "myapp"), "a name for your channel")
	port := flag.Int("port", 3000, "port number for your local server")
	serverHost := flag.String("server", envOrDefault("CELLO_SERVER_HOST", "localhost"), "cello server hostname or IP")
	channelPort := flag.String("channel-port", envOrDefault("CELLO_CHANNEL_PORT", config.DefaultChannelPort), "cello server channel port")
	dataPort := flag.String("data-port", envOrDefault("CELLO_DATA_PORT", config.DefaultDataPort), "cello server data port")

	flag.Parse()

	channelAddr := fmt.Sprintf("%s:%s", *serverHost, *channelPort)
	dataAddr := fmt.Sprintf("%s:%s", *serverHost, *dataPort)

	myClient := client.NewClient(*name, fmt.Sprintf(":%v", *port), channelAddr, dataAddr)
	myClient.ConnectServer()
}

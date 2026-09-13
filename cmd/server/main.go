package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

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

// envBool is envOrDefault for boolean flags. An unparseable value falls back
// to def rather than failing, matching how the string form ignores nonsense.
func envBool(env string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(env)); err == nil {
		return v
	}
	return def
}

func main() {
	// Priority: flag > env var > built-in default
	channelPort := flag.String("channel-port", envOrDefault("CELLO_CHANNEL_PORT", config.DefaultChannelPort), "port for client channel connections")
	publicPort := flag.String("public-port", envOrDefault("CELLO_PUBLIC_PORT", config.DefaultPublicPort), "port for public HTTP connections")
	dataPort := flag.String("data-port", envOrDefault("CELLO_DATA_PORT", config.DefaultDataPort), "port for data transfer connections")
	publicBase := flag.String("public-base", envOrDefault("CELLO_PUBLIC_BASE", server.DefaultPublicBase), "base URL tunnels are published under, e.g. https://cello.example.com")
	allowClientNames := flag.Bool("allow-client-names", envBool("CELLO_ALLOW_CLIENT_NAMES", false), "let clients choose their own channel name")
	reservedNames := flag.String("reserved-names", envOrDefault("CELLO_RESERVED_NAMES", "www,api,admin,mail"), "comma-separated channel names that may never be registered")

	flag.Parse()

	cfg := server.Ports{
		ChannelPort: fmt.Sprintf(":%s", *channelPort),
		PublicPort:  fmt.Sprintf(":%s", *publicPort),
		DataPort:    fmt.Sprintf(":%s", *dataPort),
	}

	myServer, err := server.NewServer(cfg, server.Options{
		PublicBase:       *publicBase,
		AllowClientNames: *allowClientNames,
		ReservedNames:    strings.Split(*reservedNames, ","),
	})
	if err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	go myServer.StartPublic()
	go myServer.StartData()
	myServer.StartChannel()

	// select {}
}

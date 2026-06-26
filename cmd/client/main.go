package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

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

// cleanServerHost reduces a user-supplied -server value to a bare host. The
// client dials the channel/data ports over raw TCP, so a scheme, path, or
// embedded port (which belong to the public HTTPS URL, not this connection)
// would break the dial. We strip them so "https://cello.henock.me:443/foo"
// becomes "cello.henock.me".
func cleanServerHost(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:] // drop scheme
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i] // drop path
	}
	// Drop a trailing :port, but leave IPv6 literals ("[::1]") intact.
	if !strings.HasPrefix(s, "[") {
		if i := strings.LastIndexByte(s, ':'); i >= 0 {
			if _, err := strconv.Atoi(s[i+1:]); err == nil {
				s = s[:i]
			}
		}
	}
	return s
}

func main() {
	// Priority: flag > env var > built-in default
	name := flag.String("name", envOrDefault("CELLO_DEFAULT_CHANNEL", "myapp"), "a name for your channel")
	port := flag.Int("port", 3000, "port number for your local server")
	serverHost := flag.String("server", envOrDefault("CELLO_SERVER_HOST", "localhost"), "cello server hostname or IP")
	channelPort := flag.String("channel-port", envOrDefault("CELLO_CHANNEL_PORT", config.DefaultChannelPort), "cello server channel port")
	dataPort := flag.String("data-port", envOrDefault("CELLO_DATA_PORT", config.DefaultDataPort), "cello server data port")

	flag.Parse()

	host := cleanServerHost(*serverHost)
	if host != *serverHost {
		fmt.Fprintf(os.Stderr, "note: using server host %q (from %q)\n", host, *serverHost)
	}

	channelAddr := fmt.Sprintf("%s:%s", host, *channelPort)
	dataAddr := fmt.Sprintf("%s:%s", host, *dataPort)

	myClient := client.NewClient(*name, fmt.Sprintf(":%v", *port), channelAddr, dataAddr)
	myClient.ConnectServer()
}

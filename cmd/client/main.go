package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/henockt/cello/internal/client"
)

// envBool is envOrDefault for boolean flags. An unparseable value falls back to
// def rather than failing, matching how the string form ignores nonsense.
func envBool(env string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(env)); err == nil {
		return v
	}
	return def
}

// envOrDefault returns the value of the named environment variable, or def if
// the variable is unset or empty.
func envOrDefault(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}

// cleanServerHost reduces a -server value to a bare host. a scheme, path or
// port belongs to the public URL rather than the dial, so
// "https://cello.example.com:443/foo" becomes "cello.example.com".
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
	name := flag.String("name", envOrDefault("CELLO_CHANNEL_NAME", ""), "a name for your channel; empty lets the server assign one")
	port := flag.Int("port", 3000, "port number for your local server")
	serverHost := flag.String("server", envOrDefault("CELLO_SERVER_HOST", "localhost"), "cello server hostname or IP")
	serverPort := flag.String("server-port", envOrDefault("CELLO_SERVER_PORT", "443"), "port the cello server is reachable on")
	useTLS := flag.Bool("tls", envBool("CELLO_TLS", true), "connect to the server over TLS")
	skipVerify := flag.Bool("tls-skip-verify", envBool("CELLO_TLS_SKIP_VERIFY", false), "accept any server certificate (unsafe. for self-signed self-hosting)")

	flag.Parse()

	host := cleanServerHost(*serverHost)
	if host != *serverHost {
		fmt.Fprintf(os.Stderr, "note: using server host %q (from %q)\n", host, *serverHost)
	}

	if *skipVerify {
		fmt.Fprintln(os.Stderr, "warning: -tls-skip-verify disables certificate checks. the connection is not authenticated")
	}

	myClient := client.NewClient(*name, fmt.Sprintf(":%v", *port), client.NewServerAddr(host, *serverPort, *useTLS, *skipVerify))
	myClient.ConnectServer()
}

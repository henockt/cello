package main

import (
	"cmp"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/henockt/cello/internal/client"
)

// configuredServerHost reads the server host the installer recorded, so the
// domain lives in one editable file rather than compiled into the binary.
// Returns "" when there is no config, leaving the built-in default.
func configuredServerHost() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}

	b, err := os.ReadFile(filepath.Join(dir, "cello", "server"))
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	return ""
}

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
	// Priority: flag > env var > installer config > built-in default
	name := flag.String("name", envOrDefault("CELLO_CHANNEL_NAME", ""), "a name for your channel; empty lets the server assign one")
	port := flag.Int("port", 3000, "port number for your local server")
	defaultServer := cmp.Or(configuredServerHost(), "localhost")
	serverHost := flag.String("server", envOrDefault("CELLO_SERVER_HOST", defaultServer), "cello server hostname or IP")
	serverPort := flag.String("server-port", envOrDefault("CELLO_SERVER_PORT", "443"), "port the cello server is reachable on")
	useTLS := flag.Bool("tls", envBool("CELLO_TLS", true), "connect to the server over TLS")
	skipVerify := flag.Bool("tls-skip-verify", envBool("CELLO_TLS_SKIP_VERIFY", false), "accept any server certificate (unsafe. for self-signed self-hosting)")

	flag.Parse()

	// "cello 3000" is shorthand for "cello -port 3000"
	if arg := flag.Arg(0); arg != "" {
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 || n > 65535 {
			fmt.Fprintf(os.Stderr, "not a port number: %q\n", arg)
			os.Exit(2)
		}
		*port = n
	}

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

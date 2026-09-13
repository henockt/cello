package main

import (
	"flag"
	"log"
	"os"
	"strconv"
	"strings"

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
	listen := flag.String("listen", envOrDefault("CELLO_LISTEN", server.DefaultListen), "address to listen on, e.g. :3001 or 127.0.0.1:3001")
	publicBase := flag.String("public-base", envOrDefault("CELLO_PUBLIC_BASE", server.DefaultPublicBase), "base URL tunnels are published under, e.g. https://cello.example.com")
	allowClientNames := flag.Bool("allow-client-names", envBool("CELLO_ALLOW_CLIENT_NAMES", false), "let clients choose their own channel name")
	reservedNames := flag.String("reserved-names", envOrDefault("CELLO_RESERVED_NAMES", "www,api,admin,mail"), "comma-separated channel names that may never be registered")

	flag.Parse()

	cfg := server.Ports{Listen: *listen}

	myServer, err := server.NewServer(cfg, server.Options{
		PublicBase:       *publicBase,
		AllowClientNames: *allowClientNames,
		ReservedNames:    strings.Split(*reservedNames, ","),
	})
	if err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}

	myServer.Start()
}

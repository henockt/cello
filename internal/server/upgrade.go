package server

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/henockt/cello/internal/config"
)

// cap the preamble scan, a peer might never end its headers
const maxHeaderLines = 100

// preamble is the part of a request the server routes on.
type preamble struct {
	path      string // request target, e.g. "/_cello/channel"
	host      string // Host header, lowercased and without any port
	upgrade   string // Upgrade header, lowercased
	forwarded string // first X-Forwarded-For entry, if any
}

// isControl reports whether this is a tunnel client asking to upgrade.
func (p preamble) isControl(baseHost string) bool {
	return p.host == baseHost &&
		strings.HasPrefix(p.path, config.ControlPrefix) &&
		p.upgrade == config.UpgradeToken
}

func readPreamble(reader *bufio.Reader) (preamble, bool) {
	var p preamble

	line, err := reader.ReadString('\n')
	if err != nil {
		return p, false
	}
	// "GET /path HTTP/1.1"
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) != 3 {
		return p, false
	}
	p.path = parts[1]

	for range maxHeaderLines {
		line, err := reader.ReadString('\n')
		if err != nil {
			return p, false
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break // end of headers
		}

		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)

		switch strings.ToLower(strings.TrimSpace(name)) {
		case "host":
			p.host = hostWithoutPort(strings.ToLower(value))
		case "upgrade":
			p.upgrade = strings.ToLower(value)
		case "x-forwarded-for":
			// a proxy appends, the original client is first
			first, _, _ := strings.Cut(value, ",")
			p.forwarded = strings.TrimSpace(first)
		}
	}

	if p.host == "" {
		return p, false
	}
	return p, true
}

// hostWithoutPort strips a trailing port, leaving IPv6 literals intact.
func hostWithoutPort(host string) string {
	if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.HasSuffix(host, "]") {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// acceptUpgrade completes the handshake. after this the conn is not HTTP.
func acceptUpgrade(conn net.Conn) error {
	_, err := fmt.Fprintf(conn,
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: %s\r\nConnection: Upgrade\r\n\r\n",
		config.UpgradeToken)
	return err
}

// detach returns a reader continuing where r left off, reading straight from
// conn once r's buffer is drained.
func detach(r *bufio.Reader, conn net.Conn) *bufio.Reader {
	// bytes read off the socket but not consumed by the preamble. a client that
	// pipelined its first frame would otherwise lose it. only this tail is
	// replayed, the preamble itself was addressed to us.
	leftover := make([]byte, r.Buffered())
	if len(leftover) > 0 {
		if _, err := io.ReadFull(r, leftover); err != nil {
			leftover = nil
		}
	}
	return bufio.NewReader(&BufferedConn{Conn: conn, buffer: bytes.NewReader(leftover)})
}

// clientIP attributes a connection. X-Forwarded-For is client-settable, so
// only trust it from a loopback peer.
func clientIP(conn net.Conn, p preamble) string {
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return conn.RemoteAddr().String()
	}
	if p.forwarded != "" && isLoopback(host) {
		return p.forwarded
	}
	return host
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

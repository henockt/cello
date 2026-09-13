package server

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
)

func TestExtractHost(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		want   string
		wantOk bool
	}{
		{"simple host", "GET / HTTP/1.1\r\nHost: myapp.test.me\r\n\r\n", "myapp.test.me", true},
		{"host with port", "GET / HTTP/1.1\r\nHost: foo.example.com:8080\r\n\r\n", "foo.example.com", true},
		{"case-insensitive header name", "GET / HTTP/1.1\r\nhOsT: bar.example.com\r\n\r\n", "bar.example.com", true},
		{"mixed-case host is lowercased", "GET / HTTP/1.1\r\nHost: MyApp.Example.COM\r\n\r\n", "myapp.example.com", true},
		{"host header not first", "GET / HTTP/1.1\r\nUser-Agent: curl\r\nHost: baz.example.com\r\nAccept: */*\r\n\r\n", "baz.example.com", true},
		{"ipv6 literal", "GET / HTTP/1.1\r\nHost: [::1]:3001\r\n\r\n", "::1", true},
		{"missing host header", "GET / HTTP/1.1\r\nUser-Agent: curl\r\n\r\n", "", false},
		{"eof before any header", "GET / HTTP/1.1\r\n", "", false},
		{"empty host value", "GET / HTTP/1.1\r\nHost:\r\n\r\n", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tc.raw))
			// Consume the request line — extractHost only parses headers.
			if _, err := r.ReadString('\n'); err != nil {
				t.Fatalf("failed to consume request line: %v", err)
			}
			got, ok := extractHost(r)
			if got != tc.want || ok != tc.wantOk {
				t.Errorf("extractHost() = %q, %v; want %q, %v", got, ok, tc.want, tc.wantOk)
			}
		})
	}
}

func TestExtractHostStopsAtHeaderLimit(t *testing.T) {
	// A peer that never sends a blank line must not hold the scan open.
	raw := strings.Repeat("X-Filler: padding\r\n", maxHeaderLines+50)
	r := bufio.NewReader(strings.NewReader(raw))
	if got, ok := extractHost(r); ok {
		t.Errorf("extractHost() = %q, true; want \"\", false", got)
	}
}

func TestChannelFromHost(t *testing.T) {
	const base = "cello.example.com"

	tests := []struct {
		name   string
		host   string
		want   string
		wantOk bool
	}{
		{"subdomain", "myapp.cello.example.com", "myapp", true},
		{"deep subdomain takes leftmost label", "a.b.cello.example.com", "a", true},
		{"apex is not a tunnel", "cello.example.com", "", false},
		{"unrelated host", "evil.example.com", "", false},
		{"loopback is not a tunnel", "127.0.0.1", "", false},
		{"localhost is not a tunnel", "localhost", "", false},
		{"suffix match must be on a label boundary", "notcello.example.com", "", false},
		{"empty label", ".cello.example.com", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := channelFromHost(tc.host, base)
			if got != tc.want || ok != tc.wantOk {
				t.Errorf("channelFromHost(%q, %q) = %q, %v; want %q, %v", tc.host, base, got, ok, tc.want, tc.wantOk)
			}
		})
	}
}

func TestChannelFromHostLocalhostBase(t *testing.T) {
	// The dev default: myapp.localhost:3001 must still route to "myapp".
	got, ok := channelFromHost("myapp.localhost", "localhost")
	if got != "myapp" || !ok {
		t.Errorf("channelFromHost() = %q, %v; want \"myapp\", true", got, ok)
	}
}

func TestTunnelURL(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"https://cello.example.com", "https://k7m2xq.cello.example.com"},
		{"http://localhost:3001", "http://k7m2xq.localhost:3001"},
		{"https://Cello.EXAMPLE.com", "https://k7m2xq.cello.example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.base, func(t *testing.T) {
			s, err := NewServer(Ports{}, Options{PublicBase: tc.base})
			if err != nil {
				t.Fatalf("NewServer(%q) failed: %v", tc.base, err)
			}
			if got := s.tunnelURL("k7m2xq"); got != tc.want {
				t.Errorf("tunnelURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewServerRejectsBadPublicBase(t *testing.T) {
	for _, base := range []string{"cello.example.com", "ftp://cello.example.com", "https://"} {
		if _, err := NewServer(Ports{}, Options{PublicBase: base}); err == nil {
			t.Errorf("NewServer(%q) succeeded; want error", base)
		}
	}
}

func TestNewRequestID(t *testing.T) {
	const draws = 10000

	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		id, err := newRequestID()
		if err != nil {
			t.Fatalf("newRequestID() error: %v", err)
		}

		// 16 random bytes, base64url (no padding) means 22 chars.
		if len(id) != 22 {
			t.Fatalf("newRequestID() = %q, want length 22", id)
		}

		// Must decode back to exactly 16 bytes
		b, err := base64.RawURLEncoding.DecodeString(id)
		if err != nil {
			t.Fatalf("newRequestID() = %q, not valid base64url: %v", id, err)
		}
		if len(b) != 16 {
			t.Fatalf("newRequestID() decoded to %d bytes, want 16", len(b))
		}

		// no collisions across many draws.
		if _, dup := seen[id]; dup {
			t.Fatalf("newRequestID() produced duplicate %q after %d draws", id, i)
		}
		seen[id] = struct{}{}
	}
}

// Regression: BufferedConn.Read must not leak the buffer's io.EOF.
func TestBufferedConn_ReadDoesNotLeakBufferEOF(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	bc := &BufferedConn{
		Conn:   serverSide,
		buffer: bytes.NewReader([]byte("buffered")),
	}

	p := make([]byte, 8)
	n, err := bc.Read(p)
	if err != nil {
		t.Fatalf("Read returned err: %v", err)
	}
	if n != 8 || string(p) != "buffered" {
		t.Fatalf("Read returned (%d, %q), want (8, %q)", n, p[:n], "buffered")
	}

	go func() { clientSide.Write([]byte("fromwire")) }()
	q := make([]byte, 8)
	n, err = io.ReadFull(bc, q)
	if err != nil {
		t.Fatalf("subsequent Read failed: %v", err)
	}
	if n != 8 || string(q) != "fromwire" {
		t.Fatalf("subsequent Read returned (%d, %q), want (8, %q)", n, q[:n], "fromwire")
	}
}

func TestBufferedConn_ReadShorterThanBuffer(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	bc := &BufferedConn{
		Conn:   serverSide,
		buffer: bytes.NewReader([]byte("AAAABBBB")),
	}

	p := make([]byte, 4)
	if n, err := bc.Read(p); err != nil || n != 4 || string(p) != "AAAA" {
		t.Fatalf("first Read: (%d, %q, %v), want (4, AAAA, nil)", n, p[:n], err)
	}
	if n, err := bc.Read(p); err != nil || n != 4 || string(p) != "BBBB" {
		t.Fatalf("second Read: (%d, %q, %v), want (4, BBBB, nil)", n, p[:n], err)
	}

	go clientSide.Write([]byte("WIRE"))
	q := make([]byte, 4)
	if n, err := io.ReadFull(bc, q); err != nil || n != 4 || string(q) != "WIRE" {
		t.Fatalf("third Read (from wire): (%d, %q, %v), want (4, WIRE, nil)", n, q[:n], err)
	}
}

func TestBufferedConn_EmptyBufferReadsFromConn(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	bc := &BufferedConn{
		Conn:   serverSide,
		buffer: bytes.NewReader(nil),
	}

	go clientSide.Write([]byte("hello"))
	p := make([]byte, 5)
	if n, err := io.ReadFull(bc, p); err != nil || n != 5 || string(p) != "hello" {
		t.Fatalf("Read with empty buffer: (%d, %q, %v), want (5, hello, nil)", n, p[:n], err)
	}
}

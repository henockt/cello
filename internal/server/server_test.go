package server

import (
	"bytes"
	"encoding/base64"
	"io"
	"net"
	"testing"
)

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

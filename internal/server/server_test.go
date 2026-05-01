package server

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
)

func TestExtractSubdomain(t *testing.T) {
	const def = "default-channel"

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "simple subdomain",
			raw:  "GET / HTTP/1.1\r\nHost: myapp.test.me\r\n\r\n",
			want: "myapp",
		},
		{
			name: "subdomain with port",
			raw:  "GET / HTTP/1.1\r\nHost: foo.example.com:8080\r\n\r\n",
			want: "foo",
		},
		{
			name: "deep subdomain takes leftmost label",
			raw:  "GET / HTTP/1.1\r\nHost: a.b.c.example.com\r\n\r\n",
			want: "a",
		},
		{
			name: "case-insensitive header name",
			raw:  "GET / HTTP/1.1\r\nhOsT: bar.example.com\r\n\r\n",
			want: "bar",
		},
		{
			name: "host header not first",
			raw:  "GET / HTTP/1.1\r\nUser-Agent: curl\r\nHost: baz.example.com\r\nAccept: */*\r\n\r\n",
			want: "baz",
		},
		{
			name: "localhost falls back to default",
			raw:  "GET / HTTP/1.1\r\nHost: localhost:3001\r\n\r\n",
			want: def,
		},
		{
			name: "127.0.0.1 falls back to default",
			raw:  "GET / HTTP/1.1\r\nHost: 127.0.0.1:3001\r\n\r\n",
			want: def,
		},
		{
			name: "missing host header (end of headers) returns default",
			raw:  "GET / HTTP/1.1\r\nUser-Agent: curl\r\n\r\n",
			want: def,
		},
		{
			name: "EOF before any header returns default",
			raw:  "GET / HTTP/1.1\r\n",
			want: def,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader(tc.raw))
			// Consume the request line — extractSubdomain only parses headers.
			if _, err := r.ReadString('\n'); err != nil {
				t.Fatalf("failed to consume request line: %v", err)
			}
			got := extractSubdomain(r, def)
			if got != tc.want {
				t.Errorf("extractSubdomain() = %q, want %q", got, tc.want)
			}
		})
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

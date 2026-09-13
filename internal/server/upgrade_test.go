package server

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/henockt/cello/internal/config"
)

func TestReadPreamble(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantPath    string
		wantHost    string
		wantUpgrade string
		wantOk      bool
	}{
		{
			name:     "plain tunnel request",
			raw:      "GET / HTTP/1.1\r\nHost: myapp.cello.example.com\r\n\r\n",
			wantPath: "/", wantHost: "myapp.cello.example.com", wantOk: true,
		},
		{
			name:     "host with port is stripped",
			raw:      "GET / HTTP/1.1\r\nHost: foo.example.com:8080\r\n\r\n",
			wantPath: "/", wantHost: "foo.example.com", wantOk: true,
		},
		{
			name:     "mixed-case host is lowercased",
			raw:      "GET / HTTP/1.1\r\nHost: MyApp.Example.COM\r\n\r\n",
			wantPath: "/", wantHost: "myapp.example.com", wantOk: true,
		},
		{
			name:     "case-insensitive header names",
			raw:      "GET / HTTP/1.1\r\nhOsT: bar.example.com\r\n\r\n",
			wantPath: "/", wantHost: "bar.example.com", wantOk: true,
		},
		{
			name:     "ipv6 literal",
			raw:      "GET / HTTP/1.1\r\nHost: [::1]:3001\r\n\r\n",
			wantPath: "/", wantHost: "::1", wantOk: true,
		},
		{
			name: "upgrade request",
			raw: "GET /_cello/channel HTTP/1.1\r\nHost: cello.example.com\r\n" +
				"Upgrade: cello\r\nConnection: Upgrade\r\n\r\n",
			wantPath: "/_cello/channel", wantHost: "cello.example.com", wantUpgrade: "cello", wantOk: true,
		},
		{
			name:   "host header missing",
			raw:    "GET / HTTP/1.1\r\nUser-Agent: curl\r\n\r\n",
			wantOk: false,
		},
		{
			name:   "not a request line",
			raw:    "hello there\r\n\r\n",
			wantOk: false,
		},
		{
			name:   "eof before headers end",
			raw:    "GET / HTTP/1.1\r\n",
			wantOk: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := readPreamble(bufio.NewReader(strings.NewReader(tc.raw)))
			if ok != tc.wantOk {
				t.Fatalf("readPreamble() ok = %v, want %v", ok, tc.wantOk)
			}
			if !ok {
				return
			}
			if got.path != tc.wantPath || got.host != tc.wantHost || got.upgrade != tc.wantUpgrade {
				t.Errorf("readPreamble() = {path:%q host:%q upgrade:%q}, want {path:%q host:%q upgrade:%q}",
					got.path, got.host, got.upgrade, tc.wantPath, tc.wantHost, tc.wantUpgrade)
			}
		})
	}
}

func TestReadPreambleStopsAtHeaderLimit(t *testing.T) {
	// a peer that never sends a blank line must not hold the scan open
	raw := "GET / HTTP/1.1\r\n" + strings.Repeat("X-Filler: padding\r\n", maxHeaderLines+50)
	if _, ok := readPreamble(bufio.NewReader(strings.NewReader(raw))); ok {
		t.Error("readPreamble() accepted a request with no end of headers")
	}
}

func TestIsControl(t *testing.T) {
	const base = "cello.example.com"

	tests := []struct {
		name string
		p    preamble
		want bool
	}{
		{"channel upgrade on apex", preamble{path: config.ChannelPath, host: base, upgrade: "cello"}, true},
		{"data upgrade on apex", preamble{path: config.DataPath, host: base, upgrade: "cello"}, true},
		// a tunnelled app may serve /_cello/ paths of its own, on a subdomain
		{"same path on a tunnel host", preamble{path: config.ChannelPath, host: "myapp." + base, upgrade: "cello"}, false},
		{"apex without upgrade header", preamble{path: config.ChannelPath, host: base}, false},
		{"apex with wrong upgrade token", preamble{path: config.ChannelPath, host: base, upgrade: "websocket"}, false},
		{"apex with ordinary path", preamble{path: "/", host: base, upgrade: "cello"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.isControl(base); got != tc.want {
				t.Errorf("isControl() = %v, want %v", got, tc.want)
			}
		})
	}
}

// a reader that consumed the handshake may still hold protocol bytes, and
// reverting to the raw conn silently drops them.
func TestDetachKeepsBufferedBytes(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()

	// The client pipelines its first frame behind the upgrade request.
	go func() {
		io.WriteString(cli, "GET /_cello/channel HTTP/1.1\r\nHost: cello.example.com\r\n"+
			"Upgrade: cello\r\nConnection: Upgrade\r\n\r\nSUB:myapp\n")
	}()

	reader := bufio.NewReader(srv)
	if _, ok := readPreamble(reader); !ok {
		t.Fatal("readPreamble() failed")
	}

	got, err := detach(reader, srv).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the pipelined frame failed: %v", err)
	}
	if want := "SUB:myapp\n"; got != want {
		t.Errorf("first frame after detach = %q, want %q", got, want)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name      string
		remote    string
		forwarded string
		want      string
	}{
		{"direct connection", "203.0.113.7:54321", "", "203.0.113.7"},
		{"forwarded from loopback proxy", "127.0.0.1:54321", "203.0.113.7", "203.0.113.7"},
		{"first entry of a forwarded chain", "127.0.0.1:54321", "203.0.113.7, 70.41.3.18", "203.0.113.7"},
		// X-Forwarded-For is client-settable, honoring it from a non-loopback
		// peer would let a client choose its own identity and sidestep any
		// per-client limit keyed on it.
		{"forwarded from a remote peer is ignored", "203.0.113.7:54321", "10.0.0.1", "203.0.113.7"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn := &fakeAddrConn{remote: tc.remote}
			p := preamble{}
			if tc.forwarded != "" {
				first, _, _ := strings.Cut(tc.forwarded, ",")
				p.forwarded = strings.TrimSpace(first)
			}
			if got := clientIP(conn, p); got != tc.want {
				t.Errorf("clientIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeAddrConn only reports a remote address.
type fakeAddrConn struct {
	net.Conn
	remote string
}

func (c *fakeAddrConn) RemoteAddr() net.Addr { return fakeAddr(c.remote) }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

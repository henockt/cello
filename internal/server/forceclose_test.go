package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/henockt/cello/internal/config"
)

func TestForceClose(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		upgrade bool
		want    string
	}{
		{
			"adds connection close",
			"GET / HTTP/1.1\r\nHost: a.example.com\r\n\r\n",
			false,
			"GET / HTTP/1.1\r\nHost: a.example.com\r\nConnection: close\r\n\r\n",
		},
		{
			"replaces keep-alive headers",
			"GET / HTTP/1.1\r\nHost: a.example.com\r\nconnection: keep-alive\r\nKeep-Alive: timeout=5\r\nProxy-Connection: keep-alive\r\nAccept: */*\r\n\r\n",
			false,
			"GET / HTTP/1.1\r\nHost: a.example.com\r\nAccept: */*\r\nConnection: close\r\n\r\n",
		},
		{
			"upgrade is untouched",
			"GET /ws HTTP/1.1\r\nHost: a.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n",
			true,
			"GET /ws HTTP/1.1\r\nHost: a.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n",
		},
		{
			"body bytes are preserved",
			"POST / HTTP/1.1\r\nHost: a.example.com\r\nContent-Length: 11\r\n\r\nhello\r\n\r\nxy",
			false,
			"POST / HTTP/1.1\r\nHost: a.example.com\r\nContent-Length: 11\r\nConnection: close\r\n\r\nhello\r\n\r\nxy",
		},
		{
			"incomplete headers are untouched",
			"GET / HTTP/1.1\r\nHost: a.example.com\r\n",
			false,
			"GET / HTTP/1.1\r\nHost: a.example.com\r\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(forceClose([]byte(tc.in), tc.upgrade)); got != tc.want {
				t.Errorf("forceClose() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestTunnelConnectionNotReused plays a pooling front proxy: it sends a
// request, reads the response, then sends a second request on the same TCP
// connection. the second must not be delivered through the first's relay.
func TestTunnelConnectionNotReused(t *testing.T) {
	var hits atomic.Int64
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "ok")
	}))
	defer app.Close()

	s := testServer(t, false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handleConn(c)
		}
	}()

	// fake client agent: register, then serve each PUB the way handlePublish does
	ch := dialChannel(t, s)
	ch.conn.SetDeadline(time.Time{})
	reply := ch.sub(t, config.ChannelRequest)
	name := tunnelName(t, reply)
	go func() {
		for {
			line, err := ch.reader.ReadString('\n')
			if err != nil {
				return
			}
			id, ok := strings.CutPrefix(strings.TrimSpace(line), config.ChannelPublish+":")
			if ok {
				go fakePublish(t, ln.Addr().String(), app.Listener.Addr().String(), id)
			}
		}
	}()

	pub, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()
	pub.SetDeadline(time.Now().Add(5 * time.Second))
	br := bufio.NewReader(pub)
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s.cello.example.com\r\nConnection: keep-alive\r\n\r\n", name)

	if _, err := io.WriteString(pub, req); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("reading first response: %v", err)
	}
	io.ReadAll(resp.Body)
	resp.Body.Close()
	if !resp.Close {
		t.Errorf("first response did not carry Connection: close, a proxy would pool it")
	}

	// the second request on the same connection must go nowhere
	io.WriteString(pub, req)
	if resp, err := http.ReadResponse(br, nil); err == nil {
		resp.Body.Close()
		t.Errorf("second request on a reused connection got %s through the old relay", resp.Status)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("local app saw %d requests, want 1", n)
	}
}

func tunnelName(t *testing.T, reply string) string {
	t.Helper()
	u, ok := strings.CutPrefix(reply, config.ChannelSuccess+":https://")
	if !ok {
		t.Fatalf("registration failed: %q", reply)
	}
	name, _, _ := strings.Cut(u, ".")
	return name
}

// fakePublish opens a data connection for id and pipes it to the local app.
func fakePublish(t *testing.T, serverAddr, appAddr, id string) {
	local, err := net.Dial("tcp", appAddr)
	if err != nil {
		t.Errorf("dialing local app: %v", err)
		return
	}
	defer local.Close()
	data, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Errorf("dialing server: %v", err)
		return
	}
	defer data.Close()
	fmt.Fprintf(data, "GET %s HTTP/1.1\r\nHost: cello.example.com\r\nUpgrade: %s\r\nConnection: Upgrade\r\n\r\n%s\n",
		config.DataPath, config.UpgradeToken, id)
	r := bufio.NewReader(data)
	for { // 101 headers, then the ACK
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if strings.TrimSpace(line) == config.ChannelSuccess {
			break
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		io.Copy(data, local)
		data.(*net.TCPConn).CloseWrite()
	})
	io.Copy(local, r)
	local.(*net.TCPConn).CloseWrite()
	wg.Wait()
}

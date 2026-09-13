package server

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/henockt/cello/internal/config"
)

const testBase = "https://cello.example.com"

// testServer builds a server whose tunnels publish under testBase.
func testServer(t *testing.T, allowClientNames bool) *Server {
	t.Helper()
	s, err := NewServer(Ports{}, Options{
		PublicBase:       testBase,
		AllowClientNames: allowClientNames,
		ReservedNames:    []string{"www", "admin"},
	})
	if err != nil {
		t.Fatalf("NewServer() failed: %v", err)
	}
	return s
}

// channelConn is a client's end of a channel conn, kept open so the
// registration stays live.
type channelConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func dialChannel(t *testing.T, s *Server) *channelConn {
	t.Helper()
	cli, srv := net.Pipe()
	t.Cleanup(func() { cli.Close() })
	go s.handleClient(srv, bufio.NewReader(srv), preamble{host: "cello.example.com"})
	if err := cli.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("SetDeadline() failed: %v", err)
	}
	return &channelConn{conn: cli, reader: bufio.NewReader(cli)}
}

// sub sends a raw registration frame and returns the server's reply line.
func (c *channelConn) sub(t *testing.T, frame string) string {
	t.Helper()
	if _, err := fmt.Fprintf(c.conn, "%s\n", frame); err != nil {
		t.Fatalf("failed to send %q: %v", frame, err)
	}
	line, err := c.reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read reply to %q: %v", frame, err)
	}
	return strings.TrimRight(line, "\r\n")
}

func register(t *testing.T, s *Server, requested string) string {
	t.Helper()
	return dialChannel(t, s).sub(t, config.ChannelRequest+":"+requested)
}

func TestRegisterAssignsName(t *testing.T) {
	s := testServer(t, false)

	url, ok := strings.CutPrefix(register(t, s, ""), config.ChannelSuccess+":")
	if !ok {
		t.Fatalf("registration was not accepted: %q", url)
	}

	want := "https://"
	if !strings.HasPrefix(url, want) || !strings.HasSuffix(url, ".cello.example.com") {
		t.Fatalf("tunnel URL = %q, want %s<name>.cello.example.com", url, want)
	}
	name := strings.TrimSuffix(strings.TrimPrefix(url, want), ".cello.example.com")
	if len(name) != nameLen {
		t.Errorf("assigned name %q is %d characters, want %d", name, len(name), nameLen)
	}
}

func TestRegisterWithoutColonAssignsName(t *testing.T) {
	// A bare "SUB" frame is as valid as "SUB:". both mean "assign me one".
	s := testServer(t, false)
	if reply := dialChannel(t, s).sub(t, config.ChannelRequest); !strings.HasPrefix(reply, config.ChannelSuccess+":") {
		t.Errorf("bare SUB was not accepted: %q", reply)
	}
}

func TestRegisterIgnoresRequestedNameWhenNotAllowed(t *testing.T) {
	// the tunnel still comes up, with a different name
	s := testServer(t, false)

	reply := register(t, s, "myapp")
	if !strings.HasPrefix(reply, config.ChannelSuccess+":") {
		t.Fatalf("registration was not accepted: %q", reply)
	}
	if strings.Contains(reply, "myapp.") {
		t.Errorf("requested name was honored despite policy: %q", reply)
	}
}

func TestRegisterHonorsRequestedName(t *testing.T) {
	s := testServer(t, true)

	want := config.ChannelSuccess + ":https://myapp.cello.example.com"
	if reply := register(t, s, "myapp"); reply != want {
		t.Errorf("reply = %q, want %q", reply, want)
	}
}

func TestRegisterLowercasesRequestedName(t *testing.T) {
	// hostnames are case-insensitive, so "MyApp" and "myapp" are one channel
	s := testServer(t, true)

	want := config.ChannelSuccess + ":https://myapp.cello.example.com"
	if reply := register(t, s, "MyApp"); reply != want {
		t.Errorf("reply = %q, want %q", reply, want)
	}
}

func TestRegisterRejections(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		wantCode  string
	}{
		{"invalid characters", "my_app", config.NakInvalid},
		{"too long", strings.Repeat("a", maxNameLen+1), config.NakInvalid},
		{"reserved", "admin", config.NakReserved},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer(t, true)
			reply := register(t, s, tc.requested)
			want := config.ChannelReject + ":" + tc.wantCode + ":"
			if !strings.HasPrefix(reply, want) {
				t.Errorf("reply = %q, want prefix %q", reply, want)
			}
		})
	}
}

func TestRegisterRejectsTakenName(t *testing.T) {
	s := testServer(t, true)

	if reply := register(t, s, "myapp"); !strings.HasPrefix(reply, config.ChannelSuccess+":") {
		t.Fatalf("first registration failed: %q", reply)
	}

	reply := register(t, s, "myapp")
	want := config.ChannelReject + ":" + config.NakTaken + ":"
	if !strings.HasPrefix(reply, want) {
		t.Errorf("second registration reply = %q, want prefix %q", reply, want)
	}
}

func TestRegisterRejectsSecondNameOnSameConnection(t *testing.T) {
	s := testServer(t, true)
	c := dialChannel(t, s)

	if reply := c.sub(t, config.ChannelRequest+":myapp"); !strings.HasPrefix(reply, config.ChannelSuccess+":") {
		t.Fatalf("first registration failed: %q", reply)
	}

	reply := c.sub(t, config.ChannelRequest+":other")
	want := config.ChannelReject + ":" + config.NakInvalid + ":"
	if !strings.HasPrefix(reply, want) {
		t.Errorf("second registration reply = %q, want prefix %q", reply, want)
	}
}

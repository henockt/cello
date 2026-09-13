package server

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"net/http"

	"github.com/henockt/cello/internal/config"
)

// DefaultPublicBase suits local development. a client registered as "myapp" is
// then reachable at http://myapp.localhost:3001
const DefaultPublicBase = "http://localhost:3001"

// Ports holds the listen addresses for each server listener.
// Each value is a full listen address, e.g. ":9000" or "0.0.0.0:9000".
type Ports struct {
	ChannelPort string
	DataPort    string
	PublicPort  string
}

// Options holds server policy, as distinct from the listen addresses.
type Options struct {
	// PublicBase is the base URL tunnels are published under
	PublicBase string

	AllowClientNames bool

	// ReservedNames can never be registered. Only consulted when
	// AllowClientNames is set, since generated names cannot collide with them
	ReservedNames []string
}

type Server struct {
	cfg              Ports
	cm               ChannelMap // registered client channels, by channel name
	rm               ChannelMap // public request connections, by request ID
	allowClientNames bool
	reserved         map[string]bool
	publicScheme     string // "http" or "https"
	publicHostPort   string // host with port if non-default, e.g. "localhost:3001"
	baseHost         string // host without port
}

func NewServer(cfg Ports, opts Options) (*Server, error) {
	base := opts.PublicBase
	if base == "" {
		base = DefaultPublicBase
	}
	u, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("invalid public base %q: %w", base, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("public base %q must start with http:// or https://", base)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("public base %q has no host", base)
	}

	reserved := make(map[string]bool, len(opts.ReservedNames))
	for _, n := range opts.ReservedNames {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			reserved[n] = true
		}
	}

	return &Server{
		cfg:              cfg,
		cm:               *NewChannelMap(),
		rm:               *NewChannelMap(),
		allowClientNames: opts.AllowClientNames,
		reserved:         reserved,
		publicScheme:     u.Scheme,
		publicHostPort:   strings.ToLower(u.Host),
		baseHost:         strings.ToLower(u.Hostname()),
	}, nil
}

// tunnelURL is the public URL a channel is reachable at.
func (s *Server) tunnelURL(name string) string {
	return fmt.Sprintf("%s://%s.%s", s.publicScheme, name, s.publicHostPort)
}

// Setups and starts listener for a client connection
func (s *Server) StartChannel() {
	listener, err := net.Listen("tcp", s.cfg.ChannelPort)
	if err != nil {
		log.Fatal("Error starting client listener on ", s.cfg.ChannelPort)
	}
	defer listener.Close()
	log.Println("Client listener active on", s.cfg.ChannelPort)

	for {
		conn, err := listener.Accept()

		if err != nil {
			log.Println("Failed to accept connection: ", err)
			continue
		}
		log.Println("Client connected")
		go s.handleClient(conn)
	}
}

func (s *Server) handleClient(conn net.Conn) {
	defer conn.Close()
	enableKeepAlive(conn)
	reader := bufio.NewReader(conn)

	for {
		data, err := reader.ReadString('\n')
		if err != nil {
			key, errk := s.cm.getKey(conn)
			if errk != nil {
				log.Println("Client disconnected - not registered")
				return
			}
			log.Printf("Client %s disconnected: %v", key, err)
			s.cm.rem(key)
			return
		}
		line := strings.TrimRight(data, "\r\n")
		if len(line) < 3 {
			continue
		}

		if line[:3] != config.ChannelRequest {
			log.Printf("Ignoring unknown frame %q", line[:3])
			continue
		}

		// "SUB", "SUB:" and "SUB:<name>" are all valid. the first two ask the
		// server to assign a name. Names are lowercased on the way in because
		// hostnames are case-insensitive
		requested, _ := strings.CutPrefix(line, config.ChannelRequest+":")
		if requested == line {
			requested = ""
		}
		s.register(conn, strings.ToLower(strings.TrimSpace(requested)))
	}
}

// register resolves a channel name for conn and replies ACK or NAK. A client
// may request a name, but whether that request is honored is server policy
// when it is not, the client is given an assigned name rather than an error,
// so the tunnel still comes up.
func (s *Server) register(conn net.Conn, requested string) {
	if existing, err := s.cm.getKey(conn); err == nil {
		s.reject(conn, config.NakInvalid, fmt.Sprintf("connection is already registered as %q", existing))
		return
	}

	if s.allowClientNames && requested != "" {
		if err := validateChannelName(requested); err != nil {
			s.reject(conn, config.NakInvalid, err.Error())
			return
		}
		if s.reserved[requested] {
			s.reject(conn, config.NakReserved, fmt.Sprintf("channel name %q is reserved", requested))
			return
		}
		if err := s.cm.add(requested, conn); err != nil {
			s.reject(conn, config.NakTaken, fmt.Sprintf("channel name %q is already in use", requested))
			return
		}
		s.accept(conn, requested, requested)
		return
	}

	// Assign a name. A collision is improbable rather than impossible, so the
	// add is what claims the name and a clash just means trying again.
	for range nameAttempts {
		name, err := newChannelName()
		if err != nil {
			log.Printf("Failed to generate a channel name: %v", err)
			s.reject(conn, config.NakInternal, "server could not assign a name")
			return
		}
		if err := s.cm.add(name, conn); err == nil {
			s.accept(conn, name, requested)
			return
		}
	}
	log.Printf("Gave up assigning a channel name after %d attempts", nameAttempts)
	s.reject(conn, config.NakInternal, "server could not assign a free name")
}

// accept confirms a registration, handing the client the URL its tunnel is
// published at. client cannot construct this itself, since the public
// scheme and port are known only to the server.
func (s *Server) accept(conn net.Conn, name, requested string) {
	url := s.tunnelURL(name)
	if _, err := fmt.Fprintf(conn, "%s:%s\n", config.ChannelSuccess, url); err != nil {
		log.Printf("Failed to confirm registration of %s: %v", name, err)
		s.cm.rem(name)
		return
	}
	if requested != "" && requested != name {
		log.Printf("Client requested %q; assigned %q", requested, name)
	}
	log.Printf("Client registered as %s (%s)", name, url)
}

// reject refuses a registration with a code and a message
// meant for the user. Newlines are stripped because the frame is one line.
func (s *Server) reject(conn net.Conn, code, msg string) {
	msg = strings.ReplaceAll(msg, "\n", " ")
	log.Printf("Registration rejected (%s): %s", code, msg)
	if _, err := fmt.Fprintf(conn, "%s:%s:%s\n", config.ChannelReject, code, msg); err != nil {
		log.Printf("Failed to send rejection: %v", err)
	}
}

// enableKeepAlive turns on TCP keepalive so dead connections are detected and
// cleaned up instead of lingering. No-op for non-TCP connections.
func enableKeepAlive(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}
}

// public connection listener
// sends PUB:<RequestId>
func (s *Server) StartPublic() {
	listener, err := net.Listen("tcp", s.cfg.PublicPort)
	if err != nil {
		log.Fatal("Error starting public listener on ", s.cfg.PublicPort)
	}
	defer listener.Close()
	log.Println("Public listener active on", s.cfg.PublicPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Failed to accept public request: ", err)
			continue
		}
		log.Println("Received new public request")

		go s.handlePublic(conn)
	}
}

func (s *Server) handlePublic(conn net.Conn) {
	buf := new(bytes.Buffer)
	bufReader := bufio.NewReader(io.TeeReader(conn, buf))

	host, ok := extractHost(bufReader)
	if !ok {
		sendHTTPResp(conn, 400, "Missing or malformed Host header")
		conn.Close()
		return
	}
	key, ok := channelFromHost(host, s.baseHost)
	if !ok {
		sendHTTPResp(conn, 404, fmt.Sprintf("No tunnel specified: this server publishes tunnels under *.%s", s.baseHost))
		conn.Close()
		return
	}

	clientConn, err := s.cm.get(key)
	if err != nil {
		sendHTTPResp(conn, 502, "Client not active")
		conn.Close()
		return
	}

	requestId, err := newRequestID()
	if err != nil {
		log.Printf("Failed to generate request id: %v", err)
		sendHTTPResp(conn, 500, "Internal server error")
		conn.Close()
		return
	}
	bufferedConn := &BufferedConn{Conn: conn, buffer: bytes.NewReader(buf.Bytes())}

	if err := s.rm.add(requestId, bufferedConn); err != nil {
		sendHTTPResp(bufferedConn, 500, "Internal server error")
		bufferedConn.Close()
		return
	}

	clientConn.Write([]byte(fmt.Sprintf("%s:%s\n", config.ChannelPublish, requestId)))

	go func(id string) {
		time.Sleep(config.RequestTimeout * time.Second)
		// rem is used as an atomic ownership claim: if it succeeds, the data
		// handler has not yet claimed this request and we own the connection.
		if expConn, err := s.rm.rem(id); err == nil {
			log.Printf("Request %s timed out, sending 504", id)
			sendHTTPResp(expConn, 504, "Client agent timed out")
			expConn.Close()
		}
	}(requestId)
}

// newRequestID returns a random, URL-safe request identifier
// request IDs must be unguessable
func newRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// maxHeaderLines bounds the header scan so a peer that never sends a blank
// line cannot hold the goroutine open indefinitely.
const maxHeaderLines = 100

// extractHost scans request headers for the Host header, returning it
// lowercased and without any port. ok is false when the request carries no
// usable Host
func extractHost(reader *bufio.Reader) (string, bool) {
	for range maxHeaderLines {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", false
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return "", false // end of headers, no Host
		}

		// Lowercase up front: the header name is case-insensitive, and so is
		// the host, which is the map key everything downstream routes on.
		rest, ok := strings.CutPrefix(strings.ToLower(line), "host:")
		if !ok {
			continue
		}
		host := strings.TrimSpace(rest)

		// Strip the port, leaving IPv6 literals ("[::1]", "[::1]:3001") intact.
		if i := strings.LastIndexByte(host, ':'); i >= 0 && !strings.HasSuffix(host, "]") {
			host = host[:i]
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			return "", false
		}
		return host, true
	}
	return "", false
}

// channelFromHost returns the channel a host routes to: the leftmost label of
// a subdomain of baseHost. ok is false when the host names the server itself
// rather than a tunnel, so that case can be answered rather than routed.
func channelFromHost(host, baseHost string) (string, bool) {
	suffix := "." + baseHost
	if !strings.HasSuffix(host, suffix) {
		return "", false // the apex itself, or a host this server does not serve
	}
	label := strings.TrimSuffix(host, suffix)
	if i := strings.IndexByte(label, '.'); i >= 0 {
		label = label[:i] // deeper subdomain: the leftmost label wins
	}
	if label == "" {
		return "", false
	}
	return label, true
}

// BufferedConn wraps a net.Conn and replays buffered data before reading from underlying connection
type BufferedConn struct {
	net.Conn
	buffer          *bytes.Reader
	bufferExhausted bool
}

func (bc *BufferedConn) Read(p []byte) (int, error) {
	if !bc.bufferExhausted {
		n, err := bc.buffer.Read(p)
		// bytes.Reader's EOF is about the buffer, not the conn. drop it
		if err == io.EOF {
			bc.bufferExhausted = true
			err = nil
		}
		if n > 0 {
			return n, err
		}
	}
	return bc.Conn.Read(p)
}

func sendHTTPResp(conn net.Conn, code int, msg string) {
	resp := fmt.Sprintf("HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nConnection: close\r\nContent-Length: %d\r\n\r\n%s", code, http.StatusText(code), len(msg), msg)
	if _, err := conn.Write([]byte(resp)); err != nil {
		log.Printf("Failed to send HTTP response: %v", err)
	}
}

// idleTimeout bounds how long a relayed connection may sit with neither side
// sending before the server reclaims it. A transparent TCP tunnel cannot know
// where an HTTP response ends, so on an HTTP/1.1 keepalive request neither the
// local service nor the browser closes and the relay would otherwise pin two
// connections and two goroutines indefinitely.
const idleTimeout = 2 * time.Minute

// deadlineSetter is the subset of net.Conn that copyIdle needs to bound a read.
type deadlineSetter interface {
	SetReadDeadline(time.Time) error
}

// copyIdle copies src into dst, refreshing a read deadline on dl before every
// read so a silent peer is reclaimed after idle rather than blocking forever.
// src and dl are separate because the response path reads through a
// bufio.Reader while the deadline belongs to the connection underneath it.
// io.Copy cannot be used here: it would never refresh the deadline, and
// bufio.Reader's WriteTo would bypass the loop entirely.
func copyIdle(dst io.Writer, src io.Reader, dl deadlineSetter, idle time.Duration) (int64, error) {
	buf := make([]byte, 32*1024)
	var total int64

	for {
		if err := dl.SetReadDeadline(time.Now().Add(idle)); err != nil {
			return total, err
		}

		n, rerr := src.Read(buf)
		if n > 0 {
			w, werr := dst.Write(buf[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return total, nil
			}
			return total, rerr
		}
	}
}

// logRelayErr reports a relay copy failure, staying quiet about the two errors
// that are how a relay normally ends: net.ErrClosed, which is how the sibling
// goroutine's deliberate Close unblocks a pending Read, and a deadline expiry,
// which is the idle reclaim working as intended.
func logRelayErr(dir, reqId string, err error) {
	switch {
	case errors.Is(err, net.ErrClosed):
	case errors.Is(err, os.ErrDeadlineExceeded):
		log.Printf("Request %s idle for %s, closing %s relay", reqId, idleTimeout, dir)
	default:
		log.Printf("Error copying %s for request %s: %v", dir, reqId, err)
	}
}

func (s *Server) StartData() {
	listener, err := net.Listen("tcp", s.cfg.DataPort)
	if err != nil {
		log.Fatal("Error starting data listener on ", s.cfg.DataPort)
	}
	defer listener.Close()
	log.Println("Data listener active on", s.cfg.DataPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Error accepting data request: ", err)
			continue
		}
		go s.handleData(conn)
	}
}

func (s *Server) handleData(conn net.Conn) {
	defer conn.Close()
	enableKeepAlive(conn)

	clientReader := bufio.NewReader(conn)
	msg, err := clientReader.ReadString('\n')
	if err != nil {
		log.Printf("Error reading request id: %v", err)
		return
	}

	msg = strings.TrimSuffix(msg, "\n")
	if len(msg) == 0 {
		log.Println("Empty message")
		return
	}

	// Check for error marker from client
	if strings.HasPrefix(msg, config.ChannelError+":") {
		reqId := strings.TrimPrefix(msg, config.ChannelError+":")
		log.Printf("Client reported connection failure for request %s", reqId)
		// Atomically claim ownership so the timeout goroutine can't race us.
		if pubConn, err := s.rm.rem(reqId); err == nil {
			sendHTTPResp(pubConn, 502, "Local server not responding")
			pubConn.Close()
		}
		return
	}

	reqId := msg

	// if the timeout goroutine already fired and called rem(), this returns an
	// error
	pubConn, err := s.rm.rem(reqId)
	if err != nil {
		log.Printf("Request %s already timed out or unknown, dropping data connection", reqId)
		return
	}
	defer pubConn.Close()

	// Send ACK
	if _, err := conn.Write([]byte(config.ChannelSuccess + "\n")); err != nil {
		log.Printf("Error sending ACK for request %s: %v", reqId, err)
		return
	}

	// go io.Copy(io.MultiWriter(pubConn, os.Stdout), clientReader)
	// io.Copy(io.MultiWriter(conn, os.Stdout), pubConn)

	var wg sync.WaitGroup

	// Response path (client agent -> HTTP client).
	// Copies from clientReader, not conn: the bufio.Reader that consumed the
	// request-ID line above may also hold payload bytes that arrived in the
	// same read, and reading the bare conn would discard them.
	// When done, close pubConn entirely so the request goroutine below which
	// is blocked on pubConn.Read() waiting for the HTTP client to send more
	// data — is unblocked and can exit cleanly.
	wg.Go(func() {
		if _, err := copyIdle(pubConn, clientReader, conn, idleTimeout); err != nil {
			logRelayErr("dataConn->pubConn", reqId, err)
		}
		// Half-close the HTTP client side to flush the response.
		if tc, ok := pubConn.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
		// Close pubConn entirely so the request goroutine's Read unblocks.
		pubConn.Close()
	})

	// Request path (HTTP client -> client agent).
	// HTTP clients never close their write side. they wait for the response.
	// This goroutine is unblocked by pubConn.Close() above once the response
	// is fully delivered, or by its own idle deadline.
	wg.Go(func() {
		if _, err := copyIdle(conn, pubConn, pubConn, idleTimeout); err != nil {
			logRelayErr("pubConn->dataConn", reqId, err)
		}
		// Half-close so the client agent's Read returns EOF.
		if tc, ok := conn.(interface{ CloseWrite() error }); ok {
			tc.CloseWrite()
		}
	})

	wg.Wait()

	log.Printf("Request %s completed", reqId)
}

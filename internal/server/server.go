package server

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/tls"
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

// behind a proxy this should be loopback only, e.g. "127.0.0.1:3001"
const DefaultListen = ":3001"

// Ports holds the listen address. visitors and clients share it.
type Ports struct {
	Listen string
}

// Options holds server policy.
type Options struct {
	// PublicBase is the base URL tunnels are published under
	PublicBase string

	AllowClientNames bool

	// ReservedNames can never be registered. only checked for client-chosen
	// names, generated ones are too long to collide
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

// Start accepts visitors and tunnel clients on one port.
func (s *Server) Start() {
	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		log.Fatalf("Error starting listener on %s: %v", s.cfg.Listen, err)
	}
	defer listener.Close()
	log.Printf("Listening on %s, publishing tunnels under *.%s", s.cfg.Listen, s.baseHost)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Failed to accept connection: ", err)
			continue
		}
		go s.handleConn(conn)
	}
}

// handleConn routes a new connection to the control protocol or the relay.
func (s *Server) handleConn(conn net.Conn) {
	// tee the preamble so a tunnel request can be replayed to the client
	buf := new(bytes.Buffer)
	reader := config.NewFrameReader(io.TeeReader(conn, buf))

	req, ok := readPreamble(reader)
	if !ok {
		sendHTTPResp(conn, 400, "Malformed request")
		conn.Close()
		return
	}

	if req.isControl(s.baseHost) {
		s.handleControl(conn, reader, req)
		return
	}
	s.handleTunnel(conn, buf, req)
}

// handleControl upgrades the connection and dispatches by endpoint.
func (s *Server) handleControl(conn net.Conn, reader *bufio.Reader, req preamble) {
	var handler func(net.Conn, *bufio.Reader, preamble)
	switch req.path {
	case config.ChannelPath:
		handler = s.handleClient
	case config.DataPath:
		handler = s.handleData
	default:
		sendHTTPResp(conn, 404, "Unknown control endpoint")
		conn.Close()
		return
	}

	if err := acceptUpgrade(conn); err != nil {
		log.Printf("Failed to complete upgrade for %s: %v", req.path, err)
		conn.Close()
		return
	}
	// stop teeing, this conn outlives the request that opened it
	handler(conn, detach(reader, conn), req)
}

func (s *Server) handleClient(conn net.Conn, reader *bufio.Reader, req preamble) {
	defer conn.Close()
	enableKeepAlive(conn)
	log.Printf("Client connected from %s", clientIP(conn, req))

	for {
		data, err := config.ReadFrame(reader)
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

		// "SUB", "SUB:" and "SUB:<name>" are all valid, the first two ask for
		// an assigned name. lowercased since hostnames are case-insensitive
		requested, _ := strings.CutPrefix(line, config.ChannelRequest+":")
		if requested == line {
			requested = ""
		}
		s.register(conn, strings.ToLower(strings.TrimSpace(requested)))
	}
}

// register picks a channel name for conn and replies ACK or NAK. a requested
// name is honored only if policy allows it, otherwise one is assigned.
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

	// the add claims the name, so a collision just means trying again
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

// accept confirms a registration with the tunnel URL. only the server knows
// the public scheme and port, so only it can build this.
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

// reject refuses a registration. newlines are stripped, a frame is one line.
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
	// unwrap TLS first, a *tls.Conn is not a *net.TCPConn
	if tc, ok := conn.(*tls.Conn); ok {
		conn = tc.NetConn()
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}
}

// handleTunnel relays one visitor request to the tunnel its Host names. buf
// holds what was already read, replayed so the client gets the request intact.
func (s *Server) handleTunnel(conn net.Conn, buf *bytes.Buffer, req preamble) {
	key, ok := channelFromHost(req.host, s.baseHost)
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

// channelFromHost returns the leftmost label of a subdomain of baseHost.
// ok is false when the host names the server itself rather than a tunnel.
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

// idleTimeout reclaims a relay that has gone quiet. a TCP tunnel cannot tell
// where an HTTP response ends, so on keepalive neither side ever closes.
const idleTimeout = 2 * time.Minute

// deadlineSetter is the subset of net.Conn that copyIdle needs to bound a read.
type deadlineSetter interface {
	SetReadDeadline(time.Time) error
}

// copyIdle copies src to dst, refreshing a read deadline before every read.
// src and dl differ when reading through a bufio.Reader: the deadline belongs
// to the conn underneath. io.Copy would never refresh it.
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

// logRelayErr reports a copy failure, ignoring the two errors that are how a
// relay normally ends: the sibling goroutine's Close, and the idle deadline.
func logRelayErr(dir, reqId string, err error) {
	switch {
	case errors.Is(err, net.ErrClosed):
	case errors.Is(err, os.ErrDeadlineExceeded):
		log.Printf("Request %s idle for %s, closing %s relay", reqId, idleTimeout, dir)
	default:
		log.Printf("Error copying %s for request %s: %v", dir, reqId, err)
	}
}

func (s *Server) handleData(conn net.Conn, clientReader *bufio.Reader, _ preamble) {
	defer conn.Close()
	enableKeepAlive(conn)

	msg, err := config.ReadFrame(clientReader)
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
	// Reads clientReader, not conn: it may hold payload that arrived with the
	// request-ID line. The Close at the end unblocks the goroutine below.
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
	// HTTP clients never close their write side, they wait for the response.
	// pubConn.Close() above unblocks this, or the idle deadline does.
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

package config

import (
	"bufio"
	"errors"
	"io"
)

/*
This package includes common communication
configurations between client and server
*/

// Control endpoints. clients reach these on the server's own host and upgrade
// the connection, everything else is visitor traffic for a tunnel.
const (
	ControlPrefix = "/_cello/"
	ChannelPath   = "/_cello/channel"
	DataPath      = "/_cello/data"

	// the protocol named in the Upgrade header
	UpgradeToken = "cello"
)

// Channel verbs. each frame is one line, the first three bytes name the verb
// and anything after the first ':' is payload.
const (
	ChannelRequest = "SUB" // SUB:<name>, or bare SUB / SUB: to be assigned one
	ChannelSuccess = "ACK" // ACK:<url> on the channel port; bare ACK on the data port
	ChannelReject  = "NAK" // NAK:<code>:<message>, registration refused
	ChannelEnd     = "END" // END:<reason>, server is closing an established session
	ChannelPublish = "PUB" // PUB:<RequestId>
	ChannelError   = "ERR" // ERR:<RequestId>
)

// NAK codes. the client retries or gives up based on the code, and shows the
// message to the user as-is.
const (
	NakTaken    = "taken"    // name is in use, another name may work
	NakInvalid  = "invalid"  // name is malformed, or the request made no sense
	NakReserved = "reserved" // name is on the server's reserved list
	NakInternal = "internal" // the server failed, through no fault of the client
)

const (
	// the maximum time the server waits for the client agent
	// to claim a public request before responding with 504.
	RequestTimeout = 30
)

// MaxFrameLen bounds one protocol line. a peer that never sends a newline would
// otherwise grow the read buffer until the process dies.
const MaxFrameLen = 4096

// ErrFrameTooLong is returned for a line that never ends.
var ErrFrameTooLong = errors.New("frame too long")

// NewFrameReader wraps r with a buffer sized so that ReadFrame can cap a line.
func NewFrameReader(r io.Reader) *bufio.Reader {
	return bufio.NewReaderSize(r, MaxFrameLen)
}

// ReadFrame reads one newline-terminated line. ReadSlice fails once the buffer
// fills, where ReadString would keep growing it.
func ReadFrame(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", ErrFrameTooLong
	}
	// the slice is only valid until the next read, so copy it out
	return string(line), err
}

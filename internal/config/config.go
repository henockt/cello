package config

/*
This package includes common communication
configurations between client and server
*/

// Default ports (no leading colon). Used as fallbacks when no flag or env var is set.
const (
	DefaultChannelPort = "9000"
	DefaultDataPort    = "9001"
	DefaultPublicPort  = "3001"
)

// Channel-port verbs. Every frame is a single newline-terminated line whose
// first three bytes name the verb; anything after the first ':' is payload, so
// a payload may itself contain ':' (a tunnel URL does).
const (
	ChannelRequest = "SUB" // SUB:<name>, or bare SUB / SUB: to be assigned one
	ChannelSuccess = "ACK" // ACK:<url> on the channel port; bare ACK on the data port
	ChannelReject  = "NAK" // NAK:<code>:<message>, registration refused
	ChannelEnd     = "END" // END:<reason>, server is closing an established session
	ChannelPublish = "PUB" // PUB:<RequestId>
	ChannelError   = "ERR" // ERR:<RequestId>
)

// NAK codes. The client decides whether a retry is worthwhile from the code,
// and shows the accompanying message to the user verbatim.
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

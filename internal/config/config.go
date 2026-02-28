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

const (
	ChannelRequest = "SUB" // SUB:<ChannelId>
	ChannelSuccess = "ACK"
	ChannelTaken   = "TAK"
	ChannelPublish = "PUB" // PUB:<RequestId>:<length>
	ChannelError   = "ERR" // ERR:<RequestId>
	// ChannelDataTransfer = "REQ"
)

const (
	// the maximum time the server waits for the client agent
	// to claim a public request before responding with 504.
	RequestTimeout = 30
)

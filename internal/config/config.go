package config

/* 
This package includes common communication
configurations between client and server
*/

const (
	ChannelPort = ":9000"
	DataPort = ":9001"
	PublicPort = ":3000"
)

const (
	ChannelRequest = "SUB" // SUB:<ChannelId>
	ChannelSuccess = "ACK"
	ChannelTaken = "TAK"
	ChannelPublish = "PUB" // PUB:<RequestId>:<length>
)

// // fmt.Fprintf
// func controlWrite(conn net.Conn, ) {

// }
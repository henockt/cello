# cello

A lightweight reverse proxy and tunneling system that exposes local services to the public internet. Built with Go, cello enables communication between clients and public-facing servers without requiring direct internet access.

## Overview

cello is a client-server application similar to ngrok that allows you to:
- Register your local services with a central server
- Expose local applications publicly through subdomain routing
- Enable bidirectional communication through a three-channel protocol
- Handle multiple concurrent client connections

## Architecture

cello uses a three-tier communication model:

### Communication Channels

1. **Channel Port** (`:9000`) - Client Registration
   - Clients connect and register with a unique channel ID
   - Uses the `SUB:<ChannelId>` protocol for registration
   - Server sends back `ACK` or `TAK` (taken) responses

2. **Public Port** (`:3001`) - HTTP Request Router
   - Receives incoming HTTP requests from the public internet
   - Extracts subdomain information from Host headers
   - Maps subdomains to registered client channels
   - Forwards valid requests to the appropriate client

3. **Data Port** (`:9001`) - Bidirectional Data Transfer
   - Handles actual data transfer between clients and public requesters
   - Maintains request context and manages connections
   - Supports bidirectional proxying with proper EOF signaling


## Quick Start

### Prerequisites

- Go 1.25 or later
- Make (optional, for convenience)

### Installation

```bash
git clone https://github.com/henockt/cello.git
cd cello
go mod download
```

### Building

```bash
# Build server
go build -o bin/server ./cmd/server

# Build client
go build -o bin/client ./cmd/client
```

### Running

#### Development Mode (local)

```bash
make dev
```

This starts both server and client with default settings.

#### Production Mode

**Start the server:**
```bash
./bin/server
```

**Start a client (in another terminal):**
```bash
./bin/client -name myapp -port 3000
```

### Options

**Server** - No command-line options (uses hardcoded ports)

**Client:**
- `-name` (default: `myapp`) - Unique identifier for your channel
- `-port` (default: `3000`) - Local port of the service to expose

## Usage

### Scenario: Expose a local test server

1. **Terminal 1 - Start cello server:**
   ```bash
   go run cmd/server/main.go
   ```

2. **Terminal 2 - Start the test server:**
   ```bash
   go run cmd/test/main.go
   ```
   This starts a simple HTTP server on port 3000 that responds with "hello, from local server".

3. **Terminal 3 - Start cello client:**
   ```bash
   go run cmd/client/main.go -name myapp -port 3000
   ```

4. **Access through cello:**
   - In Terminal 4, test the tunnel:
     ```bash
     curl http://localhost:3001 -H "Host: myapp.localhost"
     ```
   - You should see: `hello, from local server`
   - Your request was routed through the cello server to your local service

## Configuration

All configuration is hardcoded in [internal/config/config.go](internal/config/config.go):

- **Channel Port**: `:9000`
- **Data Port**: `:9001`
- **Public Port**: `:3001`

To customize ports, modify the constants in the config file and rebuild.

## License

This project is licensed under the MIT License. See the LICENSE file for details.

## Contact

For questions or issues, please open an issue on the GitHub repository.

<h2 align="center">
    cello
</h2>


<p align="center">
    A lightweight reverse proxy that exposes local services to the public internet.
</p>
<p align="center">
    <a href="https://github.com/henockt/cello/blob/main/LICENSE">
        <img alt="cello is released under the MIT license." src="https://img.shields.io/badge/license-MIT-blue.svg"/></a>
</p>

## How it works

cello uses three ports:

| Port | Default | Purpose |
|------|---------|---------|
| Channel | `9000` | Client registration |
| Public | `3001` | Incoming HTTP requests (subdomain-routed) |
| Data | `9001` | Bidirectional data transfer |

The server assigns each client a random channel name and replies with the full tunnel URL, e.g. `https://k7m2xq.cello.example.com`. Requests to that subdomain on the public port are forwarded to the client, which proxies them to a local service. The server returns `504` if the client does not claim a request within 30 seconds.

Run the server with `-allow-client-names` to let clients choose their own name instead.

![System Architecture](./assets/diagram.svg)

## Quick start

```bash
git clone https://github.com/henockt/cello.git
cd cello
go mod download
```

```bash
# Terminal 1 – server (-allow-client-names keeps the URL predictable for this demo)
go run cmd/server/main.go -allow-client-names

# Terminal 2 – local service to expose
go run cmd/test/main.go

# Terminal 3 – client
go run cmd/client/main.go -name myapp -port 3000
#  Tunnel live: http://myapp.localhost:3001 -> localhost:3000

# Terminal 4 – test
curl http://myapp.localhost:3001
#  hello, from local server
```

Without `-allow-client-names` the server assigns the name, and the client prints
the URL to use — open or curl it directly.

`*.localhost` resolves to loopback in browsers, and on most Linux systems for
other clients too. Where it does not, address the server directly and set the
name in the `Host` header instead:

```bash
curl http://localhost:3001 -H "Host: myapp.localhost"
```

## Options

**Server**

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `-channel-port` | `CELLO_CHANNEL_PORT` | `9000` | Client registration port |
| `-public-port` | `CELLO_PUBLIC_PORT` | `3001` | Public HTTP port |
| `-data-port` | `CELLO_DATA_PORT` | `9001` | Data transfer port |
| `-public-base` | `CELLO_PUBLIC_BASE` | `http://localhost:3001` | Base URL tunnels are published under |
| `-allow-client-names` | `CELLO_ALLOW_CLIENT_NAMES` | `false` | Let clients choose their own channel name |
| `-reserved-names` | `CELLO_RESERVED_NAMES` | `www,api,admin,mail` | Names that may never be registered |

`-public-base` is the URL visitors use; `-public-port` is the socket cello accepts
HTTP on. In development they match. Behind a TLS-terminating proxy they don't:
visitors use `https://cello.example.com` on 443 while cello listens for plain HTTP
on 3001, and it can't infer the former from the latter. The value is used both to
build the tunnel URLs handed to clients and to decide which Host headers name a
tunnel.

**Client**

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `-name` | `CELLO_CHANNEL_NAME` | — | Requested channel name; empty lets the server assign one |
| `-port` | — | `3000` | Local service port |
| `-server` | `CELLO_SERVER_HOST` | `localhost` | cello server hostname or IP |
| `-channel-port` | `CELLO_CHANNEL_PORT` | `9000` | Server channel port |
| `-data-port` | `CELLO_DATA_PORT` | `9001` | Server data port |


## License

MIT

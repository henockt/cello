# Deploying cello

cello is a plain HTTP server that expects a reverse proxy in front of it. The
proxy owns port 443 and the certificate. cello binds loopback and never handles
a certificate itself.


The proxy is the only thing the firewall needs to expose.

## Caddy

Caddy is the least work: it obtains and renews the certificate itself, and both
the rate limit and the tunnel lifetime are config rather than code.

```bash
xcaddy build --with github.com/caddy-dns/<your-provider> \
             --with github.com/mholt/caddy-ratelimit
```

```
*.cello.example.com, cello.example.com {
    tls { dns <your-provider> {env.API_TOKEN} }

    rate_limit {
        zone registrations {
            match {
                host cello.example.com
                path /_cello/channel
            }
            key         {remote_host}
            window      1m
            events      5
            ipv6_prefix 64
        }
    }

    reverse_proxy 127.0.0.1:3001 {
        stream_timeout 10m
    }
}
```

The site block must cover the apex as well as the wildcard.

`rate_limit` caps how fast one address can open tunnels. Registration is an
ordinary HTTP request before the upgrade, so the proxy sees the real client
address and can limit it. `ipv6_prefix 64` keys on the prefix, since a single
host usually gets a whole /64.

`stream_timeout` is the lifetime of a tunnel. Ten minutes suits sharing a link
for someone to look at now. Raise it for longer sessions. Set it to `0` only if you want tunnels to live
until a peer closes. note that it also cuts off a single request that runs longer
than the timeout.

## nginx

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

server {
    listen 443 ssl;
    server_name cello.example.com *.cello.example.com;

    ssl_certificate     /etc/letsencrypt/live/cello.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/cello.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:3001;

        # cello routes on Host. without this nginx sends the upstream address
        # and nothing works: clients cannot even register
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;

        # the control connections are HTTP upgrades
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;

        # a channel connection is idle between requests. the 60s default would
        # close it, and this doubles as the tunnel lifetime
        proxy_read_timeout 10m;
        proxy_send_timeout 10m;
    }
}
```

The certificate must be a wildcard covering the apex too, which means a DNS-01
challenge: `certbot certonly --dns-<provider> -d cello.example.com -d '*.cello.example.com'`.

nginx has no equivalent of Caddy's `rate_limit` for this, but `limit_req` on
`/_cello/channel` keyed by `$binary_remote_addr` gets close.
## Running cello

```bash
# on the server, a port above 1024, bound to loopback
cello-server -listen 127.0.0.1:3001 -public-base https://cello.example.com

# on your machine, 443 is the proxy, and is the default
cello-client -server cello.example.com -port 3000
```

## Without a proxy

cello speaks plain HTTP, so running it directly on a public port means tunnel
traffic and client connections are both unencrypted. For local testing that is
fine. Pick any port above 1024 and turn TLS off on the client. `-public-base`
must carry the port, since it is the only source of the URLs handed to clients:

```bash
cello-server -listen :3001 -public-base http://cello.example.com:3001
cello-client -server cello.example.com -server-port 3001 -tls=false -port 3000
```

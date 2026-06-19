# Deploying dcrmapper on a VPS

This guide walks through a production deployment on a fresh Linux VPS
(Ubuntu 24.04 LTS "Noble" or Debian 13 "Trixie"). The app runs as a plain Go
binary behind [Caddy](https://caddyserver.com/), which reverse-proxies it and
provisions TLS automatically.

**Architecture**

```
            (TLS, auto)            (HTTP, loopback)
Internet ───────────►  Caddy  ──────────────────►  dcrmapper
:443                   :443                         127.0.0.1:8111
                                                        │ outbound
                                                        ▼
                                          Decred P2P network (crawl)
                                          ip-api.com (geolocation)
```

The crawler makes many outbound TCP connections to peers on the Decred P2P port
(9108 mainnet / 19108 testnet) and HTTP requests to `ip-api.com`. It only needs
inbound `80`/`443` for the website itself.

Why Caddy: HTTPS is automatic — it obtains and renews Let's Encrypt certificates
on its own, redirects HTTP→HTTPS, and enables HTTP/2 and modern TLS defaults
with a two-line config. No certbot, no cron, no manual renewal.

---

## Quick start (automated)

The repository ships a [`deploy.sh`](deploy.sh) that performs every step in this
guide. On a fresh VPS, as root:

```sh
git clone https://github.com/jzbz/dcrmapper
sudo ./dcrmapper/deploy.sh --domain map.example.com
```

It installs Go and Caddy, builds the app, and starts it as a hardened systemd
service behind Caddy with automatic HTTPS. The script is idempotent, so **running
it again upgrades an existing install**: it force-syncs the checkout to the
latest remote commit, rebuilds, and only swaps the new binary into place if the
build succeeds — a broken build never takes down the running service. (The
force-sync discards any local edits to tracked files in the checkout.) Useful
flags:

| Flag | Effect |
| --- | --- |
| `--domain <host>` | Domain to serve; Caddy provisions a TLS certificate for it. |
| `--http` | Serve plain HTTP on `:80` (no domain needed) — handy for testing. |
| `--testnet` | Crawl testnet instead of mainnet. |
| `--go-version <v>` | Override the Go toolchain version (default `1.26.4`). |

Run `./deploy.sh --help` for the full list. The rest of this document explains
what the script does, for manual setups or customization.

---

## 1. Provision the server

A small instance is plenty: **1 vCPU / 1 GB RAM / 10 GB disk**. Point your
domain's `A`/`AAAA` DNS records at the VPS before starting (Caddy needs them
resolving to issue a certificate).

SSH in as a sudo-capable user and update the base system:

```sh
sudo apt update && sudo apt upgrade -y
sudo apt install -y git ufw curl gnupg
```

Configure a basic firewall (allow SSH + web):

```sh
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

---

## 2. Install Go

dcrmapper targets the Go version in `go.mod` (currently **1.26.x**). Distro
packages are often older, so install the official toolchain:

```sh
GO_VERSION=1.26.4
curl -sL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
export PATH=$PATH:/usr/local/go/bin
go version
```

> On `arm64` hosts swap `linux-amd64` for `linux-arm64`.

---

## 3. Install Caddy

Install from the official Cloudsmith apt repository so it stays up to date and
ships with a ready-made systemd service:

```sh
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt update
sudo apt install -y caddy
```

This installs and starts a `caddy` systemd service that reads
`/etc/caddy/Caddyfile`. We configure it in [§6](#6-reverse-proxy--automatic-https-with-caddy).

---

## 4. Create a service user

Run the app as an unprivileged, no-login system user. Its home directory is
where the crawler persists its node cache (`~/.dcrmapper/<network>/nodes.json`).

```sh
sudo useradd --system --create-home --home-dir /opt/dcrmapper --shell /usr/sbin/nologin dcrmapper
```

---

## 5. Build the application

Clone and compile into the service user's directory:

```sh
sudo git clone https://github.com/jzbz/dcrmapper /opt/dcrmapper/app
cd /opt/dcrmapper/app
sudo /usr/local/go/bin/go build -o /opt/dcrmapper/app/dcrmapper .
sudo chown -R dcrmapper:dcrmapper /opt/dcrmapper
```

The templates and static assets are **embedded into the binary** at build time
(`go:embed`), so the compiled `dcrmapper` is self-contained and can run from any
working directory. (The systemd unit below still sets `WorkingDirectory`, which
is harmless.)

The UI uses a single hand-authored stylesheet (`public/css/app.css`) — no CSS
framework and **no build step of any kind** beyond `go build`. The whole deploy
is just compiling the Go binary.

---

## 6. Run it as a systemd service

Create the unit file:

```sh
sudo tee /etc/systemd/system/dcrmapper.service >/dev/null <<'EOF'
[Unit]
Description=dcrmapper - Decred network world map
After=network-online.target
Wants=network-online.target

[Service]
User=dcrmapper
Group=dcrmapper
WorkingDirectory=/opt/dcrmapper/app
ExecStart=/opt/dcrmapper/app/dcrmapper -listen 127.0.0.1:8111 -domain map.example.com
Restart=on-failure
RestartSec=5

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
# Allow writing only the node cache under the service user's home.
ReadWritePaths=/opt/dcrmapper

[Install]
WantedBy=multi-user.target
EOF
```

Replace `map.example.com` with your domain (the `-domain` flag sets the cookie
domain used by the theme switcher). Add `-testnet` to `ExecStart` to crawl
testnet instead of mainnet.

Enable and start:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now dcrmapper
sudo systemctl status dcrmapper
journalctl -u dcrmapper -f      # watch the crawl logs
```

You should see lines like `Listening on 127.0.0.1:8111` and
`Done checking N addresses, M good`. The web server starts immediately; the map
fills in over the first few minutes as nodes are contacted and geolocated.

---

## 7. Reverse proxy + automatic HTTPS with Caddy

Replace the default Caddyfile with one that proxies your domain to the app.
Caddy obtains and renews the TLS certificate automatically the first time it
serves the site:

```sh
sudo tee /etc/caddy/Caddyfile >/dev/null <<'EOF'
map.example.com {
	encode zstd gzip
	reverse_proxy 127.0.0.1:8111

	# Cache the static assets aggressively.
	@assets path /public/*
	header @assets Cache-Control "public, max-age=604800"
}
EOF
```

> Caddyfiles use **tabs** for indentation. The heredoc above preserves them.

Replace `map.example.com` with your domain, then validate and reload:

```sh
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
journalctl -u caddy -f      # watch certificate issuance
```

Within a few seconds Caddy provisions a Let's Encrypt certificate and your site
is live at `https://map.example.com`, with HTTP automatically redirecting to
HTTPS. Renewal is handled in the background for as long as the service runs —
there is nothing else to schedule.

> **For a quick test without a domain**, replace the first line of the Caddyfile
> with `:80` (plain HTTP, no TLS) and browse to the server's IP.

---

## 8. Updating to a new version

```sh
cd /opt/dcrmapper/app
sudo -u dcrmapper git pull
sudo /usr/local/go/bin/go build -o /opt/dcrmapper/app/dcrmapper .
sudo systemctl restart dcrmapper
```

The node cache in `/opt/dcrmapper/.dcrmapper/` persists across restarts, so the
map repopulates almost instantly after an update.

---

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `502 Bad Gateway` | Is the app up? `systemctl status dcrmapper`. Is it listening on `127.0.0.1:8111`? `ss -ltnp \| grep 8111`. |
| TLS certificate not issued | DNS must resolve to this host and ports 80+443 must be open. Watch `journalctl -u caddy -f` for ACME errors. |
| Blank page / missing styles | The app must run with `WorkingDirectory=/opt/dcrmapper/app` so `templates/` and `public/` resolve. |
| Map never populates | The crawler needs **outbound** network access. Confirm the host can reach peers and `ip-api.com`; watch `journalctl -u dcrmapper`. |
| `Hit ip-api rate limit` in logs | Normal — the free ip-api tier is rate-limited; the crawler backs off and retries automatically. |
| Theme switcher doesn't persist | Make sure `-domain` matches the domain you serve from. |

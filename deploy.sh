#!/usr/bin/env bash
#
# deploy.sh — provision dcrmapper on a fresh Debian 13 / Ubuntu 24.04 LTS VPS.
#
# Installs Go, Caddy (automatic HTTPS), builds the app, and runs it as a
# hardened systemd service behind Caddy. Safe to re-run: every step is
# idempotent, so this doubles as an updater.
#
# Usage (run as root or with sudo):
#   sudo ./deploy.sh --domain map.example.com
#   sudo ./deploy.sh --domain map.example.com --testnet
#   sudo ./deploy.sh --http                 # no domain: serve plain HTTP on :80
#
# Options:
#   --domain <host>   Domain to serve (Caddy provisions a TLS cert for it).
#   --http            Serve plain HTTP on :80 instead of HTTPS (for testing).
#   --testnet         Crawl testnet instead of mainnet.
#   --go-version <v>  Go toolchain version to install   (default: 1.26.4).
#   --repo <url>      Git repository to deploy.
#   --listen <addr>   Internal listen address           (default: 127.0.0.1:8111).
#   -h, --help        Show this help.
#
set -euo pipefail

# ---- Configuration --------------------------------------------------------

GO_VERSION="1.26.4"
REPO_URL="https://github.com/jholdstock/dcrmapper"
SERVICE_USER="dcrmapper"
APP_HOME="/opt/dcrmapper"
APP_DIR="${APP_HOME}/app"
LISTEN="127.0.0.1:8111"
DOMAIN=""
HTTP_ONLY=0
TESTNET=0

# ---- Logging --------------------------------------------------------------

if [[ -t 1 ]]; then
  C_BLUE=$'\e[34m'; C_GREEN=$'\e[32m'; C_RED=$'\e[31m'; C_DIM=$'\e[2m'; C_OFF=$'\e[0m'
else
  C_BLUE=""; C_GREEN=""; C_RED=""; C_DIM=""; C_OFF=""
fi
log()  { printf '%s==>%s %s\n' "$C_BLUE" "$C_OFF" "$*"; }
ok()   { printf '%s  ✓%s %s\n' "$C_GREEN" "$C_OFF" "$*"; }
warn() { printf '%s  !%s %s\n' "$C_RED" "$C_OFF" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$C_RED" "$C_OFF" "$*" >&2; exit 1; }

# Print the header comment block (everything after the shebang up to the first
# non-comment line) as usage text.
usage() {
  local line
  while IFS= read -r line; do
    case "$line" in
      '#!'*) continue ;;
      '# '*) printf '%s\n' "${line#'# '}" ;;
      '#')   printf '\n' ;;
      '#'*)  printf '%s\n' "${line#'#'}" ;;
      *)     break ;;
    esac
  done < "$0"
  exit "${1:-0}"
}

# ---- Argument parsing -----------------------------------------------------

while [[ $# -gt 0 ]]; do
  case "$1" in
    --domain)     DOMAIN="${2:?--domain needs a value}"; shift 2 ;;
    --http)       HTTP_ONLY=1; shift ;;
    --testnet)    TESTNET=1; shift ;;
    --go-version) GO_VERSION="${2:?--go-version needs a value}"; shift 2 ;;
    --repo)       REPO_URL="${2:?--repo needs a value}"; shift 2 ;;
    --listen)     LISTEN="${2:?--listen needs a value}"; shift 2 ;;
    -h|--help)    usage 0 ;;
    *)            warn "unknown option: $1"; usage 1 ;;
  esac
done

[[ $EUID -eq 0 ]] || die "must run as root (try: sudo $0 ...)"
if [[ $HTTP_ONLY -eq 0 && -z "$DOMAIN" ]]; then
  die "provide --domain <host>, or --http to serve plain HTTP for testing"
fi

# Cookie domain for the theme switcher; harmless when serving plain HTTP.
COOKIE_DOMAIN="${DOMAIN:-localhost}"
NETWORK="mainnet"; [[ $TESTNET -eq 1 ]] && NETWORK="testnet"

# ---- 1. Base packages -----------------------------------------------------

log "Installing base packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq git ufw curl gnupg ca-certificates >/dev/null
ok "git, ufw, curl, gnupg installed"

# ---- 2. Firewall ----------------------------------------------------------

log "Configuring firewall (ufw)"
ufw allow OpenSSH >/dev/null 2>&1 || true
ufw allow 80/tcp  >/dev/null
ufw allow 443/tcp >/dev/null
ufw --force enable >/dev/null
ok "SSH, 80/tcp and 443/tcp allowed"

# ---- 3. Go toolchain ------------------------------------------------------

case "$(uname -m)" in
  x86_64|amd64)  GO_ARCH="amd64" ;;
  aarch64|arm64) GO_ARCH="arm64" ;;
  *)             die "unsupported CPU architecture: $(uname -m)" ;;
esac

if /usr/local/go/bin/go version 2>/dev/null | grep -q "go${GO_VERSION} "; then
  ok "Go ${GO_VERSION} already installed"
else
  log "Installing Go ${GO_VERSION} (${GO_ARCH})"
  tmp="$(mktemp -d)"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -o "${tmp}/go.tar.gz"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "${tmp}/go.tar.gz"
  rm -rf "$tmp"
  # $PATH must stay literal so it expands at login, not now.
  # shellcheck disable=SC2016
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  ok "Go installed: $(/usr/local/go/bin/go version)"
fi
GO=/usr/local/go/bin/go

# ---- 4. Caddy -------------------------------------------------------------

if command -v caddy >/dev/null 2>&1; then
  ok "Caddy already installed ($(caddy version | head -1))"
else
  log "Installing Caddy from the official apt repository"
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
    | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
    > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update -qq
  apt-get install -y -qq caddy >/dev/null
  ok "Caddy installed ($(caddy version | head -1))"
fi

# ---- 5. Service user ------------------------------------------------------

if id "$SERVICE_USER" >/dev/null 2>&1; then
  ok "Service user '${SERVICE_USER}' exists"
else
  log "Creating service user '${SERVICE_USER}'"
  useradd --system --create-home --home-dir "$APP_HOME" \
    --shell /usr/sbin/nologin "$SERVICE_USER"
  ok "Created ${SERVICE_USER} (home: ${APP_HOME})"
fi

# ---- 6. Fetch & build -----------------------------------------------------

if [[ -d "${APP_DIR}/.git" ]]; then
  ACTION="upgrade"
  log "Updating existing checkout (force-syncing to remote; local edits to tracked files are discarded)"
  sudo -u "$SERVICE_USER" git -C "$APP_DIR" fetch --depth 1 origin
  sudo -u "$SERVICE_USER" git -C "$APP_DIR" reset --hard '@{u}'
else
  ACTION="install"
  log "Cloning ${REPO_URL}"
  git clone --depth 1 "$REPO_URL" "$APP_DIR"
  chown -R "${SERVICE_USER}:${SERVICE_USER}" "$APP_HOME"
fi

# Build into a temporary file next to the live binary. It is swapped into place
# atomically in the next step, *after* a successful build — so a broken build
# can never take down a running service, and the swap has no partial-file window
# (rename(2) on the same filesystem is atomic).
log "Building dcrmapper"
( cd "$APP_DIR" && GOTOOLCHAIN=local "$GO" build -o "${APP_DIR}/dcrmapper.new" . )
chown "${SERVICE_USER}:${SERVICE_USER}" "${APP_DIR}/dcrmapper.new"
ok "Build succeeded"

# ---- 7. systemd service ---------------------------------------------------

log "Writing systemd unit"
EXEC="${APP_DIR}/dcrmapper -listen ${LISTEN} -domain ${COOKIE_DOMAIN}"
[[ $TESTNET -eq 1 ]] && EXEC="${EXEC} -testnet"

cat > /etc/systemd/system/dcrmapper.service <<EOF
[Unit]
Description=dcrmapper - Decred network world map
After=network-online.target
Wants=network-online.target

[Service]
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=${APP_DIR}
ExecStart=${EXEC}
Restart=on-failure
RestartSec=5

# Hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${APP_HOME}

[Install]
WantedBy=multi-user.target
EOF

# Atomically swap in the new binary, then (re)start. mv is a rename(2) within the
# same directory, so it replaces the file even while the old binary is executing;
# the running process keeps the old inode until the restart execs the new one.
mv -f "${APP_DIR}/dcrmapper.new" "${APP_DIR}/dcrmapper"

systemctl daemon-reload
systemctl enable dcrmapper >/dev/null 2>&1 || true
systemctl restart dcrmapper
ok "dcrmapper service running (network: ${NETWORK})"

# ---- 8. Caddy reverse proxy ----------------------------------------------

log "Writing Caddyfile"
if [[ $HTTP_ONLY -eq 1 ]]; then
  SITE=":80"
else
  SITE="$DOMAIN"
fi

# Tabs are required for Caddyfile block indentation.
cat > /etc/caddy/Caddyfile <<EOF
${SITE} {
	encode zstd gzip
	reverse_proxy ${LISTEN}

	@assets path /public/*
	header @assets Cache-Control "public, max-age=604800"
}
EOF

caddy validate --config /etc/caddy/Caddyfile >/dev/null
systemctl reload caddy
ok "Caddy configured for ${SITE}"

# ---- Done -----------------------------------------------------------------

echo
if [[ "$ACTION" == "upgrade" ]]; then
  ok "Upgrade complete — dcrmapper rebuilt and restarted."
else
  ok "Deployment complete."
fi
if [[ $HTTP_ONLY -eq 1 ]]; then
  IP="$(curl -fsSL https://api.ipify.org 2>/dev/null || echo '<server-ip>')"
  echo "   ${C_DIM}Site:${C_OFF}  http://${IP}/"
else
  echo "   ${C_DIM}Site:${C_OFF}  https://${DOMAIN}/   ${C_DIM}(TLS provisions on first request)${C_OFF}"
fi
cat <<EOF
   ${C_DIM}Logs:${C_OFF}  journalctl -u dcrmapper -f
   ${C_DIM}Caddy:${C_OFF} journalctl -u caddy -f

The map fills in over the first few minutes as nodes are crawled and geolocated.
EOF

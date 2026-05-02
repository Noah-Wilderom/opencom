#!/usr/bin/env bash
# setup-node.sh — provision a hardened Ubuntu server as an opencom relay /
# bootstrap node. Idempotent (safe to re-run). Non-interactive (designed for
# cloud-init user_data; also runnable manually as root with env vars set).
#
# Required env vars:
#   ADMIN_SSH_KEY        SSH public key (single line, "ssh-ed25519 AAAA... user@host")
#                        installed for the admin user; root login is disabled.
#
# Optional env vars (with defaults):
#   ADMIN_USER           "admin"      Linux username for the sudo account.
#   OPENCOM_USER         "opencom"    System user the daemon runs as.
#   OPENCOM_LISTEN_PORT  "4001"       TCP+UDP port libp2p binds to.
#   OPENCOM_REPO         "Noah-Wilderom/opencom"  GitHub owner/repo for binary.
#   OPENCOM_VERSION      "latest"     Release tag, or "latest".
#   OPENCOM_BINARY_URL   ""           Direct tarball URL (overrides version+repo).
#   SSH_PORT             "22"         SSH listen port (firewall + sshd).
#
# Output: progress to /var/log/opencom-setup.log (and stdout via tee).

set -euo pipefail

# --- Logging -----------------------------------------------------------------
mkdir -p /var/log
exec > >(tee -a /var/log/opencom-setup.log) 2>&1

echo "===> opencom setup starting at $(date -u +%FT%TZ)"

# --- Defaults ----------------------------------------------------------------
ADMIN_USER="${ADMIN_USER:-admin}"
OPENCOM_USER="${OPENCOM_USER:-opencom}"
OPENCOM_LISTEN_PORT="${OPENCOM_LISTEN_PORT:-4001}"
OPENCOM_REPO="${OPENCOM_REPO:-Noah-Wilderom/opencom}"
OPENCOM_VERSION="${OPENCOM_VERSION:-latest}"
OPENCOM_BINARY_URL="${OPENCOM_BINARY_URL:-}"
SSH_PORT="${SSH_PORT:-22}"

if [[ -z "${ADMIN_SSH_KEY:-}" ]]; then
  echo "FATAL: ADMIN_SSH_KEY env var must be set" >&2
  exit 2
fi

# --- Wait for network --------------------------------------------------------
# Cloud-init normally runs after networking, but be defensive on slow boots.
for _ in $(seq 1 30); do
  if getent hosts deb.debian.org github.com api.github.com >/dev/null 2>&1; then
    break
  fi
  echo "waiting for DNS..."
  sleep 2
done

# --- System update + base packages ------------------------------------------
export DEBIAN_FRONTEND=noninteractive
# Ubuntu 24.04+: needrestart pops up an interactive prompt during apt
# upgrade and, worse, can decide to restart cloud-final.service — which is
# the very service running this script — killing it mid-flight. Disable
# both behaviors before any apt operation.
export NEEDRESTART_MODE=a
export NEEDRESTART_SUSPEND=1
mkdir -p /etc/needrestart/conf.d
cat > /etc/needrestart/conf.d/99-opencom.conf <<'EOF'
# Disable interactive prompts and automatic service restarts during
# unattended provisioning (managed by opencom setup-node.sh).
$nrconf{restart} = 'a';
$nrconf{kernelhints} = 0;
$nrconf{ucodehints} = 0;
EOF

apt-get update -q
apt-get -y -q -o Dpkg::Options::="--force-confdef" -o Dpkg::Options::="--force-confold" upgrade

apt-get install -y -q \
  ca-certificates curl gnupg jq \
  ufw fail2ban \
  unattended-upgrades \
  systemd-timesyncd \
  libopus0 libopusfile0

systemctl enable --now systemd-timesyncd

# --- Admin user --------------------------------------------------------------
if ! id -u "$ADMIN_USER" >/dev/null 2>&1; then
  useradd -m -s /bin/bash -G sudo "$ADMIN_USER"
fi
install -d -m 0700 -o "$ADMIN_USER" -g "$ADMIN_USER" "/home/$ADMIN_USER/.ssh"
echo "$ADMIN_SSH_KEY" > "/home/$ADMIN_USER/.ssh/authorized_keys"
chown "$ADMIN_USER:$ADMIN_USER" "/home/$ADMIN_USER/.ssh/authorized_keys"
chmod 0600 "/home/$ADMIN_USER/.ssh/authorized_keys"

# Passwordless sudo for the admin user (key-only login already enforces auth).
cat > "/etc/sudoers.d/90-${ADMIN_USER}" <<EOF
${ADMIN_USER} ALL=(ALL) NOPASSWD: ALL
EOF
chmod 0440 "/etc/sudoers.d/90-${ADMIN_USER}"
visudo -c -q

# --- SSH hardening -----------------------------------------------------------
mkdir -p /etc/ssh/sshd_config.d
cat > /etc/ssh/sshd_config.d/99-opencom.conf <<EOF
# Managed by opencom setup-node.sh — do not edit by hand.
Port ${SSH_PORT}
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PubkeyAuthentication yes
PermitEmptyPasswords no
UsePAM yes
X11Forwarding no
AllowTcpForwarding no
ClientAliveInterval 300
ClientAliveCountMax 2
LoginGraceTime 30
MaxAuthTries 3
EOF
sshd -t  # validate config
systemctl reload ssh || systemctl reload sshd

# --- Firewall (UFW) ----------------------------------------------------------
ufw --force reset >/dev/null
ufw default deny incoming
ufw default allow outgoing
ufw allow "${SSH_PORT}/tcp" comment 'SSH'
ufw allow "${OPENCOM_LISTEN_PORT}/tcp" comment 'opencom libp2p TCP'
ufw allow "${OPENCOM_LISTEN_PORT}/udp" comment 'opencom libp2p QUIC'
ufw --force enable

# --- fail2ban (SSH brute-force protection) -----------------------------------
cat > /etc/fail2ban/jail.d/sshd.local <<EOF
[sshd]
enabled  = true
port     = ${SSH_PORT}
filter   = sshd
backend  = systemd
maxretry = 5
findtime = 10m
bantime  = 1h
EOF
systemctl enable --now fail2ban
systemctl restart fail2ban

# --- Unattended security upgrades -------------------------------------------
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
# Ubuntu's default 50unattended-upgrades enables -security; leave intact.
systemctl enable --now unattended-upgrades

# --- opencom system user + dirs ----------------------------------------------
if ! id -u "$OPENCOM_USER" >/dev/null 2>&1; then
  useradd --system --home /var/lib/opencom --shell /usr/sbin/nologin "$OPENCOM_USER"
fi
install -d -m 0700 -o "$OPENCOM_USER" -g "$OPENCOM_USER" \
  /var/lib/opencom \
  /var/lib/opencom/config \
  /var/lib/opencom/config/opencom \
  /var/lib/opencom/state \
  /var/lib/opencom/state/opencom \
  /var/lib/opencom/runtime \
  /var/log/opencom

# --- opencom binary install --------------------------------------------------
DPKG_ARCH="$(dpkg --print-architecture)"
case "$DPKG_ARCH" in
  amd64) ASSET_ARCH="x86_64" ;;
  arm64) ASSET_ARCH="arm64" ;;
  *) echo "FATAL: unsupported arch '$DPKG_ARCH'" >&2; exit 3 ;;
esac

resolve_binary_url() {
  if [[ -n "$OPENCOM_BINARY_URL" ]]; then
    echo "$OPENCOM_BINARY_URL"
    return
  fi
  local api_url="https://api.github.com/repos/${OPENCOM_REPO}/releases"
  if [[ "$OPENCOM_VERSION" == "latest" ]]; then
    api_url="${api_url}/latest"
  else
    api_url="${api_url}/tags/${OPENCOM_VERSION}"
  fi
  curl -fsSL --retry 5 --retry-delay 5 "$api_url" \
    | jq -r --arg arch "$ASSET_ARCH" '
        .assets[]
        | select(.name | endswith("Linux_" + $arch + ".tar.gz"))
        | .browser_download_url' \
    | head -n1
}

# Only re-download if the version changed or binary missing.
NEEDED_VERSION="$OPENCOM_VERSION"
INSTALLED_VERSION="$(/usr/local/bin/opencom version 2>/dev/null | awk '{print $2}' || echo none)"
if [[ "$INSTALLED_VERSION" != "$NEEDED_VERSION" || "$NEEDED_VERSION" == "latest" ]]; then
  URL="$(resolve_binary_url)"
  if [[ -z "$URL" ]]; then
    echo "FATAL: could not resolve opencom download URL (repo='$OPENCOM_REPO' ver='$OPENCOM_VERSION' arch='$ASSET_ARCH')" >&2
    exit 4
  fi
  echo "===> downloading $URL"
  TMPDIR="$(mktemp -d)"
  trap 'rm -rf "$TMPDIR"' EXIT
  curl -fsSL --retry 5 --retry-delay 5 "$URL" -o "$TMPDIR/opencom.tar.gz"
  # --strip-components=1 drops the opencom_<version>_<OS>_<arch>/ prefix
  # the matrix release workflow puts inside the archive.
  tar -xzf "$TMPDIR/opencom.tar.gz" -C "$TMPDIR" --strip-components=1
  install -m 0755 "$TMPDIR/opencom" /usr/local/bin/opencom
fi
/usr/local/bin/opencom version

# --- Initialize opencom identity (first run only) ----------------------------
# The daemon refuses to start without an identity; init.go creates it. We run
# init, then immediately stop the auto-spawned daemon — systemd will manage
# the long-running daemon below.
if [[ ! -f /var/lib/opencom/config/opencom/priv.key ]]; then
  HOSTNAME_SHORT="$(hostname -s)"
  echo "===> running 'opencom init' as $OPENCOM_USER"
  sudo -u "$OPENCOM_USER" -H \
    env HOME=/var/lib/opencom \
        XDG_CONFIG_HOME=/var/lib/opencom/config \
        XDG_STATE_HOME=/var/lib/opencom/state \
        XDG_RUNTIME_DIR=/var/lib/opencom/runtime \
    /usr/local/bin/opencom init "relay-${HOSTNAME_SHORT}" || true

  # Stop the daemon init auto-spawned (we want systemd to own it).
  sudo -u "$OPENCOM_USER" -H \
    env XDG_CONFIG_HOME=/var/lib/opencom/config \
        XDG_RUNTIME_DIR=/var/lib/opencom/runtime \
    /usr/local/bin/opencom daemon stop >/dev/null 2>&1 || true

  # Wait briefly for socket teardown.
  for _ in $(seq 1 10); do
    [[ -S /var/lib/opencom/runtime/opencom.sock ]] || break
    sleep 1
  done
  rm -f /var/lib/opencom/runtime/opencom.sock
fi

# --- Relay-tuned config (overwrites init's defaults) ------------------------
HOSTNAME_SHORT="$(hostname -s)"
cat > /var/lib/opencom/config/opencom/config.yaml <<EOF
# Managed by opencom setup-node.sh.
user:
  name: relay-${HOSTNAME_SHORT}
audio:
  input_device: auto
  output_device: auto
  input_gain: 100
  output_gain: 100
  bitrate: 32000
video:
  device: auto
  resolution: 640x480
  framerate: 30
  bitrate: 500000
  enable_on_call_start: true
network:
  listen_port: ${OPENCOM_LISTEN_PORT}
  bitrate_cap: 0
  force_reachability: public  # this node has a real public IP
discovery:
  mdns: false       # public server — mDNS is pointless and noisy here
  dht: true
  ttl: 10m0s
  dht_mode: server  # bootstrap node: must be in server mode so it
                    # responds to FIND_NODE queries from clients
  bootstraps: []
relay:
  enabled: true
  peers: []         # this node IS a relay; doesn't need to reserve through others
  unlimited: true   # remove libp2p's 128 KiB / 2 min per-circuit cap; this
                    # is a dedicated public relay we operate, and audio
                    # cross-network needs sustained bandwidth through it
ui:
  theme: auto
  notification_sound: false
  ringtone: ""
  notifications: false  # headless server — no display server, no DBus
daemon:
  autostart: false
  log_level: info
EOF
chown "$OPENCOM_USER:$OPENCOM_USER" /var/lib/opencom/config/opencom/config.yaml
chmod 0600 /var/lib/opencom/config/opencom/config.yaml

# --- systemd unit ------------------------------------------------------------
cat > /etc/systemd/system/opencom.service <<EOF
[Unit]
Description=opencom P2P daemon (relay/bootstrap node)
Documentation=https://github.com/${OPENCOM_REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${OPENCOM_USER}
Group=${OPENCOM_USER}
Environment=HOME=/var/lib/opencom
Environment=XDG_CONFIG_HOME=/var/lib/opencom/config
Environment=XDG_STATE_HOME=/var/lib/opencom/state
Environment=XDG_RUNTIME_DIR=/var/lib/opencom/runtime
ExecStart=/usr/local/bin/opencom daemon start --foreground
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

# Hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/opencom /var/log/opencom
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
LockPersonality=true
MemoryDenyWriteExecute=false  # Go runtime needs RWX
RestrictNamespaces=true
RestrictRealtime=true
RestrictSUIDSGID=true
SystemCallArchitectures=native

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now opencom.service

# --- Health check ------------------------------------------------------------
echo "===> waiting for opencom to come up..."
for _ in $(seq 1 15); do
  if systemctl is-active --quiet opencom; then break; fi
  sleep 2
done

if ! systemctl is-active --quiet opencom; then
  echo "ERROR: opencom service failed to start" >&2
  journalctl -u opencom --no-pager -n 80 >&2
  exit 5
fi

# --- Done --------------------------------------------------------------------
echo
echo "===> opencom setup complete at $(date -u +%FT%TZ)"
echo "     Service status: $(systemctl is-active opencom)"
echo "     Listen port:    ${OPENCOM_LISTEN_PORT} (TCP + QUIC, IPv4 + IPv6)"
echo
echo "Retrieve peer ID with:"
echo "  sudo -u ${OPENCOM_USER} env XDG_CONFIG_HOME=/var/lib/opencom/config \\"
echo "    XDG_RUNTIME_DIR=/var/lib/opencom/runtime \\"
echo "    /usr/local/bin/opencom daemon status"

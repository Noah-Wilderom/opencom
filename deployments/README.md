# Deploy an opencom relay/bootstrap node

Provisions a hardened Ubuntu 24.04 server on Hetzner Cloud, installs opencom
as a systemd service in relay/bootstrap mode, and registers a Cloudflare
A+AAAA record pointing at it. Designed for unattended setup — `terraform apply`
is the only command needed.

## What you get

- One Hetzner VPS (default: cx22, ~€3.79/month, 2 vCPU / 4 GB RAM)
- Hardened SSH (key-only, no root login, fail2ban)
- UFW + Hetzner Cloud firewall, only ports `22` (SSH) and `4001` (libp2p) open
- Unattended security upgrades
- opencom running as a non-root systemd service with stable peer ID
- Public DNS: `opencom.<your-zone>` → server's IPv4 + IPv6
- A multiaddr ready to drop into client `config.yaml` → `relay.peers`

## Prerequisites

- Hetzner Cloud project + API token
- Cloudflare account, with the target DNS zone already added there
- Cloudflare API token scoped to that zone (Zone:Read + Zone:DNS:Edit)
- Local OpenSSH keypair (or any SSH public key string)
- Terraform `>= 1.5`
- An opencom release published to GitHub Releases (or a direct binary URL)

## One-time setup

```bash
cd deployments/terraform

cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars: tokens, zone, ssh public key.

terraform init
terraform plan        # review
terraform apply       # type "yes"
```

## After apply (~2-3 min for cloud-init to finish)

```bash
terraform output ssh_command            # how to SSH in
terraform output fetch_peer_id_command  # how to get the peer ID
```

Run the `fetch_peer_id_command` once cloud-init has finished — it prints the
daemon's peer ID. Combine that with the `fqdn` and `opencom_listen_port` to
get the multiaddr to share with clients:

```
/dns4/opencom.example.com/tcp/4001/p2p/12D3KooW...
/dns4/opencom.example.com/udp/4001/quic-v1/p2p/12D3KooW...
```

Add those to clients' `config.yaml` under `relay.peers` (and optionally
`discovery.bootstraps` for short-code DHT lookup).

## Re-running

`setup-node.sh` is idempotent — if you change config and re-run cloud-init,
nothing breaks. Hetzner doesn't auto re-run cloud-init though, so
`terraform taint hcloud_server.opencom && terraform apply` is the only way
to force a fresh provision. State (identity, friends, invites) on the
server is destroyed when the VM is destroyed.

## What `setup-node.sh` does

1. Waits for network + DNS
2. `apt update` + `unattended-upgrades` enabled
3. Installs `ufw`, `fail2ban`, `jq`, `systemd-timesyncd`
4. Creates the admin user with sudo + key-only SSH
5. Hardens `sshd_config` (no root, no passwords, sane timeouts)
6. UFW: deny all incoming except SSH + opencom listen port
7. fail2ban: SSH brute-force protection, 1h ban after 5 failures in 10 min
8. Creates the `opencom` system user (no shell, /var/lib/opencom home)
9. Downloads the opencom binary from GitHub Releases (or `OPENCOM_BINARY_URL`)
10. Runs `opencom init` once to generate the identity, then stops the
    auto-spawned daemon
11. Writes a relay-tuned `config.yaml` (mDNS off, listen port pinned)
12. Installs and starts the `opencom.service` systemd unit
13. Verifies the service came up; exits non-zero on failure

The script is safe to run manually for testing — it just needs the
`ADMIN_SSH_KEY` env var set:

```bash
sudo ADMIN_SSH_KEY="$(cat ~/.ssh/id_ed25519.pub)" \
     OPENCOM_BINARY_URL="https://example.com/opencom_Linux_x86_64.tar.gz" \
     bash deployments/scripts/setup-node.sh
```

## Costs

- Hetzner cx22: ~€3.79/month + €1/month for the IPv4 address
- Cloudflare: free tier covers DNS
- ~€5/month all-in

## Tearing down

```bash
terraform destroy
```

Removes the server, firewall, SSH key, and DNS records.

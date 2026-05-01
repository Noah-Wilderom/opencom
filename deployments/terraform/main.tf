# Upload the admin SSH key to Hetzner so the server boots with it
# pre-installed for the `root` account (cloud-init then provisions
# the unprivileged admin user and disables root login).
resource "hcloud_ssh_key" "admin" {
  name       = "${var.server_name}-admin"
  public_key = var.admin_ssh_public_key
}

# The opencom relay/bootstrap server. Cloud-init runs setup-node.sh on
# first boot, which:
#   - hardens SSH and the firewall
#   - creates an unprivileged admin user
#   - installs the opencom binary
#   - starts opencom as a systemd service in relay mode
resource "hcloud_server" "opencom" {
  name        = var.server_name
  image       = "ubuntu-24.04"
  server_type = var.server_type
  location    = var.server_location
  ssh_keys    = [hcloud_ssh_key.admin.id]

  public_net {
    ipv4_enabled = true
    ipv6_enabled = true
  }

  # Cloud-init user_data: prepend the env vars setup-node.sh expects,
  # then inline the script. This way the script stays a normal bash
  # script (runnable manually for testing) while Terraform injects
  # the per-deployment values.
  user_data = <<-EOT
    #!/usr/bin/env bash
    set -euo pipefail
    export ADMIN_USER='${var.admin_user}'
    export ADMIN_SSH_KEY='${var.admin_ssh_public_key}'
    export OPENCOM_USER='${var.opencom_user}'
    export OPENCOM_LISTEN_PORT='${var.opencom_listen_port}'
    export OPENCOM_REPO='${var.opencom_repo}'
    export OPENCOM_VERSION='${var.opencom_version}'
    export OPENCOM_BINARY_URL='${var.opencom_binary_url}'
    export SSH_PORT='${var.ssh_port}'
    ${file("${path.module}/../scripts/setup-node.sh")}
  EOT

  labels = {
    role       = "opencom-relay"
    managed-by = "terraform"
  }

  # Recreate the server if user_data changes — Hetzner doesn't re-run
  # cloud-init on existing instances, and we want config drift to
  # actually apply. Comment out if you'd rather migrate state manually.
  lifecycle {
    create_before_destroy = false
  }
}

# Optional: a static firewall at the Hetzner Cloud edge (in addition to
# UFW on the host). Belt-and-suspenders. Lets the host see all good
# traffic but blocks scans before they reach the VM.
resource "hcloud_firewall" "opencom" {
  name = "${var.server_name}-fw"

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = tostring(var.ssh_port)
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "tcp"
    port       = tostring(var.opencom_listen_port)
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "udp"
    port       = tostring(var.opencom_listen_port)
    source_ips = ["0.0.0.0/0", "::/0"]
  }

  rule {
    direction  = "in"
    protocol   = "icmp"
    source_ips = ["0.0.0.0/0", "::/0"]
  }
}

resource "hcloud_firewall_attachment" "opencom" {
  firewall_id = hcloud_firewall.opencom.id
  server_ids  = [hcloud_server.opencom.id]
}

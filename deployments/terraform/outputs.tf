output "server_ipv4" {
  description = "Public IPv4 of the relay server."
  value       = hcloud_server.opencom.ipv4_address
}

output "server_ipv6" {
  description = "Public IPv6 of the relay server."
  value       = hcloud_server.opencom.ipv6_address
}

output "fqdn" {
  description = "Public FQDN — set as a Cloudflare A+AAAA record."
  value       = "${var.dns_record_name}.${var.cloudflare_zone}"
}

output "ssh_command" {
  description = "Convenience SSH command using the FQDN."
  value       = "ssh -p ${var.ssh_port} ${var.admin_user}@${var.dns_record_name}.${var.cloudflare_zone}"
}

# Run this command ~2-3 minutes after `terraform apply` finishes (cloud-init
# needs time to install + start the daemon). Output includes peer_id —
# combine with the FQDN below to construct the multiaddr.
output "fetch_peer_id_command" {
  description = "Command to retrieve the daemon's peer ID once cloud-init completes."
  value = join(" ", [
    "ssh -p ${var.ssh_port} ${var.admin_user}@${var.dns_record_name}.${var.cloudflare_zone}",
    "'sudo -u ${var.opencom_user}",
    "env XDG_CONFIG_HOME=/var/lib/opencom/config",
    "    XDG_RUNTIME_DIR=/var/lib/opencom/runtime",
    "    XDG_STATE_HOME=/var/lib/opencom/state",
    "/usr/local/bin/opencom daemon status'",
  ])
}

# After fetching the peer_id, drop it into this template to get the
# multiaddrs to put into config.yaml's relay.peers list on every client.
output "multiaddr_template" {
  description = "Replace <PEER_ID> with the peer ID from fetch_peer_id_command output."
  value = join("\n", [
    "/dns4/${var.dns_record_name}.${var.cloudflare_zone}/tcp/${var.opencom_listen_port}/p2p/<PEER_ID>",
    "/dns4/${var.dns_record_name}.${var.cloudflare_zone}/udp/${var.opencom_listen_port}/quic-v1/p2p/<PEER_ID>",
  ])
}

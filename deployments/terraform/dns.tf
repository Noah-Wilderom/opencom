# Look up the Cloudflare zone ID by name (e.g. "wilderom.dev"). The
# token must have Zone:Read on this zone.
data "cloudflare_zones" "primary" {
  filter {
    name = var.cloudflare_zone
  }
}

locals {
  zone_id = data.cloudflare_zones.primary.zones[0].id
}

# A record: opencom.<zone> → server's public IPv4. proxied=false because
# libp2p connections are not HTTP and Cloudflare's reverse-proxy would
# break TCP+QUIC traffic on a non-standard port.
resource "cloudflare_record" "opencom_a" {
  zone_id = local.zone_id
  name    = var.dns_record_name
  type    = "A"
  content = hcloud_server.opencom.ipv4_address
  ttl     = 120
  proxied = false
  comment = "opencom relay/bootstrap node — managed by Terraform"
}

# AAAA record for the same host. Hetzner assigns a /64 IPv6; the
# `ipv6_address` attribute is the single host address Linux configures
# on the interface (typically the network address with ::1 host part).
resource "cloudflare_record" "opencom_aaaa" {
  zone_id = local.zone_id
  name    = var.dns_record_name
  type    = "AAAA"
  content = hcloud_server.opencom.ipv6_address
  ttl     = 120
  proxied = false
  comment = "opencom relay/bootstrap node — managed by Terraform"
}

terraform {
  required_version = ">= 1.5"

  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.50"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 4.40"
    }
  }
}

# Hetzner Cloud — provisions the Ubuntu server. Token is created at
# https://console.hetzner.cloud/projects → API tokens. Needs read+write.
provider "hcloud" {
  token = var.hcloud_token
}

# Cloudflare — manages the DNS A/AAAA records for the relay node. Token
# created at https://dash.cloudflare.com/profile/api-tokens with the
# permission "Zone:DNS:Edit" scoped to the target zone only.
provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

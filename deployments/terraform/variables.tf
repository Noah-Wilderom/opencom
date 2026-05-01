# --- Provider credentials ----------------------------------------------------

variable "hcloud_token" {
  description = "Hetzner Cloud API token (read+write). Create at https://console.hetzner.cloud/projects → API tokens."
  type        = string
  sensitive   = true
}

variable "cloudflare_api_token" {
  description = "Cloudflare API token. Needs Zone:Read + Zone:DNS:Edit scoped to the target zone."
  type        = string
  sensitive   = true
}

# --- DNS ---------------------------------------------------------------------

variable "cloudflare_zone" {
  description = "Cloudflare zone name (e.g. \"example.com\"). Must already be registered in Cloudflare."
  type        = string
}

variable "dns_record_name" {
  description = "Subdomain (without zone). Full FQDN will be <name>.<zone>."
  type        = string
  default     = "opencom"
}

# --- Server ------------------------------------------------------------------

variable "server_name" {
  description = "Hetzner server name + Terraform label."
  type        = string
  default     = "opencom-relay"
}

variable "server_type" {
  description = "Hetzner instance type. cx22=2vCPU/4GB amd64 (~€3.79/mo); cax11=2vCPU/4GB arm64 (~€3.29/mo)."
  type        = string
  default     = "cx22"
}

variable "server_location" {
  description = "Hetzner location code: fsn1=Falkenstein, hel1=Helsinki, nbg1=Nuremberg, ash=Ashburn (US-east), hil=Hillsboro (US-west), sin=Singapore."
  type        = string
  default     = "fsn1"
}

variable "admin_user" {
  description = "Linux username for the sudo-enabled admin account."
  type        = string
  default     = "admin"
}

variable "admin_ssh_public_key" {
  description = "SSH public key (single line) for the admin user. Read e.g. with file(\"~/.ssh/id_ed25519.pub\")."
  type        = string

  validation {
    condition     = can(regex("^(ssh-(ed25519|rsa|ecdsa)|ecdsa-sha2|sk-)", var.admin_ssh_public_key))
    error_message = "admin_ssh_public_key must be an OpenSSH-format public key (starts with ssh-ed25519, ssh-rsa, etc.)."
  }
}

variable "ssh_port" {
  description = "SSH port (configured in sshd + UFW + Hetzner firewall)."
  type        = number
  default     = 22
}

# --- opencom -----------------------------------------------------------------

variable "opencom_user" {
  description = "System user the opencom daemon runs as."
  type        = string
  default     = "opencom"
}

variable "opencom_repo" {
  description = "GitHub owner/repo for the release-asset download."
  type        = string
  default     = "Noah-Wilderom/opencom"
}

variable "opencom_version" {
  description = "Release tag (e.g. \"v0.1.0\") or \"latest\". Ignored if opencom_binary_url is set."
  type        = string
  default     = "latest"
}

variable "opencom_binary_url" {
  description = "Direct URL to a Linux opencom tarball (e.g. a GitHub Actions artifact). Overrides opencom_repo + opencom_version when non-empty."
  type        = string
  default     = ""
}

variable "opencom_listen_port" {
  description = "TCP+UDP port for libp2p (TCP for non-QUIC peers, UDP for QUIC). Must match config.yaml network.listen_port."
  type        = number
  default     = 4001
}

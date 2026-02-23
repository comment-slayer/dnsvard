variable "cloudflare_api_token" {
  description = "Cloudflare API token with Pages, R2, Workers Scripts, Zone Workers Routes, Zone DNS, and Zone Settings edit permissions"
  type        = string
  sensitive   = true
}

variable "cloudflare_account_id" {
  description = "Cloudflare account ID"
  type        = string
}

variable "cloudflare_zone_id" {
  description = "Cloudflare zone ID for dnsvard.com"
  type        = string
}

variable "domain" {
  description = "Zone hostname used for installer endpoints"
  type        = string
  default     = "dnsvard.com"
}

variable "worker_name" {
  description = "Cloudflare Worker script name"
  type        = string
  default     = "dnsvard-install"
}

variable "pages_project_name" {
  description = "Cloudflare Pages project name for the landing site"
  type        = string
  default     = "dnsvard"
}

variable "pages_production_branch" {
  description = "Git branch treated as production by Cloudflare Pages"
  type        = string
  default     = "master"
}

variable "pages_bind_domain" {
  description = "If true, bind var.domain as a custom domain on the Pages project"
  type        = bool
  default     = true
}

variable "r2_bucket_name" {
  description = "R2 bucket name for installer artifacts"
  type        = string
  default     = "dnsvard-installer"
}

variable "r2_location" {
  description = "R2 bucket location hint"
  type        = string
  default     = "wnam"
}

variable "r2_manage_bucket" {
  description = "If true, manage the R2 bucket with OpenTofu"
  type        = bool
  default     = true
}

variable "r2_custom_domain" {
  description = "Optional custom domain bound to R2 bucket (for example downloads.dnsvard.com)"
  type        = string
  default     = "downloads.dnsvard.com"
}

variable "installer_base_url" {
  description = "Public base URL for installer source files (for example https://downloads.dnsvard.com)"
  type        = string
  default     = "https://downloads.dnsvard.com"
}

variable "enable_github_fallback" {
  description = "If true, fallback to GitHub raw installer paths when primary source returns non-2xx"
  type        = bool
  default     = false
}

variable "github_owner" {
  description = "GitHub org/user owning dnsvard repository"
  type        = string
  default     = "comment-slayer"
}

variable "github_repo" {
  description = "GitHub repository name"
  type        = string
  default     = "dnsvard"
}

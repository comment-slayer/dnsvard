resource "cloudflare_r2_bucket" "installer" {
  count      = var.r2_manage_bucket ? 1 : 0
  account_id = var.cloudflare_account_id
  name       = var.r2_bucket_name
  location   = var.r2_location
}

resource "cloudflare_r2_custom_domain" "installer" {
  count       = var.r2_custom_domain != "" ? 1 : 0
  account_id  = var.cloudflare_account_id
  bucket_name = var.r2_bucket_name
  domain      = var.r2_custom_domain
  enabled     = true
  zone_id     = var.cloudflare_zone_id

  depends_on = [cloudflare_r2_bucket.installer]
}

resource "cloudflare_workers_script" "installer" {
  account_id  = var.cloudflare_account_id
  script_name = var.worker_name
  content = templatefile("${path.module}/worker.js.tftpl", {
    installer_base_url     = trimsuffix(var.installer_base_url, "/")
    enable_github_fallback = var.enable_github_fallback
    github_owner           = var.github_owner
    github_repo            = var.github_repo
  })
}

resource "cloudflare_pages_project" "site" {
  account_id        = var.cloudflare_account_id
  name              = var.pages_project_name
  production_branch = var.pages_production_branch
}

resource "cloudflare_pages_domain" "site" {
  count        = var.pages_bind_domain ? 1 : 0
  account_id   = var.cloudflare_account_id
  project_name = cloudflare_pages_project.site.name
  name         = var.domain
}

resource "cloudflare_dns_record" "pages_apex" {
  count   = var.pages_bind_domain ? 1 : 0
  zone_id = var.cloudflare_zone_id
  name    = var.domain
  type    = "CNAME"
  content = cloudflare_pages_project.site.subdomain
  proxied = true
  ttl     = 1
  comment = "Managed by OpenTofu: apex route to Cloudflare Pages"
}

resource "cloudflare_zone_setting" "always_use_https" {
  zone_id    = var.cloudflare_zone_id
  setting_id = "always_use_https"
  value      = "on"
}

locals {
  installer_route_patterns = toset([
    "${var.domain}/install",
    "${var.domain}/LATEST",
    "${var.domain}/v*",
  ])
}

resource "cloudflare_workers_route" "installer" {
  for_each = local.installer_route_patterns
  zone_id  = var.cloudflare_zone_id
  pattern  = each.value
  script   = cloudflare_workers_script.installer.script_name
}

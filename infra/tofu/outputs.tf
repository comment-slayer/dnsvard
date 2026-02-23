output "worker_name" {
  description = "Installed Cloudflare Worker script name"
  value       = cloudflare_workers_script.installer.script_name
}

output "pages_project_name" {
  description = "Cloudflare Pages project serving the landing site"
  value       = cloudflare_pages_project.site.name
}

output "pages_domain" {
  description = "Cloudflare Pages custom domain when enabled"
  value       = var.pages_bind_domain ? var.domain : null
}

output "pages_domain_status" {
  description = "Cloudflare Pages custom domain status when enabled"
  value       = var.pages_bind_domain ? cloudflare_pages_domain.site[0].status : null
}

output "install_url" {
  description = "Public latest installer endpoint"
  value       = "https://${var.domain}/install"
}

output "r2_bucket_name" {
  description = "R2 bucket name used for installer artifacts"
  value       = var.r2_bucket_name
}

output "r2_s3_endpoint" {
  description = "R2 S3 API endpoint for CI uploads"
  value       = "https://${var.cloudflare_account_id}.r2.cloudflarestorage.com"
}

output "r2_custom_domain" {
  description = "Configured R2 custom domain if enabled"
  value       = var.r2_custom_domain != "" ? var.r2_custom_domain : null
}

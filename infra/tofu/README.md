# OpenTofu: installer edge proxy

This stack deploys:

- A Cloudflare Pages project for the landing site (`https://dnsvard.com/`)
- A managed apex DNS record (`dnsvard.com` CNAME -> Pages subdomain, proxied)
- A Cloudflare Worker for installer and release artifact proxying from R2

Worker-served endpoints:

- `https://dnsvard.com/install` (latest installer)
- `https://dnsvard.com/LATEST` (latest version pointer)
- `https://dnsvard.com/v*` (immutable versioned artifacts, including `vX.Y.Z/install.sh`)

The Worker proxies from a public installer source (recommended: R2 custom domain), with optional GitHub fallback.

Landing page source lives at `www/` (Astro) and builds to static assets under `www/dist/`.

Build landing page locally:

```bash
bun install --cwd www
bun run --cwd www build
```

Landing page deploy is decoupled from software releases and runs via `.github/workflows/landing-page.yml`, which deploys `www/dist` to Cloudflare Pages on push to `master`.

For local dev preview:

```bash
bun run --cwd www dev
```

For quick local preview of the built page:

```bash
python3 -m http.server 8787 --directory www/dist
```

## Security posture

- Config can be public (good for OSS transparency).
- State and secrets stay private.
- Versioned installer paths must be immutable.
- Latest path uses short cache TTL; versioned path uses immutable long TTL.

## Prerequisites

- OpenTofu >= 1.8
- Cloudflare zone for `dnsvard.com`
- Cloudflare API token with:
  - Account Cloudflare Pages: Edit
  - Account R2 Storage: Edit
  - Account Workers Scripts: Edit
  - Zone Workers Routes: Edit
  - Zone DNS: Edit (required when creating R2 custom domain)
  - Zone Settings: Edit (required for always-use-https)

## Source layout (R2 or any static origin)

Your R2 installer bucket should contain:

```text
install.sh
LATEST

v0.1.0/install.sh
v0.1.0/checksums.txt
v0.1.0/checksums.txt.sigstore.json
v0.1.0/dnsvard_v0.1.0_darwin_arm64.tar.gz
v0.1.0/dnsvard_v0.1.0_darwin_amd64.tar.gz
...
```

Recommended: map `downloads.dnsvard.com` to a private-write/public-read R2 bucket and treat `vX.Y.Z/*` as immutable. Only root `install.sh` and `LATEST` should be mutable.
The bucket itself is managed by OpenTofu (`cloudflare_r2_bucket`).

## Publish installer files

Example with Wrangler (replace bucket/version):

```bash
wrangler r2 object put dnsvard-installer/install.sh --file ./install.sh
printf 'v0.1.0\n' | wrangler r2 object put dnsvard-installer/LATEST --pipe
wrangler r2 object put dnsvard-installer/v0.1.0/install.sh --file ./install.sh
wrangler r2 object put dnsvard-installer/v0.1.0/checksums.txt --file ./dist/checksums.txt
```

For safety, publish immutable versioned objects first, verify bytes, then update mutable root objects (`LATEST`, `install.sh`).

## Landing page deploy to Pages (GitHub Actions)

Configure repository settings:

- Secret: `CLOUDFLARE_API_TOKEN`
- Secret: `CLOUDFLARE_ACCOUNT_ID`

On push to `master`, the workflow builds `www/` and deploys `www/dist` to Cloudflare Pages.

## Configure (Terraform Cloud workspace variables)

Set:

- `cloudflare_api_token` (sensitive)
- `cloudflare_account_id`
- `cloudflare_zone_id`
- `domain` (default `dnsvard.com`)
- `worker_name` (default `dnsvard-install`)
- `pages_project_name` (default `dnsvard`)
- `pages_production_branch` (default `master`)
- `pages_bind_domain` (default `true`)
- `r2_bucket_name` (default `dnsvard-installer`)
- `r2_location` (default `wnam`)
- `r2_manage_bucket` (default `true`)
- `r2_custom_domain` (for example `downloads.dnsvard.com`)
- `installer_base_url` (for example `https://downloads.dnsvard.com`)
- `enable_github_fallback` (default `false`)

Note: if using an existing R2 bucket, set `r2_manage_bucket = false`.

If `r2_custom_domain` is set, OpenTofu manages custom-domain binding to the R2 bucket.

## Deploy

```bash
tofu -chdir=infra/tofu init
tofu -chdir=infra/tofu plan
tofu -chdir=infra/tofu apply
```

## Public installer usage

```bash
curl -fsSL https://dnsvard.com/install | sh
curl -fsSL https://dnsvard.com/v0.1.0/install | sh
curl -fsSL https://dnsvard.com/LATEST
```

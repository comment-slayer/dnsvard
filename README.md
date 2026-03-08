# dnsvard

Local dev with multiple branches should not require port math.

`dnsvard` gives each worktree a stable hostname on normal ports (`80`, `3306`, `5432`, etc.) so you can run `master` + feature branches in parallel without rewriting Compose files or juggling `localhost:3xxx`.

```bash
# before (conflicts)
localhost:3000  localhost:3001  localhost:3307  localhost:5433 ...

# after (clean)
master.myapp.test
feat-auth.myapp.test
```

## 60-second quickstart

Install + bootstrap:

```bash
curl -fsSL https://dnsvard.com/install | sh
sudo dnsvard bootstrap
dnsvard doctor
```

If `dnsvard` is not found right after install:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Enable shell tab completion (auto-detects your shell):

```bash
dnsvard completion install
```

Check completion status:

```bash
dnsvard completion status
```

For Bash login shells, if completion is configured in `~/.bashrc`, ensure `~/.bash_profile` sources it:

```bash
if [[ -f ~/.bashrc ]]; then
  source ~/.bashrc
fi
```

Linux bootstrap note (when prompted for elevated resolver setup):

```bash
sudo dnsvard bootstrap -f
dnsvard bootstrap -f
```

Start your app stack as usual:

```bash
docker compose up -d
```

Hit branch/worktree hosts directly (same app/db ports, no host-port rewrites):

```bash
curl http://master.myproj.test
curl http://feat-auth.myproj.test
```

If macOS prompts for Local Network access, click Allow.

## Why people need this

Local multi-worktree dev breaks down fast:

- branch A uses `localhost:3000`, branch B also needs `3000`
- branch A MySQL is on `3306`, branch B wants `3306` too
- branch A Postgres is on `5432`, branch B wants `5432` too
- every compose stack needs custom host port rewrites

`dnsvard` removes that class of problem by routing by hostname/worktree identity, not by fragile host port juggling.

## Not just another "portless" frontend tool

`dnsvard` is not only a web dev proxy.

- It routes **DNS + HTTP + TCP** (not only browser traffic)
- It works for **databases/caches** (`psql`, Redis, etc.)
- It is **local infrastructure plumbing**, not a cloud deploy platform
- It is branch/worktree aware from your repo context

## Scope (current)

- macOS + Linux
- IPv4 only
- Docker-first discovery
- DNS + HTTP + TCP workspace routing

Linux resolver backend status for `v0.1.0`:

- `systemd-resolved`: supported, primary tested path
- `dnsmasq`: supported, lower field coverage (treat as preview hardening path)

## Platform roadmap

Current priority is Linux hardening and broader distro validation.

Linux targets:

- Debian/Ubuntu family
- Fedora family
- Arch family
- Pop!_OS

Implementation will detect capabilities (resolver stack, service manager, networking tools), not hardcode distro assumptions.

`v0.1.x` compatibility policy:

- no intentional breaking changes to CLI commands, config keys, or hostname model

## Install options

Homebrew (macOS):

```bash
brew install --cask comment-slayer/tap/dnsvard
```

If macOS blocks first launch with a message like `"dnsvard" Not Opened`:

- Open **System Settings -> Privacy & Security**
- Scroll to the bottom and click **Open Anyway** for `dnsvard`
- Run `dnsvard` again

Release installer (checksum verified):

```bash
curl -fsSL https://dnsvard.com/install | sh
```

Version-pinned installer:

```bash
curl -fsSL https://dnsvard.com/v0.1.0/install | sh
```

Security notes:

- Installer verifies release checksum
- Installer default source policy allows only `downloads.dnsvard.com`
- Default install path: `~/.local/bin` (`INSTALL_DIR` overrides)

## Quickstart

```bash
dnsvard bootstrap
dnsvard doctor
docker compose up -d
```

Linux note: resolver setup needs root, daemon runs as user. If prompted, run a root pass first, then rerun as normal user.

```bash
sudo dnsvard bootstrap -f
dnsvard bootstrap -f
```

Current Linux resolver limitation: when backend is `systemd-resolved`, use `dns_listen: 127.0.0.1:53`.

`dnsmasq` supports non-53 forwarding, but has less production mileage in this release line.

DNS behavior inside managed domains:

- Unknown names under configured domains intentionally return loopback answers instead of NXDOMAIN.
- Reason: if an early lookup returns NXDOMAIN while a service is still starting, OS/app resolvers can negatively cache that result, so immediate retries still fail even after routes become ready.
- Outside configured domains, dnsvard does not answer.

Now hit your service by hostname instead of custom localhost ports.

## Reliability behavior

`dnsvard` heals issues by component and always reports an explicit repair path.

- restart/start safety: `dnsvard daemon restart` verifies the daemon is running before reporting success
- action-level healing: each heal action tracks failure count, last failure detail, and suppression window (`blocked_until`)
- targeted recovery: persistent upstream unreachable events trigger targeted HTTP router reset without daemon-wide restart
- deterministic diagnostics: each degraded action reports a stable status code, component, and fix list

Use these commands to inspect reliability state:

```bash
dnsvard daemon status --verbose
dnsvard doctor
dnsvard doctor --json
```

When an issue is present, outputs include:

- `code`: stable machine-readable failure code
- `component`: failing subsystem
- `message`: why this is failing now
- `fixes`: one or more immediate repair steps

## Real examples

Two branches, same app port, no conflicts:

```bash
curl http://master.myproj.test
curl http://feat-auth.myproj.test
```

Two branches, same Postgres port (`5432`), still isolated:

```bash
psql -h master.myproj.test -d app
psql -h feat-auth.myproj.test -d app
```

Fast workspace cleanup with zero docker-name hunting:

```bash
dnsvard ps
dnsvard rm -f workspace/myproj/feat-auth
dnsvard rm -f workspace/myproj
```

## Hostname model

Default host pattern: `service-workspace-project-tld`

- workspace host: same pattern without `service`
- project host: `<project>.<suffix>` points to default workspace

Examples:

- `api.master.comment-slayer.test`
- `master.comment-slayer.test`
- `comment-slayer.test`

If you set `workspace-tld`, service-specific hosts are disabled (no `<service>.` hostname).

## CLI

```bash
dnsvard [-c config] bootstrap [--force|-f] [--quick]
dnsvard [-c config] uninstall [--remove|--delete]
dnsvard [-c config] doctor [--flush-cache] [--check-local-network] [--probe-routing] [--json]
dnsvard [-c config] env [--shell]
dnsvard [-c config] config global <set|get|unset|show>
dnsvard [-c config] config local <set|get|unset|show>
dnsvard [-c config] daemon <start|stop|restart|status|logs|loopback-sync>
dnsvard [-c config] ps
dnsvard [-c config] stop <target>
dnsvard [-c config] kill <target>
dnsvard [-c config] rm <target> [--force|-f]
dnsvard [-c config] run [service] [--vite|--next|--nuxt|--astro|--svelte|--webpack|--adapter <name>] -- <cmd...>
dnsvard upgrade [--version <vX.Y.Z|latest>] [--allow-downgrade]
dnsvard version
dnsvard --version
```

## Frontend dev adapters

`dnsvard run` supports explicit adapters and adapter auto-detection.

Recommended zero-config start:

```bash
dnsvard run -- bun dev
```

dnsvard will attempt to auto-detect framework adapter from command tokens and `package.json` scripts/dependencies.
If auto-detection is not possible for likely frontend dev commands, dnsvard fails fast with explicit adapter guidance.

Explicit adapter examples:

```bash
dnsvard run --vite -- bun dev
dnsvard run --next -- npm run dev
dnsvard run --adapter svelte -- bun dev
```

Adapters currently supported: `vite`, `next`, `nuxt`, `astro`, `svelte`, `webpack`.

Port selection for `dnsvard run`:

- If `DNSVARD_HTTP_PORT` is set, dnsvard requires that exact port.
- If that port is already in use, command fails with an explicit error.
- If `DNSVARD_HTTP_PORT` is not set, dnsvard auto-allocates a free local port.

## Upgrade

`dnsvard upgrade` is for installer-based installs (`curl ... | sh`) and is disabled for Homebrew installs.

Examples:

```bash
dnsvard upgrade
dnsvard upgrade --version v0.1.0
dnsvard upgrade --allow-downgrade
```

Security behavior:

- `dnsvard upgrade` enforces default host allowlists for installer and release sources
- `--version latest` fails closed if it resolves to an older version than current unless `--allow-downgrade` is set

If installed via Homebrew, use:

```bash
brew upgrade --cask comment-slayer/tap/dnsvard
```

## Config

Load order (low to high precedence):

1. `~/.config/dnsvard/config.yaml`
2. `./dnsvard.yaml`
3. `-c/--config <path>`
4. `DNSVARD_*` environment variables

Global config (`~/.config/dnsvard/config.yaml`):

```yaml
suffix: test
host_pattern: service-workspace-project-tld
loopback_cidr: 127.90.0.0/16
dns_listen: 127.0.0.1:1053
dns_ttl: 5
http_port: 80
state_dir: ~/.local/state/dnsvard
log_level: info
docker_discovery_mode: required
```

Repo-local config (`./dnsvard.yaml`):

```yaml
suffix: test
host_pattern: service-workspace-project-tld
```

- Effective `suffix` comes from the highest-precedence config source (global, local, `-c`, env).
- Effective `host_pattern` comes from the highest-precedence config source (global, local, `-c`, env).
- If `suffix` is unset everywhere, dnsvard defaults to `test`.
- `suffixes` is not supported in config.

Set config with CLI:

```bash
dnsvard config global set suffix test
dnsvard config global set host_pattern workspace-project-tld
dnsvard config global set dns_ttl 15
dnsvard config local set host_pattern workspace-tld
dnsvard config global show
dnsvard config local show
dnsvard config global
dnsvard config local
```

Multi-label DNS suffixes are supported (examples: `dev.test`, `foo.bar.test`).
dnsvard rejects `*.local` suffixes because `.local` is reserved for mDNS/Bonjour and conflicts with unicast resolver routing.

Workload operations targets:

- `ps`, `stop`, `kill`: `lease/<id>`, `container/<name-or-id>`, `workspace[/<project>[/<workspace>[/<container>]]]`, `all` (requires `--yes` for `stop`/`kill`)
- `rm`: `container/<name-or-id>`, `workspace[/<project>[/<workspace>[/<container>]]]`, `all` (requires `--yes`)

`docker_discovery_mode` values:

- `required` (default): Docker discovery failures are fatal.
- `optional`: keep workspace/runtime-only routes when Docker discovery fails.

## Docker labels

Service names are auto-discovered from Compose metadata.

Use labels only when you need aliases or explicit HTTP behavior:

```yaml
labels:
  dnsvard.service_names: "frontend,ui"
  dnsvard.http_port: "3000"
  dnsvard.default_http: "true"
  dnsvard.detect: "manual"
```

## FAQ

### Should my app bind to `127.0.0.1` or `0.0.0.0`?

Prefer `127.0.0.1` for host-native dev servers (for example `dnsvard run -- bun dev`).

- `127.0.0.1` keeps the service local to your machine.
- It avoids accidentally exposing dev services on your LAN.
- It matches dnsvard's local-first routing model.

Use `0.0.0.0` only when you intentionally need non-local access (for example another device on your network).

### Why does bootstrap sometimes require `sudo` first?

Resolver setup is a privileged operation on both macOS and Linux. dnsvard keeps long-running daemon processes as your normal user, and auto-heals drift in the background. Only privileged resolver/root-helper setup needs a root pass:

```bash
sudo dnsvard bootstrap -f
dnsvard bootstrap -f
```

### Why does dnsvard return loopback answers for unknown names under my suffix?

This is intentional. Inside managed suffixes (for example `*.test`), returning NXDOMAIN can get negatively cached by your OS/app resolver. The race is:

1. service is still starting,
2. first DNS lookup gets NXDOMAIN,
3. resolver cache keeps NXDOMAIN briefly,
4. service becomes ready,
5. next request still fails until negative cache expires.

By returning loopback answers in managed suffixes, dnsvard avoids that negative-cache race so retries can succeed as soon as routes become ready.

### Do I need `--force`/`-f` often?

Usually no. Auto-heal should cover normal drift. Use it when you intentionally want a full privileged reconcile.

### Why can `http_port: 80` fail on Linux?

Ports below `1024` are privileged. dnsvard will guide you through capability/root setup during bootstrap. If you do not need `80`, choose an unprivileged port (`>=1024`).

## Troubleshooting

If `dnsvard run -- bun dev` cannot auto-detect framework adapter:

```bash
dnsvard run --adapter vite -- bun dev
```

If hostnames under `*.test` do not resolve, check dnsvard DNS directly:

```bash
dig +short @127.0.0.1 -p 1053 <name>.test
```

If DNS record exists in dnsvard but your shell/app still fails, check system resolver cache:

```bash
dscacheutil -q host -a name <name>.test
```

If behavior looks off, give background auto-heal a moment first, then inspect status:

```bash
dnsvard doctor
```

If doctor reports blocked privileged resolver/root-helper setup (macOS or Linux):

```bash
sudo dnsvard bootstrap -f
```

If Docker discovery blocks route build and you want dnsvard to stay usable without Docker:

```bash
DNSVARD_DOCKER_DISCOVERY_MODE=optional dnsvard doctor
```

To persist this behavior, set `docker_discovery_mode: optional` in `~/.config/dnsvard/config.yaml`.

## Development

```bash
go test ./...
```

Run a development daemon without colliding with your installed dnsvard:

```bash
DNSVARD_STATE_DIR="$HOME/.local/state/dnsvard-dev" \
DNSVARD_DNS_LISTEN="127.0.0.1:1153" \
DNSVARD_HTTP_PORT=18080 \
go run ./cmd/dnsvard daemon start --foreground
```

Use a separate test suffix for dev runs:

```bash
DNSVARD_SUFFIX=dev.test go run ./cmd/dnsvard doctor
```

Adapter integration harness (real framework projects):

```bash
make test-adapters
```

Run subset:

```bash
DNSVARD_ADAPTERS=vite,next scripts/test-dev-adapters.sh
```

Linux bootstrap harness (dry-run by default):

```bash
make test-linux
```

Backend matrix (auto-detect or explicit):

```bash
DNSVARD_LINUX_BACKENDS=systemd-resolved,dnsmasq make test-linux
```

Run full Linux two-pass bootstrap check:

```bash
DNSVARD_LINUX_APPLY=1 scripts/test-linux-bootstrap.sh
```

## Releases

Releases are cut from GitHub Actions when pushing a tag matching `v*`.

```bash
git tag v0.1.0
git push origin v0.1.0
```

## Examples and docs

- `examples/http-api-postgres/README.md`
- `docs/how-dnsvard-routing-works.md`
- `docs/release-checklist-v0.1.0.md`

## OSS project docs

- `LICENSE` (Apache-2.0)
- `NOTICE`
- `CONTRIBUTING.md`
- `CODE_OF_CONDUCT.md`
- `SECURITY.md`
- `SUPPORT.md`

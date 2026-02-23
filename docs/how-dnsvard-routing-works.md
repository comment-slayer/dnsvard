# How dnsvard Routing Works

This doc explains the full local flow for hostname-based HTTP and TCP access
across multiple worktrees, without per-worktree host port mappings.

## What problem this solves

You can run multiple worktrees at once and still connect with normal ports:

- `http://master.cool-name.test`
- `http://new-feat-1.cool-name.test`
- `psql -h master.cool-name.test ...`
- `psql -h new-feat-1.cool-name.test ...`

## One-time setup and daily workflow

One-time:

```bash
sudo dnsvard bootstrap
```

Per worktree:

```bash
docker compose up -d
```

## Compose requirements

For TCP services (Postgres, Redis, etc), do not publish host `ports`. Use
container-visible ports (`expose` or `EXPOSE` in image metadata), or:

```yaml
services:
  postgres:
    expose:
      - "5432"

  redis:
    expose:
      - "6379"
```

Service DNS names are automatic from discovered service names. HTTP behavior can
use labels (`dnsvard.http_port`, optional `dnsvard.default_http`, optional
`dnsvard.service_names` aliases).

## DNS suffix terminology

dnsvard uses a configurable DNS suffix (sometimes called a domain suffix).
The correct term for values like `test`, `dev.test`, or `foo.bar.test` is a
multi-label DNS suffix when it contains dots.

Examples:

- single-label suffix: `test`
- multi-label suffix: `dev.test`
- multi-label suffix: `foo.bar.test`

Use `dnsvard config local set suffix <suffix>` to set the active suffix for the
current workspace. Use `dnsvard config global set suffix <suffix>` for a global
default. dnsvard accepts both single-label and multi-label suffixes.

Suffix selection behavior:

- global config can set a default `suffix`
- workspace `dnsvard.yaml` can override `suffix`
- if no `suffix` is set, dnsvard defaults to `test`
- `suffixes` is not supported in config
- dnsvard rejects `*.local` suffixes because `.local` resolves via mDNS/Bonjour

Host pattern selection behavior:

- global config can set a default `host_pattern`
- workspace `dnsvard.yaml` can override `host_pattern`
- if no `host_pattern` is set, dnsvard defaults to `service-workspace-project-tld`

## High-level architecture

```text
                   +------------------------------------------+
                   | macOS resolver for .<suffix>             |
                   | /etc/resolver/<suffix> -> 127.0.0.1:1053 |
                   +---------------+--------------------------+
                                   |
                                   v
                         +---------+--------+
                         | dnsvard daemon   |
                         | DNS + HTTP + TCP |
                         +----+---------+---+
                              |         |
                 DNS A records|         |HTTP host routing
                              |         |
                              v         v
                       127.90.x.y   container_ip:http_port
                              |
                              |TCP listeners per workspace ip:port
                              v
                        container_ip:tcp_port
```

## Detailed control flow

### 1) Bootstrap installs system integration

- Writes resolver config for `.<domain>` (default `.test`) to point at dnsvard
  DNS (`127.0.0.1:1053` by default).
- Installs and starts dnsvard launch agent.
- Installs root loopback sync agent.

### 2) Workspace identity and IP assignment

- dnsvard derives `project` and `workspace` labels from git/worktree context.
- Each workspace gets a stable loopback IP in `127.90.0.0/16` via allocator.
- Loopback sync ensures those IP aliases exist on `lo0`.

Example:

- `master.cool-name.test` -> `127.90.135.47`
- `new-feat-1.cool-name.test` -> `127.90.92.31`

### 3) Docker discovery

- dnsvard inspects running containers.
- It reads:
  - Compose metadata (`com.docker.compose.*`) to detect workspace/project scope.
  - dnsvard labels for HTTP routing and aliases.
  - container TCP ports from `ExposedPorts`/network port metadata.

### 4) Route construction

dnsvard builds three route sets:

- DNS routes: hostname -> workspace loopback IP.
- HTTP routes: hostname -> `http://container_ip:http_port`.
- TCP routes: `workspace_ip:port` -> `container_ip:port`.

Important: TCP routes are per workspace scope, so different worktrees can both
use `5432` at the same time.

### 5) Request path examples

HTTP example:

```text
browser curl
  -> resolve master.cool-name.test via macOS resolver
  -> dnsvard DNS returns 127.90.135.47
  -> connect to 127.90.135.47:80
  -> dnsvard HTTP router matches Host header
  -> proxy to frontend container (its container IP + HTTP port)
```

Postgres example:

```text
psql -h master.cool-name.test
  -> resolve hostname to 127.90.135.47
  -> connect to 127.90.135.47:5432
  -> dnsvard TCP proxy listener for master workspace receives connection
  -> stream forwarded to master postgres container_ip:5432
```

Second worktree in parallel:

```text
psql -h new-feat-1.cool-name.test
  -> resolve hostname to 127.90.92.31
  -> connect to 127.90.92.31:5432
  -> forwarded to new-feat-1 postgres container_ip:5432
```

Result: same client port, isolated by hostname/workspace IP.

## Why host port publishing is removed

Using host `ports` for database/cache causes conflicts across worktrees and can
be runtime-dependent for non-`127.0.0.1` loopback aliases. dnsvard TCP proxy
removes that dependency.

## Caveats

- Keep TCP port usage unique per workspace scope. If two containers in one
  workspace expose the same TCP port, dnsvard cannot reliably pick intent.
- HTTP default route is also scoped per workspace. Keep at most one
  `dnsvard.default_http=true` per workspace.

## Quick verification

```bash
dig +short @127.0.0.1 -p 1053 master.cool-name.test
dig +short @127.0.0.1 -p 1053 new-feat-1.cool-name.test

psql -d app -h master.cool-name.test -c "select value from demo_values order by value limit 1;"
psql -d app -h new-feat-1.cool-name.test -c "select value from demo_values order by value limit 1;"
```

If both queries return rows and values differ, routing isolation works.

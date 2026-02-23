# Contributing

Thanks for helping improve `dnsvard`.

## Scope and principles

- Keep onboarding simple: sane defaults, minimal setup.
- Prefer explicit config/labels where behavior can be ambiguous.
- Fail fast with actionable error messages.
- Preserve deterministic routing across workspaces.

Current `v0.1.x` scope:

- macOS + Linux
- no Windows support contract (do not add Windows compatibility shims)
- IPv4 only
- DNS + HTTP + TCP routing
- Docker-first discovery and runtime leases

Near-term platform priority:

- Linux hardening first (resolver/service-manager/network capability detection)

Linux resolver backend status for `v0.1.x`:

- `systemd-resolved`: primary tested path
- `dnsmasq`: supported but with lower field coverage

## Local development

Run tests before opening a PR:

```bash
go test ./...
```

Run adapter integration harness for frontend dev-server compatibility:

```bash
make test-adapters
```

Run subset for faster iteration:

```bash
DNSVARD_ADAPTERS=vite,next scripts/test-dev-adapters.sh
```

Run Linux bootstrap harness (safe dry-run default):

```bash
make test-linux
```

Run full Linux two-pass bootstrap verification:

```bash
DNSVARD_LINUX_APPLY=1 scripts/test-linux-bootstrap.sh
```

## Code structure

- `cmd/dnsvard`: CLI command orchestration
- `internal/platform`: OS integration boundary
- `internal/macos`: resolver and launchd implementations
- `internal/docker`: container discovery and label parsing
- `internal/dnsserver`, `internal/httprouter`, `internal/tcpproxy`: routing data plane

When adding behavior:

- Keep platform-specific details out of `cmd/dnsvard`.
- Keep command handlers focused on orchestration, not low-level mechanics.
- Add tests for non-trivial parsing or routing logic.

## Documentation updates

If behavior changes, update at least one of:

- `README.md`
- files under `docs/`
- relevant example README in `examples/`

## Pull requests

- Keep changes focused and reviewable.
- Include rationale in the PR description (why the change exists).
- Note user-visible behavior changes explicitly.

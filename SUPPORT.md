# Support

## Getting help

- Usage questions and troubleshooting: open a GitHub issue.
- Bugs: open a GitHub issue with reproduction steps.
- Security concerns: follow `SECURITY.md`.

## What to include in bug reports

- `dnsvard doctor` output
- relevant compose/service labels
- expected behavior vs actual behavior
- OS version and architecture

## FAQ

### `curl http://<host>.<domain>` fails on macOS after bootstrap

On first use, macOS may block Local Network access for `dnsvard` (or your terminal app).

Fix:

- Open `System Settings -> Privacy & Security -> Local Network`
- Allow `dnsvard` (or your terminal app)
- Retry the request

#!/usr/bin/env bash
set -euo pipefail

if git grep -nE 'os\.(Stdout|Stderr)\s*=' -- '*_test.go' >/tmp/dnsvard-stdio-guard.out; then
  echo "error: tests must not mutate global os.Stdout/os.Stderr" >&2
  cat /tmp/dnsvard-stdio-guard.out >&2
  exit 1
fi

echo "ok: no global os.Stdout/os.Stderr mutation in tests"

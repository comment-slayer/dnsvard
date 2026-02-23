#!/usr/bin/env bash
set -euo pipefail

# Linux bootstrap harness.
#
# Default mode is dry-run diagnostics only.
# Set DNSVARD_LINUX_APPLY=1 to run full two-pass bootstrap.

need() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'error: missing required command: %s\n' "$1" >&2
    exit 1
  }
}

if [ "$(uname -s)" != "Linux" ]; then
  printf 'error: this harness only runs on Linux\n' >&2
  exit 1
fi

DNSVARD_BIN="${DNSVARD_BIN:-dnsvard}"
APPLY="${DNSVARD_LINUX_APPLY:-0}"
BACKENDS="${DNSVARD_LINUX_BACKENDS:-auto}"
DOMAIN_BASE="${DNSVARD_LINUX_TEST_DOMAIN_BASE:-dnsvardlinux}"
HTTP_PORT="${DNSVARD_LINUX_HTTP_PORT:-18080}"
DNS_LISTEN_SYSTEMD="${DNSVARD_LINUX_DNS_LISTEN_SYSTEMD:-127.0.0.1:53}"
DNS_LISTEN_DNSMASQ="${DNSVARD_LINUX_DNS_LISTEN_DNSMASQ:-127.0.0.1:53053}"
HARNESS_ROOT="${DNSVARD_LINUX_HARNESS_ROOT:-$(mktemp -d)}"

need "$DNSVARD_BIN"
need awk
need mktemp

if [ "$APPLY" = "1" ]; then
  need sudo
fi

resolve_backends() {
  if [ "$BACKENDS" != "auto" ]; then
    printf '%s\n' "$BACKENDS"
    return
  fi

  found=""
  if command -v resolvectl >/dev/null 2>&1; then
    found="systemd-resolved"
  fi
  if command -v dnsmasq >/dev/null 2>&1; then
    if [ -n "$found" ]; then
      found="$found,dnsmasq"
    else
      found="dnsmasq"
    fi
  fi

  if [ -z "$found" ]; then
    found="systemd-resolved"
  fi

  printf '%s\n' "$found"
}

cleanup_case() {
  local backend="$1"
  local domain="$2"
  local case_dir="$3"
  local config_file="$case_dir/config.yaml"
  local state_dir="$case_dir/state"

  DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" daemon stop >/dev/null 2>&1 || true

  if [ "${DNSVARD_LINUX_CLEANUP_RESOLVER:-1}" = "1" ]; then
    case "$backend" in
      systemd-resolved)
        sudo rm -f "/etc/systemd/resolved.conf.d/dnsvard-$domain.conf" >/dev/null 2>&1 || true
        sudo systemctl restart systemd-resolved >/dev/null 2>&1 || true
        ;;
      dnsmasq)
        sudo rm -f "/etc/dnsmasq.d/dnsvard-$domain.conf" >/dev/null 2>&1 || true
        sudo systemctl restart dnsmasq >/dev/null 2>&1 || sudo systemctl restart NetworkManager >/dev/null 2>&1 || true
        ;;
    esac
  fi

  rm -f "$config_file"
  rm -f "$state_dir/daemon.pid" "$state_dir/daemon.log" "$state_dir/allocator-state.json" "$state_dir/runtime-leases.json" "$state_dir/resolver-sync-state.json" "$state_dir/bootstrap-state.json"
  rmdir "$state_dir" >/dev/null 2>&1 || true
  rmdir "$case_dir" >/dev/null 2>&1 || true
}

run_case() {
  local backend="$1"
  local domain="$2"
  local dns_listen="$3"
  local case_dir="$HARNESS_ROOT/$backend"
  local state_dir="$case_dir/state"
  local config_file="$case_dir/config.yaml"

  mkdir -p "$state_dir"
  cat >"$config_file" <<EOF
domains: [$domain]
domain: $domain
loopback_cidr: 127.90.0.0/16
dns_listen: $dns_listen
dns_ttl: 5
http_port: $HTTP_PORT
state_dir: $state_dir
log_level: info
EOF

  printf '\n=== backend: %s ===\n' "$backend"
  printf -- '- config: %s\n' "$config_file"
  printf -- '- domain: %s\n' "$domain"
  printf -- '- dns_listen: %s\n' "$dns_listen"
  printf -- '- apply mode: %s\n' "$APPLY"

  printf '[1/4] doctor baseline\n'
  DOCTOR_OUTPUT="$(DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" doctor 2>&1 || true)"
  printf '%s\n' "$DOCTOR_OUTPUT"

  if [ "$APPLY" != "1" ]; then
    printf 'dry-run only for backend %s\n' "$backend"
    cleanup_case "$backend" "$domain" "$case_dir"
    return
  fi

  printf '[2/4] bootstrap root pass\n'
  if [ "$(id -u)" -eq 0 ]; then
    DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" bootstrap --force
  else
    sudo DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" bootstrap --force
  fi

  printf '[3/4] bootstrap user pass\n'
  if [ "$(id -u)" -eq 0 ]; then
    if [ -n "${SUDO_USER:-}" ]; then
      sudo -u "$SUDO_USER" DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" bootstrap --force
    else
      printf 'error: running as root without SUDO_USER; cannot execute user pass automatically\n' >&2
      cleanup_case "$backend" "$domain" "$case_dir"
      exit 1
    fi
  else
    DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" bootstrap --force
  fi

  printf '[4/4] verification\n'
  DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" doctor
  DNSVARD_LINUX_RESOLVER_BACKEND="$backend" "$DNSVARD_BIN" -c "$config_file" daemon status || true

  cleanup_case "$backend" "$domain" "$case_dir"
  printf 'backend %s completed\n' "$backend"
}

cleanup_all() {
  rmdir "$HARNESS_ROOT" >/dev/null 2>&1 || true
}
trap cleanup_all EXIT INT TERM

printf 'Linux harness\n'
printf -- '- dnsvard binary: %s\n' "$DNSVARD_BIN"
printf -- '- backends: %s\n' "$BACKENDS"
printf -- '- harness root: %s\n' "$HARNESS_ROOT"

BACKEND_LIST="$(resolve_backends)"
IFS=',' read -r -a backend_items <<<"$BACKEND_LIST"
for backend in "${backend_items[@]}"; do
  backend="$(printf '%s' "$backend" | awk '{$1=$1};1')"
  [ -n "$backend" ] || continue

  case "$backend" in
    systemd-resolved)
      run_case "$backend" "$DOMAIN_BASE-systemd" "$DNS_LISTEN_SYSTEMD"
      ;;
    dnsmasq)
      run_case "$backend" "$DOMAIN_BASE-dnsmasq" "$DNS_LISTEN_DNSMASQ"
      ;;
    *)
      printf 'error: unknown backend entry: %s\n' "$backend" >&2
      exit 1
      ;;
  esac
done

printf '\nLinux bootstrap harness completed.\n'

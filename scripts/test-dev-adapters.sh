#!/usr/bin/env bash
set -euo pipefail

# Real adapter harness: builds minimal projects for popular dev servers,
# runs them through dnsvard, and verifies each generated host is reachable.

need() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'error: missing required command: %s\n' "$1" >&2
    exit 1
  }
}

need bun
need curl
need mktemp

DNSVARD_BIN="${DNSVARD_BIN:-dnsvard}"
need "$DNSVARD_BIN"

HARNESS_ROOT="${DNSVARD_ADAPTER_HARNESS_ROOT:-$(mktemp -d)}"
ADAPTERS="${DNSVARD_ADAPTERS:-vite,next,nuxt,astro,svelte,webpack}"
printf 'Harness workspace: %s\n' "$HARNESS_ROOT"
printf 'Adapters: %s\n' "$ADAPTERS"
printf 'dnsvard binary: %s\n' "$DNSVARD_BIN"

wait_for_log() {
  local log_file="$1"
  local needle="$2"
  local timeout_sec="$3"

  local i=0
  while [ "$i" -lt "$timeout_sec" ]; do
    if [ -f "$log_file" ] && grep -q "$needle" "$log_file"; then
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done
  return 1
}

wait_for_http() {
  local url="$1"
  local timeout_sec="$2"
  local host
  host="${url#http://}"
  host="${host%%/*}"

  local i=0
  while [ "$i" -lt "$timeout_sec" ]; do
    code="$(curl -sS -o /dev/null -w '%{http_code}' --resolve "$host:80:127.0.0.1" "$url" || true)"
    case "$code" in
      2*|3*|4*)
        return 0
        ;;
    esac
    sleep 1
    i=$((i + 1))
  done
  return 1
}

stop_run_process() {
  local pid="$1"

  kill -INT "$pid" >/dev/null 2>&1 || true
  local i=0
  while [ "$i" -lt 8 ]; do
    if ! kill -0 "$pid" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done

  kill -TERM "$pid" >/dev/null 2>&1 || true
  i=0
  while [ "$i" -lt 4 ]; do
    if ! kill -0 "$pid" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done

  kill -KILL "$pid" >/dev/null 2>&1 || true
}

write_vite_project() {
  local dir="$1"
  mkdir -p "$dir/src"
  cat >"$dir/package.json" <<'EOF'
{
  "name": "adapter-vite",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite --clearScreen false"
  },
  "devDependencies": {
    "vite": "^7.3.1"
  }
}
EOF
  cat >"$dir/vite.config.js" <<'EOF'
import { defineConfig } from 'vite';

export default defineConfig({
  server: {
    host: '127.0.0.1',
    port: Number(process.env.PORT) || 5173,
    strictPort: true
  }
});
EOF
  cat >"$dir/index.html" <<'EOF'
<!doctype html>
<html>
  <body>
    <div id="app">vite-ok</div>
  </body>
</html>
EOF
}

write_next_project() {
  local dir="$1"
  mkdir -p "$dir/pages"
  cat >"$dir/package.json" <<'EOF'
{
  "name": "adapter-next",
  "private": true,
  "scripts": {
    "dev": "./dev.sh"
  },
  "dependencies": {
    "next": "^15.4.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0"
  }
}
EOF
  cat >"$dir/dev.sh" <<'EOF'
#!/bin/sh
set -eu
exec next dev -p "${PORT:-3000}" -H 127.0.0.1
EOF
  chmod +x "$dir/dev.sh"
  cat >"$dir/pages/index.js" <<'EOF'
export default function Home() {
  return <main>next-ok</main>;
}
EOF
}

write_nuxt_project() {
  local dir="$1"
  mkdir -p "$dir"
  cat >"$dir/package.json" <<'EOF'
{
  "name": "adapter-nuxt",
  "private": true,
  "scripts": {
    "dev": "sh -c 'nuxt dev --host 127.0.0.1 --port ${PORT:-3000}'"
  },
  "dependencies": {
    "nuxt": "^3.17.0"
  }
}
EOF
  cat >"$dir/app.vue" <<'EOF'
<template>
  <main>nuxt-ok</main>
</template>
EOF
}

write_astro_project() {
  local dir="$1"
  mkdir -p "$dir/src/pages"
  cat >"$dir/package.json" <<'EOF'
{
  "name": "adapter-astro",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "sh -c 'astro dev --host 127.0.0.1 --port ${PORT:-4321}'"
  },
  "devDependencies": {
    "astro": "^5.12.0"
  }
}
EOF
  cat >"$dir/src/pages/index.astro" <<'EOF'
<html>
  <body>
    <main>astro-ok</main>
  </body>
</html>
EOF
}

write_svelte_project() {
  local dir="$1"
  mkdir -p "$dir/src"
  cat >"$dir/package.json" <<'EOF'
{
  "name": "adapter-svelte",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite --clearScreen false"
  },
  "devDependencies": {
    "@sveltejs/vite-plugin-svelte": "^5.0.0",
    "svelte": "^5.0.0",
    "vite": "^7.3.1"
  }
}
EOF
  cat >"$dir/vite.config.js" <<'EOF'
import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte()],
  server: {
    host: '127.0.0.1',
    port: Number(process.env.PORT) || 5173,
    strictPort: true
  }
});
EOF
  cat >"$dir/index.html" <<'EOF'
<!doctype html>
<html>
  <body>
    <div id="app"></div>
    <script type="module" src="/src/main.js"></script>
  </body>
</html>
EOF
  cat >"$dir/src/App.svelte" <<'EOF'
<main>svelte-ok</main>
EOF
  cat >"$dir/src/main.js" <<'EOF'
import { mount } from 'svelte';
import App from './App.svelte';

mount(App, { target: document.getElementById('app') });
EOF
}

write_webpack_project() {
  local dir="$1"
  mkdir -p "$dir/src"
  cat >"$dir/package.json" <<'EOF'
{
  "name": "adapter-webpack",
  "private": true,
  "scripts": {
    "dev": "webpack serve --mode development"
  },
  "devDependencies": {
    "html-webpack-plugin": "^5.6.0",
    "webpack": "^5.95.0",
    "webpack-cli": "^5.1.4",
    "webpack-dev-server": "^5.1.0"
  }
}
EOF
  cat >"$dir/webpack.config.js" <<'EOF'
const HtmlWebpackPlugin = require('html-webpack-plugin');

module.exports = {
  entry: './src/index.js',
  devServer: {
    host: '127.0.0.1',
    port: Number(process.env.PORT) || 8080,
    allowedHosts: 'all'
  },
  plugins: [
    new HtmlWebpackPlugin({ templateContent: '<!doctype html><html><body><div id="app"></div></body></html>' })
  ]
};
EOF
  cat >"$dir/src/index.js" <<'EOF'
document.getElementById('app').textContent = 'webpack-ok';
EOF
}

run_case() {
  local adapter="$1"
  local writer_func="$2"
  local dir="$HARNESS_ROOT/$adapter"
  local log_file="$dir/run.log"

  mkdir -p "$dir"
  "$writer_func" "$dir"

  printf '\n[%s] installing dependencies...\n' "$adapter"
  bun install --cwd "$dir" >/dev/null

  printf '[%s] starting dnsvard run: %s run --adapter %s -- bun dev\n' "$adapter" "$DNSVARD_BIN" "$adapter"
  (
    cd "$dir"
    exec "$DNSVARD_BIN" run --adapter "$adapter" -- bun dev >"$log_file" 2>&1
  ) &
  local run_pid=$!

  if ! wait_for_log "$log_file" 'public url:' 120; then
    printf '[%s] error: did not emit public url in time\n' "$adapter" >&2
    stop_run_process "$run_pid"
    wait "$run_pid" >/dev/null 2>&1 || true
    return 1
  fi

  local public_url
  public_url="$(grep 'public url:' "$log_file" | tail -n1 | awk '{print $4}')"
  if [ -z "$public_url" ]; then
    printf '[%s] error: failed to parse public url\n' "$adapter" >&2
    stop_run_process "$run_pid"
    wait "$run_pid" >/dev/null 2>&1 || true
    return 1
  fi

  printf '[%s] probing %s ...\n' "$adapter" "$public_url"
  if ! wait_for_http "$public_url" 60; then
    printf '[%s] error: URL not reachable: %s\n' "$adapter" "$public_url" >&2
    stop_run_process "$run_pid"
    wait "$run_pid" >/dev/null 2>&1 || true
    return 1
  fi

  printf '[%s] ok\n' "$adapter"
  stop_run_process "$run_pid"
  wait "$run_pid" >/dev/null 2>&1 || true
}

IFS=',' read -r -a adapter_list <<<"$ADAPTERS"
for adapter in "${adapter_list[@]}"; do
  case "$adapter" in
    vite) run_case "vite" write_vite_project ;;
    next) run_case "next" write_next_project ;;
    nuxt) run_case "nuxt" write_nuxt_project ;;
    astro) run_case "astro" write_astro_project ;;
    svelte) run_case "svelte" write_svelte_project ;;
    webpack) run_case "webpack" write_webpack_project ;;
    *)
      printf 'error: unknown adapter in DNSVARD_ADAPTERS: %s\n' "$adapter" >&2
      exit 1
      ;;
  esac
done

printf '\nAll adapter harness checks passed.\n'

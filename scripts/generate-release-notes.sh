#!/usr/bin/env bash
set -euo pipefail

output_path="${1:-dist/release-notes.md}"
current_tag="${2:-${GITHUB_REF_NAME:-}}"

if [[ -z "${current_tag}" ]]; then
  printf 'error: release tag is required (pass as arg2 or set GITHUB_REF_NAME)\n' >&2
  exit 1
fi

is_excluded_path() {
  local path="$1"
  case "$path" in
    .gitignore | LICENSE | NOTICE | .github/CODEOWNERS )
      return 0
      ;;
    *.md | dnsvard.yaml | */dnsvard.yaml | docs/* | examples/* | infra/* | scripts/* | www/* )
      return 0
      ;;
  esac
  return 1
}

commit_has_relevant_changes() {
  local sha="$1"
  local saw_file=0
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    saw_file=1
    if ! is_excluded_path "$path"; then
      return 0
    fi
  done < <(git diff-tree --no-commit-id --name-only -r "$sha")

  [[ "$saw_file" -eq 1 ]] && return 1
  return 1
}

previous_tag="$(git describe --tags --abbrev=0 "${current_tag}^" 2>/dev/null || true)"
range="$current_tag"
if [[ -n "$previous_tag" ]]; then
  range="${previous_tag}..${current_tag}"
fi

release_lines=()
while IFS=$'\t' read -r sha subject; do
  [[ -z "$sha" ]] && continue
  if commit_has_relevant_changes "$sha"; then
    release_lines+=("- ${subject} (${sha:0:7})")
  fi
done < <(git log --format='%H%x09%s' --reverse "$range")

mkdir -p "$(dirname "$output_path")"

{
  printf '## Changelog\n\n'
  if [[ "${#release_lines[@]}" -eq 0 ]]; then
    printf -- '- No runtime/binary-relevant changes in this release.\n'
  else
    printf '%s\n' "${release_lines[@]}"
  fi
  if [[ -n "$previous_tag" ]]; then
    printf '\n_Compared against `%s`._\n' "$previous_tag"
  fi
} > "$output_path"

printf 'generated release notes: %s\n' "$output_path"

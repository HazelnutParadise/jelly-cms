#!/usr/bin/env bash
set -euo pipefail

pele_version='1.9.0'
pele_commit='e7f092a4de147cc32210913c20e36eaa5fcd0ef1'
pele_integrity='sha512-KUmBt7P5JduVKQaP6FKfXiRgH3LBVIKsmdgPJZU7jD3ye6F+Z8fbms+OjivE7rWY09Qx+5X65VKe/9ywKFJ9Sw=='

usage() {
  cat <<'USAGE'
Usage: scripts/vendor/pele.sh [--help]

Fetch and verify the pinned Pele npm package, then update its vendored browser
bundle, LICENSE, and VERSION. Update pele_version, pele_commit, and
pele_integrity in this script before upgrading.
USAGE
}

case "${1:-}" in
  --help|-h) usage; exit 0 ;;
  '') ;;
  *) usage >&2; exit 2 ;;
esac

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

actual_commit="$(git ls-remote --tags https://github.com/obsidianmd/pele "refs/tags/$pele_version" | awk 'NR == 1 { print $1 }')"
if [[ "$actual_commit" != "$pele_commit" ]]; then
  printf 'Pele tag commit mismatch: expected %s, got %s\n' "$pele_commit" "${actual_commit:-<missing>}" >&2
  exit 1
fi

pack_name="$(cd "$tmp_dir" && npm pack "pele@$pele_version" --silent)"
actual_integrity="sha512-$(openssl dgst -sha512 -binary "$tmp_dir/$pack_name" | openssl base64 -A)"
if [[ "$actual_integrity" != "$pele_integrity" ]]; then
  printf 'Pele npm integrity mismatch: expected %s, got %s\n' "$pele_integrity" "$actual_integrity" >&2
  exit 1
fi

tar -xzf "$tmp_dir/$pack_name" -C "$tmp_dir"
js_file="$tmp_dir/package/dist/pele.global.js"
license_file="$tmp_dir/package/LICENSE"
actual_sha="$(shasum -a 256 "$js_file" | awk '{print $1}')"

out_dir="$repo_root/web/admin/assets/pele"
mkdir -p "$out_dir"
cp "$js_file" "$out_dir/pele.global.js"
cp "$license_file" "$out_dir/LICENSE"
cat > "$out_dir/VERSION" <<VERSION
Pele
Version: $pele_version
Source: https://github.com/obsidianmd/pele
Commit: $pele_commit
Package: https://registry.npmjs.org/pele/-/pele-$pele_version.tgz
Package integrity: $pele_integrity
File: dist/pele.global.js
File SHA-256: $actual_sha
Build date (UTC): $(date -u +%F)
VERSION

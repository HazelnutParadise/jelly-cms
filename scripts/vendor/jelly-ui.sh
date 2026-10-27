#!/usr/bin/env bash
# Rebuild the pinned Jelly UI browser bundle (ticket 12, Z2).
set -euo pipefail

jelly_ui_commit=1b775385b17884ecb48374615c96ec6ee1b58d21
fluent_icons_license_commit=60d561a815310bd52914fa0bccee14836428e23b

usage() {
  cat <<'USAGE'
Usage: scripts/vendor/jelly-ui.sh [--help]

Fetch the pinned jelly-org/ui source, run npm ci and npm run build in an
isolated temporary directory, then write jelly.js, LICENSE and VERSION under
web/admin/assets/jelly-ui/. Requires git, Node.js, npm and network access.
USAGE
}

case "${1:-}" in
  --help|-h) usage; exit 0 ;;
  '') ;;
  *) usage >&2; exit 2 ;;
esac

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
out_dir="$repo_root/web/admin/assets/jelly-ui"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

git clone --quiet --no-checkout https://github.com/jelly-org/ui.git "$tmp_dir/source"
git -C "$tmp_dir/source" fetch --quiet --depth 1 origin "$jelly_ui_commit"
git -C "$tmp_dir/source" checkout --quiet --detach FETCH_HEAD
actual_commit="$(git -C "$tmp_dir/source" rev-parse HEAD)"
if [[ "$actual_commit" != "$jelly_ui_commit" ]]; then
  printf 'Jelly UI commit mismatch: expected %s, got %s\n' "$jelly_ui_commit" "$actual_commit" >&2
  exit 1
fi

cd "$tmp_dir/source"
PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm ci --no-audit --no-fund
PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1 npm run build

version="$(node -p "require('./package.json').version")"
build_date="$(date -u +%F)"
node_version="$(node --version)"
npm_version="$(npm --version)"
mkdir -p "$out_dir"
sed '/^\/\/# sourceMappingURL=/d' dist/jelly.js > "$out_dir/jelly.js"
cp LICENSE "$out_dir/LICENSE"
cat >> "$out_dir/LICENSE" <<FLUENT_LICENSE

==== Fluent System Icons (Microsoft Corporation) ====
The upstream Jelly UI source includes a subset of Fluent System Icons as inline
SVG files. The upstream source attribution is in src/icons/index.ts at the
pinned Jelly UI commit. Microsoft license text was retrieved from the official
repository at https://github.com/microsoft/fluentui-system-icons/blob/${fluent_icons_license_commit}/LICENSE
on 2026-10-27. The Fluent System Icons LICENSE source commit is pinned in
scripts/vendor/jelly-ui.sh as fluent_icons_license_commit.

MIT License

Copyright (c) 2020 Microsoft Corporation

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
FLUENT_LICENSE

bundle_sha256="$(shasum -a 256 "$out_dir/jelly.js" | awk '{print $1}')"
cat > "$out_dir/VERSION" <<VERSION
Version: $version
Source: https://github.com/jelly-org/ui
Commit: $jelly_ui_commit
Build date (UTC): $build_date
Build: npm ci && npm run build; PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
Node: $node_version
npm: $npm_version
jelly.js SHA-256: $bundle_sha256
Source map comment: stripped; source map is not shipped
VERSION

printf 'wrote %s (SHA-256 %s)\n' "$out_dir/jelly.js" "$bundle_sha256"

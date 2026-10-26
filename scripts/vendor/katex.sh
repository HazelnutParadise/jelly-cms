#!/usr/bin/env bash
# Rebuilds the KaTeX script, stylesheet and woff2 fonts under web/vendor/katex/ (ticket 09, G1).
#
# usage: scripts/vendor/katex.sh
#
# Downloads the pinned release tarball from the npm registry, checks its SHA-512
# integrity, and copies dist/katex.min.js, dist/katex.min.css, the woff2 fonts and
# the license. Only woff2 is shipped; katex.min.css lists woff2 first, so browsers
# never request the woff or ttf fallbacks. Requires curl, tar and python3.
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "$0")/../.." && pwd)"
out_dir="$repo_root/web/vendor/katex"
build_date="$(date -u +%Y-%m-%d)"
temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

version=0.19.1
commit=8000fbd4e29cca2c0cb1e88b745af3ed2f436b66
url="https://registry.npmjs.org/katex/-/katex-$version.tgz"
integrity="sha512-jG8uKntjSEcm2EpQOh0XturpPuuC9r3Sq5HLHdsFdWZT27E672SF19QEi+of0/YeOy15lDgRhKvr5614uRrEfA=="

curl --fail --location --silent --show-error --retry 3 --output "$temp_dir/katex.tgz" "$url"
actual="sha512-$(python3 -c 'import base64,hashlib,sys; print(base64.b64encode(hashlib.sha512(open(sys.argv[1], "rb").read()).digest()).decode())' "$temp_dir/katex.tgz")"
if [[ "$actual" != "$integrity" ]]; then
  printf 'integrity mismatch for %s: expected %s, got %s\n' "$url" "$integrity" "$actual" >&2
  exit 1
fi
tar -xzf "$temp_dir/katex.tgz" -C "$temp_dir"
dist="$temp_dir/package/dist"

mkdir -p "$out_dir/fonts"
rm -f "$out_dir"/fonts/*.woff2
cp "$dist/katex.min.js" "$dist/katex.min.css" "$out_dir/"
cp "$dist"/fonts/*.woff2 "$out_dir/fonts/"
cp "$temp_dir/package/LICENSE" "$out_dir/LICENSE"

cat > "$out_dir/VERSION" <<EOF
KaTeX $version
source: https://github.com/KaTeX/KaTeX commit $commit
package: $url
integrity: $integrity
build date: $build_date
files: katex.min.js, katex.min.css, fonts/*.woff2 (woff and ttf fallbacks are not shipped)
EOF

echo "wrote $out_dir"

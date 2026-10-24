#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd -- "$(dirname -- "$0")/../.." && pwd)"
charset="$repo_root/scripts/vendor/fonts-charset.txt"
build_date="$(date -u +%Y-%m-%d)"
temp_dir="$(mktemp -d)"
trap 'rm -rf "$temp_dir"' EXIT

bricolage_commit=6ce172f74aa355ea43eb964fa4a91570a4d3064d
noto_commit=3be1884c48c3e45b52ecc725676a08f87776373e
fonttools_version=4.60.1
brotli_version=1.2.1

if [[ ! -s "$charset" ]]; then
  echo "missing or empty charset file: $charset" >&2
  exit 1
fi

fetch_source() {
  local name="$1" url="$2" expected_sha256="$3" destination="$temp_dir/$1"
  if [[ -n "${FONT_SOURCE_DIR:-}" ]]; then
    cp "$FONT_SOURCE_DIR/$name" "$destination"
  else
    curl --fail --location --silent --show-error --retry 3 --output "$destination" "$url"
  fi

  local actual_sha256
  actual_sha256="$(python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1], "rb").read()).hexdigest())' "$destination")"
  if [[ "$actual_sha256" != "$expected_sha256" ]]; then
    printf 'SHA-256 mismatch for %s: expected %s, got %s\n' "$name" "$expected_sha256" "$actual_sha256" >&2
    exit 1
  fi
}

fetch_source "bricolage.ttf" \
  "https://raw.githubusercontent.com/google/fonts/$bricolage_commit/ofl/bricolagegrotesque/BricolageGrotesque%5Bopsz%2Cwdth%2Cwght%5D.ttf" \
  413e7357809ddd12fd80a96a8a396de0e401638d4acd3cb3e37532f0472ac682
fetch_source "noto.ttf" \
  "https://raw.githubusercontent.com/google/fonts/$noto_commit/ofl/notosanstc/NotoSansTC%5Bwght%5D.ttf" \
  864727d210d54f2537bbe23b3a839436c3992af72de9322af5270897246bd44f
fetch_source "bricolage-OFL.txt" \
  "https://raw.githubusercontent.com/google/fonts/$bricolage_commit/ofl/bricolagegrotesque/OFL.txt" \
  4b5a7d8f37f5602621c8a8d7358a6a2e71317e6c231c661e15aef0275d3e07ba
fetch_source "noto-OFL.txt" \
  "https://raw.githubusercontent.com/google/fonts/$noto_commit/ofl/notosanstc/OFL.txt" \
  1c05c68c34f9708415aada51f17e1b0092d2cea709bf4a94cd38114f9e73d7d9

if python3 -c 'import brotli,fontTools,sys; sys.exit(0 if fontTools.version == sys.argv[1] and brotli.__version__ == sys.argv[2] else 1)' \
  "$fonttools_version" "$brotli_version"; then
  subset_python="$(command -v python3)"
else
  python3 -m venv "$temp_dir/venv"
  subset_python="$temp_dir/venv/bin/python"
  "$subset_python" -m pip install --disable-pip-version-check --quiet \
    "fonttools==$fonttools_version" "brotli==$brotli_version"
fi

"$subset_python" -m fontTools.subset "$temp_dir/bricolage.ttf" \
  --text-file="$charset" --flavor=woff2 --output-file="$temp_dir/bricolage-grotesque.woff2" \
  --layout-features='*' --ignore-missing-glyphs --no-hinting --desubroutinize
"$subset_python" -m fontTools.subset "$temp_dir/noto.ttf" \
  --text-file="$charset" --flavor=woff2 --output-file="$temp_dir/noto-sans-tc.woff2" \
  --layout-features='*' --ignore-missing-glyphs --no-hinting

{
  printf 'Bricolage Grotesque (SIL Open Font License 1.1)\n\n'
  sed 's/[[:blank:]]*$//' "$temp_dir/bricolage-OFL.txt"
  printf '\n\nNoto Sans TC (SIL Open Font License 1.1)\n\n'
  sed 's/[[:blank:]]*$//' "$temp_dir/noto-OFL.txt"
} > "$temp_dir/LICENSE"

cat > "$temp_dir/VERSION" <<EOF
Bricolage Grotesque
Version: 1.001
Source: https://github.com/google/fonts
Commit: $bricolage_commit
Source file SHA-256: 413e7357809ddd12fd80a96a8a396de0e401638d4acd3cb3e37532f0472ac682

Noto Sans TC
Version: 2.004-H2
Source: https://github.com/google/fonts
Commit: $noto_commit
Source file SHA-256: 864727d210d54f2537bbe23b3a839436c3992af72de9322af5270897246bd44f

Subset charset: scripts/vendor/fonts-charset.txt
Build tools: fontTools $fonttools_version, Brotli $brotli_version
Build date (UTC): $build_date
EOF

# design/project/fonts is for the design boards only: it is git-ignored and rebuilt here (ticket 12).
for destination in \
  "$repo_root/web/themes/default/assets/fonts" \
  "$repo_root/web/admin/assets/fonts" \
  "$repo_root/design/project/fonts"; do
  mkdir -p "$destination"
  cp "$temp_dir/bricolage-grotesque.woff2" "$destination/bricolage-grotesque.woff2"
  cp "$temp_dir/noto-sans-tc.woff2" "$destination/noto-sans-tc.woff2"
  cp "$temp_dir/LICENSE" "$destination/LICENSE"
  cp "$temp_dir/VERSION" "$destination/VERSION"
done

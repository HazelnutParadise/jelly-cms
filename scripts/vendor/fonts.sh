#!/usr/bin/env bash
# Vendors the public theme fonts (ticket 05, B4): Bricolage Grotesque and Noto Sans TC as woff2 subsets,
# written to web/themes/default/assets/fonts/ and web/admin/assets/fonts/ with LICENSE and VERSION files.
# Planned steps: download the original fonts, subset them with pyftsubset using fonts-charset.txt, and
# record the version. Not implemented yet.
set -euo pipefail

echo "usage: scripts/vendor/fonts.sh" >&2
echo "not implemented: see docs/tickets/05-public-theme.md (B4 fonts)" >&2
exit 1

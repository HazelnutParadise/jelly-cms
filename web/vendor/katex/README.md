# KaTeX vendor files (placeholder)

This directory will hold the KaTeX stylesheet and its woff2 fonts, shared by
the admin pages and the theme (ticket 09, G1). They are built by
`scripts/vendor/katex.sh`, which is not implemented yet.

Required when the files are added:

- `LICENSE` (the upstream KaTeX license)
- `VERSION` (release, source commit, and build date)

Pages must not load these from a CDN.

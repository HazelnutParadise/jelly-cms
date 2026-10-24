# KaTeX

KaTeX 0.19.1 is bundled for the editor preview and public content. `VERSION`
records the source commit, npm archive, SHA-512 integrity and build date.
`LICENSE` contains the upstream MIT license.

Run `scripts/vendor/katex.sh` from any directory to rebuild the JavaScript,
stylesheet and 20 woff2 fonts. The script verifies the pinned archive before
copying its distribution files. Browsers use the first, woff2, font source;
woff and ttf fallback formats are not distributed.

Pages load these local assets only when their content contains mathematics.
Rendering uses `trust: false`; invalid TeX remains readable as source.

# Vendored front-end assets

Third-party front-end packages are shipped as built files inside `web/`. Pages
never load scripts, styles, fonts or icons from a CDN or any external URL
(AGENTS.md).

Rules:

- Each vendored package has a `LICENSE` file and a `VERSION` file in its
  directory. `VERSION` records the release, the source commit, and the build
  date.
- The script that fetches and rebuilds a package lives in this directory, named
  after the package (`codemirror.sh`, `katex.sh`). Rebuilding from the script
  must reproduce the files in `web/`.
- Before each release, update every vendored package to its latest version and
  rerun the acceptance checks (`docs/release-checklist.md`).

Scripts:

| Script | Output | Status |
|---|---|---|
| `codemirror.sh` | `web/admin/assets/codemirror/` | not implemented |
| `katex.sh` | `web/vendor/katex/` | not implemented |

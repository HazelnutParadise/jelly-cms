# Render golden files

This directory holds the golden files for `internal/markup`. Each feature has
two files with the same base name:

- `<feature>.jmd` is the Jelly MD source. It is the input to `Render`.
- `<feature>.html` is the expected output of `Render` for that source.

Example: `callout.jmd` is paired with `callout.html`. The feature name is
lowercase ASCII with hyphens, one feature per file pair, for example
`heading-id`, `container-hero`, `math-inline`.

Rules:

- The golden file is the reviewed output. Change it only with the render
  change that causes the difference, and review the diff.
- Keep each `.jmd` file small. One feature per file makes a failing diff easy
  to read.

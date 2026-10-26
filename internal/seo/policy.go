package seo

// RobotsDirectives returns the robot directives configured for a public page.
// noindex and noai are per-page overrides; snippetMode is one of default,
// short or noarchive. The optional image flag preserves the existing call
// shape while allowing callers to append noimageindex.
func RobotsDirectives(snippetMode string, noindex, noai bool, imageIndexAllowed ...bool) []string {
	directives := []string{}
	switch snippetMode {
	case "short":
		directives = append(directives, "max-snippet:160", "max-image-preview:standard")
	case "noarchive":
		directives = append(directives, "noarchive", "max-image-preview:large")
	default:
		directives = append(directives, "max-image-preview:large")
	}
	if noindex {
		directives = append(directives, "noindex")
	}
	if noai {
		directives = append(directives, "noai")
	}
	if len(imageIndexAllowed) > 0 && !imageIndexAllowed[0] {
		directives = append(directives, "noimageindex")
	}
	return directives
}

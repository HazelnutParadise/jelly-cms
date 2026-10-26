package seo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Crawler policy presets (ai_policy_preset).
const (
	PresetBlockTrainingAllowSearch = "block_training_allow_search"
	PresetBlockAll                 = "block_all"
	PresetAllowAll                 = "allow_all"
)

// customRule is one entry of ai_custom_rules.
type customRule struct {
	UserAgent string `json:"user_agent"`
	Action    string `json:"action"`
	Path      string `json:"path"`
}

// robotsGroup collects the directives of one user agent.
type robotsGroup struct {
	agent string
	lines []string
}

// blocked reports whether the policy keeps bot out of the whole site. An
// explicit ai_bots entry ("allow" or "block") wins over the preset.
func blocked(preset string, bots map[string]string, bot Bot) bool {
	switch strings.ToLower(bots[bot.Name]) {
	case "block":
		return true
	case "allow":
		return false
	}
	switch preset {
	case PresetBlockAll:
		return true
	case PresetAllowAll:
		return false
	default:
		return bot.Purpose == "train"
	}
}

// validRules returns the custom rules that can be written safely. A rule with
// a control character, a missing user agent or a path not starting with "/"
// is skipped rather than written.
func validRules(raw string) []customRule {
	if raw == "" {
		return nil
	}
	var rules []customRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil
	}
	out := rules[:0]
	for _, rule := range rules {
		rule.UserAgent = strings.TrimSpace(rule.UserAgent)
		rule.Action = strings.ToLower(strings.TrimSpace(rule.Action))
		if rule.UserAgent == "" || hasControl(rule.UserAgent) || strings.ContainsAny(rule.UserAgent, ":#") {
			continue
		}
		if !strings.HasPrefix(rule.Path, "/") || hasControl(rule.Path) || strings.Contains(rule.Path, "#") {
			continue
		}
		if rule.Action != "allow" && rule.Action != "disallow" {
			continue
		}
		out = append(out, rule)
	}
	return out
}

// hasControl reports whether s has a control character, such as a line break.
func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// Robots renders /robots.txt for the current policy. The default group keeps
// crawlers out of the admin and account areas. Each catalog crawler the
// preset blocks gets its own group with "Disallow: /". Custom rules are added
// to their user agent's group, so a catalog refresh cannot remove them.
func Robots(ctx context.Context, siteURL string) ([]byte, error) {
	if err := readyDB(); err != nil {
		return nil, err
	}
	preset := option(ctx, keyAIPolicyPreset)
	var bots map[string]string
	if raw := option(ctx, keyAIBots); raw != "" {
		if err := json.Unmarshal([]byte(raw), &bots); err != nil {
			bots = nil
		}
	}

	var groups []robotsGroup
	index := map[string]int{}
	add := func(agent string, lines ...string) {
		key := strings.ToLower(agent)
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, robotsGroup{agent: agent})
		}
		groups[i].lines = append(groups[i].lines, lines...)
	}

	if option(ctx, keySEOIndexSite) == "false" {
		add("*", "Disallow: /")
	} else {
		add("*", "Disallow: /admin/", "Disallow: /api/", "Disallow: /login", "Disallow: /install",
			"Disallow: /checkout", "Disallow: /account")
	}
	for _, bot := range Catalog() {
		if blocked(preset, bots, bot) {
			add(bot.Name, "Disallow: /")
		}
	}
	for _, rule := range validRules(option(ctx, keyAICustomRules)) {
		directive := "Disallow: "
		if rule.Action == "allow" {
			directive = "Allow: "
		}
		add(rule.UserAgent, directive+rule.Path)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# AI crawler catalog %s\n", CatalogVersion)
	if siteURL != "" {
		fmt.Fprintf(&b, "Sitemap: %s\n", absolute(siteURL, "/sitemap.xml"))
	}
	for _, group := range groups {
		fmt.Fprintf(&b, "\nUser-agent: %s\n", group.agent)
		for _, line := range group.lines {
			b.WriteString(line + "\n")
		}
	}
	return []byte(b.String()), nil
}

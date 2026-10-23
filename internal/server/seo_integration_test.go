package server

// Ticket 08b (docs/tickets/08-system.md): crawler policy and SEO output. Covers
// robots.txt presets and custom rules, the robots file beating a slug catch-all,
// meta robots and X-Robots-Tag per type, per-post noindex, the sitemap scope and
// split, TDM reservation, llms.txt, server-side 403 for crawlers that ignore
// robots.txt, the 7-day crawler statistics and the IndexNow key file. The contract
// is the 「搜尋引擎與 AI 爬蟲」 bullet, the 公開 route list and the SEO option keys.
// These tests fail until 08b lands. Do not edit them to match an implementation.
//
// Assumptions where the ticket does not name a shape: custom rules are objects with
// user_agent, action and path; per-post AI opt-out is seo_ai = "noai"; crawler
// statistics are a JSON tree whose entries carry bot, action and count. The
// preset names are the ones the task names (block_training_allow_search,
// block_all, allow_all) and the seed default is block_training_allow_search.

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/seo"
	"github.com/labstack/echo/v4"
)

// s08bRobots fetches /robots.txt, requires text/plain and returns its per-agent rules.
func s08bRobots(t *testing.T, h *accessHarness) (string, map[string][]string) {
	t.Helper()
	r := s08bGet(h, "/robots.txt", nil)
	s08bWant(t, "GET /robots.txt", r, http.StatusOK)
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("GET /robots.txt Content-Type = %q, want text/plain", ct)
	}
	return r.body, s08bRobotsGroups(r.body)
}

// s08bRobotsGroups maps each lower-cased user agent to its directives, written
// as "key: value" with the key lower-cased. A User-agent line starts a new group
// only after a directive, so consecutive agent lines share one group.
func s08bRobotsGroups(body string) map[string][]string {
	groups := map[string][]string{}
	var agents []string
	inAgentBlock := false
	for _, raw := range strings.Split(body, "\n") {
		line := raw
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "user-agent" {
			if !inAgentBlock {
				agents = nil
			}
			agents = append(agents, strings.ToLower(value))
			inAgentBlock = true
			continue
		}
		inAgentBlock = false
		for _, agent := range agents {
			groups[agent] = append(groups[agent], key+": "+value)
		}
	}
	return groups
}

// s08bHasNoindexMeta reports whether a robots meta tag in body carries noindex.
var s08bMetaTag = regexp.MustCompile(`(?is)<meta\b[^>]*>`)

func s08bHasNoindexMeta(body string) bool {
	for _, tag := range s08bMetaTag.FindAllString(body, -1) {
		lower := strings.ToLower(tag)
		if strings.Contains(lower, "robots") && strings.Contains(lower, "noindex") {
			return true
		}
	}
	return false
}

// s08bBotCounts sums the count fields of crawler statistics entries by bot name.
// An entry is any object with a "bot" string; it contributes count, hits or requests.
func s08bBotCounts(v any, out map[string]float64) {
	switch x := v.(type) {
	case map[string]any:
		if bot, ok := x["bot"].(string); ok {
			for _, key := range []string{"count", "hits", "requests"} {
				if n, ok := s08bNumber(x[key]); ok {
					out[bot] += n
					break
				}
			}
		}
		for _, child := range x {
			s08bBotCounts(child, out)
		}
	case []any:
		for _, child := range x {
			s08bBotCounts(child, out)
		}
	}
}

// s08bUA returns a crawler User-Agent header value for the named bot.
func s08bUA(name string) string {
	return "Mozilla/5.0 (compatible; " + name + "/1.0; +https://example.test/bot)"
}

func TestRobotsPerPreset(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "s08b-robots-admin")
	token := h.token(admin)
	catalog := seo.Catalog()

	s08bSetOptions(t, h, token, map[string]string{"ai_policy_preset": "block_training_allow_search"})
	_, groups := s08bRobots(t, h)
	for _, bot := range catalog {
		blocked := s08bContains(groups[strings.ToLower(bot.Name)], "disallow: /")
		switch bot.Purpose {
		case "train":
			if !blocked {
				t.Errorf("block_training_allow_search: training crawler %s is not disallowed", bot.Name)
			}
		case "search":
			if blocked {
				t.Errorf("block_training_allow_search: search crawler %s is disallowed", bot.Name)
			}
		}
	}

	s08bSetOptions(t, h, token, map[string]string{"ai_policy_preset": "block_all"})
	_, groups = s08bRobots(t, h)
	for _, bot := range catalog {
		if !s08bContains(groups[strings.ToLower(bot.Name)], "disallow: /") {
			t.Errorf("block_all: crawler %s is not disallowed", bot.Name)
		}
	}

	s08bSetOptions(t, h, token, map[string]string{"ai_policy_preset": "allow_all"})
	_, groups = s08bRobots(t, h)
	for _, bot := range catalog {
		if s08bContains(groups[strings.ToLower(bot.Name)], "disallow: /") {
			t.Errorf("allow_all: crawler %s is disallowed", bot.Name)
		}
	}

	// Custom rules survive a change of preset, which stands in for a catalog refresh.
	rules := []map[string]string{{"user_agent": "ProbeBot", "action": "disallow", "path": "/private-probe/"}}
	s08bSetOptions(t, h, token, map[string]string{
		"ai_policy_preset": "block_training_allow_search",
		"ai_custom_rules":  s08bJSON(t, rules),
	})
	_, groups = s08bRobots(t, h)
	if !s08bContains(groups["probebot"], "disallow: /private-probe/") {
		t.Errorf("custom rule for ProbeBot missing from robots.txt; groups=%v", groups["probebot"])
	}
	s08bSetOptions(t, h, token, map[string]string{"ai_policy_preset": "block_all"})
	s08bSetOptions(t, h, token, map[string]string{"ai_policy_preset": "block_training_allow_search"})
	_, groups = s08bRobots(t, h)
	if !s08bContains(groups["probebot"], "disallow: /private-probe/") {
		t.Errorf("custom rule for ProbeBot lost after the catalog-derived rules were rebuilt")
	}
}

// s08bContains reports whether want is in list.
func s08bContains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestRobotsBeatsSlugCatchAll(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "s08b-robots-slug-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Robots Slug Impostor", slug: "robots.txt", typ: "page"})

	r := s08bGet(h, "/robots.txt", nil)
	s08bWant(t, "GET /robots.txt with a page slug robots.txt", r, http.StatusOK)
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("GET /robots.txt Content-Type = %q, want text/plain", ct)
	}
	if strings.Contains(r.body, "Robots Slug Impostor") {
		t.Errorf("GET /robots.txt serves the page with slug robots.txt")
	}
	if !strings.Contains(strings.ToLower(r.body), "user-agent:") {
		t.Errorf("GET /robots.txt has no user-agent directive; body=%q", s08bSnippet(r.body))
	}
}

func TestMetaRobotsAndXRobotsTagPerType(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-metarobots-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-metarobots-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Meta Robots Post", slug: "meta-robots-post"})
	s08bSetOptions(t, h, token, map[string]string{"seo_index_types": s08bJSON(t, map[string]bool{"search": false})})

	search := s08bGet(h, "/search?q=x", nil)
	s08bWant(t, "GET /search?q=x", search, http.StatusOK)
	if !s08bHasNoindexMeta(search.body) {
		t.Errorf("search page with seo_index_types search=false has no noindex meta robots")
	}
	if !strings.Contains(strings.ToLower(search.header.Get("X-Robots-Tag")), "noindex") {
		t.Errorf("search page X-Robots-Tag = %q, want noindex", search.header.Get("X-Robots-Tag"))
	}

	post := s08bGet(h, "/post/meta-robots-post", nil)
	s08bWant(t, "GET /post/meta-robots-post", post, http.StatusOK)
	if s08bHasNoindexMeta(post.body) {
		t.Errorf("post page carries noindex although only search is set")
	}
	if strings.Contains(strings.ToLower(post.header.Get("X-Robots-Tag")), "noindex") {
		t.Errorf("post page X-Robots-Tag = %q, want no noindex", post.header.Get("X-Robots-Tag"))
	}
}

func TestPerPostOverrideNoindexLeavesSitemap(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "s08b-override-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Override Visible Post", slug: "override-visible"})
	s08bSeedPost(t, h, author, s08bPost{title: "Override Hidden Post", slug: "override-hidden"})
	if err := h.db.Exec(`UPDATE posts SET seo_index = 'noindex' WHERE slug = ?`, "override-hidden").Error; err != nil {
		t.Fatalf("set seo_index on hidden post: %v", err)
	}

	hidden := s08bGet(h, "/post/override-hidden", nil)
	s08bWant(t, "GET /post/override-hidden", hidden, http.StatusOK)
	if !s08bHasNoindexMeta(hidden.body) {
		t.Errorf("post with seo_index=noindex has no noindex meta robots")
	}

	sitemap := s08bGet(h, "/sitemap-posts-1.xml", nil)
	s08bWant(t, "GET /sitemap-posts-1.xml", sitemap, http.StatusOK)
	archivesWantText(t, "/sitemap-posts-1.xml", sitemap.body, "/post/override-visible")
	archivesWantAbsent(t, "/sitemap-posts-1.xml", sitemap.body, "/post/override-hidden")
}

func TestSitemapPublicScopeAndSplit(t *testing.T) {
	h := newAccessHarness(t)
	author := h.user("author", "s08b-sitemap-author")
	s08bSeedPost(t, h, author, s08bPost{title: "Scope Public Entry", slug: "scope-public"})
	s08bSeedPost(t, h, author, s08bPost{title: "Scope Draft Entry", slug: "scope-draft", status: "draft"})
	s08bSeedPost(t, h, author, s08bPost{title: "Scope Private Entry", slug: "scope-private", visibility: "private"})
	s08bSeedPost(t, h, author, s08bPost{title: "Scope Future Entry", slug: "scope-future", publishedAt: time.Now().UTC().Add(30 * 24 * time.Hour)})
	trashed := s08bSeedPost(t, h, author, s08bPost{title: "Scope Trashed Entry", slug: "scope-trashed"})
	if err := h.db.Delete(&trashed).Error; err != nil {
		t.Fatalf("trash post: %v", err)
	}

	part := s08bGet(h, "/sitemap-posts-1.xml", nil)
	s08bWant(t, "GET /sitemap-posts-1.xml", part, http.StatusOK)
	archivesWantText(t, "/sitemap-posts-1.xml", part.body, "/post/scope-public")
	archivesWantAbsent(t, "/sitemap-posts-1.xml", part.body, "scope-draft", "scope-private", "scope-future", "scope-trashed")

	// Split: 50001 more public posts. Part 1 holds 50000 URLs; part 2 holds the rest.
	if err := h.db.Exec(`INSERT INTO posts (title, slug, content, type, status, author_id, visibility, pinned, allow_comments, published_at, created_at, updated_at)
		SELECT 'Bulk ' || g, 'bulk-' || g, '', 'post', 'published', ?, 'public', false, true, now() - interval '1 day', now() - interval '1 day', now() - interval '1 day'
		FROM generate_series(1, 50001) AS g`, author.ID).Error; err != nil {
		t.Fatalf("seed 50001 public posts: %v", err)
	}
	total := s08bCount(t, h, `SELECT COUNT(*) FROM posts WHERE type = 'post' AND status = 'published' AND visibility = 'public' AND deleted_at IS NULL AND published_at <= now()`)

	index := s08bGet(h, "/sitemap.xml", nil)
	s08bWant(t, "GET /sitemap.xml", index, http.StatusOK)
	archivesWantText(t, "/sitemap.xml", index.body, "sitemap-posts-1.xml", "sitemap-posts-2.xml")

	p1 := s08bGet(h, "/sitemap-posts-1.xml", nil)
	s08bWant(t, "GET /sitemap-posts-1.xml after split", p1, http.StatusOK)
	if n := strings.Count(p1.body, "<url>"); n != 50000 {
		t.Errorf("sitemap-posts-1.xml has %d URLs, want 50000", n)
	}
	p2 := s08bGet(h, "/sitemap-posts-2.xml", nil)
	s08bWant(t, "GET /sitemap-posts-2.xml", p2, http.StatusOK)
	if n := strings.Count(p2.body, "<url>"); int64(n) != total-50000 {
		t.Errorf("sitemap-posts-2.xml has %d URLs, want %d", n, total-50000)
	}
	s08bWant(t, "GET /sitemap-posts-3.xml (empty part)", s08bGet(h, "/sitemap-posts-3.xml", nil), http.StatusNotFound)
}

func TestTDMHeaderAndWellKnown(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-tdm-admin")
	token := h.token(admin)

	s08bSetOptions(t, h, token, map[string]string{"tdm_reservation": "true"})
	home := s08bGet(h, "/", nil)
	s08bWant(t, "GET /", home, http.StatusOK)
	if got := home.header.Get("TDM-Reservation"); got != "1" {
		t.Errorf("TDM-Reservation header = %q, want 1", got)
	}
	wk := s08bGet(h, "/.well-known/tdmrep.json", nil)
	s08bWant(t, "GET /.well-known/tdmrep.json", wk, http.StatusOK)
	reservation, ok := s08bFindKey(s08bDecode(t, wk.body), "tdm-reservation")
	if !ok {
		t.Errorf("tdmrep.json has no tdm-reservation; body=%q", s08bSnippet(wk.body))
	} else if n, isNum := s08bNumber(reservation); !isNum || n != 1 {
		t.Errorf("tdm-reservation = %v, want 1", reservation)
	}

	s08bSetOptions(t, h, token, map[string]string{"tdm_reservation": "false"})
	if got := s08bGet(h, "/", nil).header.Get("TDM-Reservation"); got != "" {
		t.Errorf("TDM-Reservation header = %q with reservation off, want none", got)
	}
}

func TestLLMSTxtOmitsBlockedAI(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-llms-admin")
	token := h.token(admin)
	author := h.user("author", "s08b-llms-author")
	s08bSeedPost(t, h, author, s08bPost{title: "LLMS Open Entry", slug: "llms-open"})
	s08bSeedPost(t, h, author, s08bPost{title: "LLMS Blocked Entry", slug: "llms-blocked"})
	if err := h.db.Exec(`UPDATE posts SET seo_ai = 'noai' WHERE slug = ?`, "llms-blocked").Error; err != nil {
		t.Fatalf("set seo_ai on blocked post: %v", err)
	}

	s08bSetOptions(t, h, token, map[string]string{"llms_txt_enabled": "true", "llms_txt_intro": "Probe intro for models"})
	r := s08bGet(h, "/llms.txt", nil)
	s08bWant(t, "GET /llms.txt", r, http.StatusOK)
	archivesWantText(t, "/llms.txt", r.body, "LLMS Open Entry")
	archivesWantAbsent(t, "/llms.txt", r.body, "LLMS Blocked Entry", "llms-blocked")

	s08bSetOptions(t, h, token, map[string]string{"llms_txt_enabled": "false"})
	s08bWant(t, "GET /llms.txt disabled", s08bGet(h, "/llms.txt", nil), http.StatusNotFound)
}

func TestServerSide403OnlyForIgnoresRobots(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-serverside-admin")
	token := h.token(admin)

	flagged := ""
	for _, bot := range seo.Catalog() {
		if bot.IgnoresRobots {
			flagged = bot.Name
			break
		}
	}
	if flagged == "" {
		t.Errorf("no catalog bot is flagged IgnoresRobots; the catalog must flag crawlers observed ignoring robots.txt")
		flagged = "Bytespider"
	}

	s08bSetOptions(t, h, token, map[string]string{"ai_block_server_side": "true"})
	s08bWant(t, "GET / as GPTBot with server-side blocking on", s08bGet(h, "/", map[string]string{"User-Agent": s08bUA("GPTBot")}), http.StatusOK)
	s08bWant(t, "GET / as flagged "+flagged, s08bGet(h, "/", map[string]string{"User-Agent": s08bUA(flagged)}), http.StatusForbidden)

	s08bSetOptions(t, h, token, map[string]string{"ai_block_server_side": "false"})
	s08bWant(t, "GET / as flagged "+flagged+" with server-side blocking off", s08bGet(h, "/", map[string]string{"User-Agent": s08bUA(flagged)}), http.StatusOK)
}

func TestCrawlerStatsEndpoint(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "s08b-stats-admin")
	editor := h.user("editor", "s08b-stats-editor")
	token, editorToken := h.token(admin), h.token(editor)
	s08bSetOptions(t, h, token, map[string]string{"ai_block_server_side": "false"})

	for i := 0; i < 2; i++ {
		s08bWant(t, "GET / as GPTBot", s08bGet(h, "/", map[string]string{"User-Agent": s08bUA("GPTBot")}), http.StatusOK)
	}
	s08bWant(t, "GET / as OAI-SearchBot", s08bGet(h, "/", map[string]string{"User-Agent": s08bUA("OAI-SearchBot")}), http.StatusOK)

	r := h.request(http.MethodGet, "/api/admin/seo/crawler-stats?days=7", "", token, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(r, "GET /api/admin/seo/crawler-stats")
	s08bWant(t, "GET /api/admin/seo/crawler-stats", r, http.StatusOK)
	counts := map[string]float64{}
	s08bBotCounts(s08bDecode(t, r.body), counts)
	if counts["GPTBot"] != 2 {
		t.Errorf("7-day GPTBot count = %v, want 2; body=%q", counts["GPTBot"], s08bSnippet(r.body))
	}
	if counts["OAI-SearchBot"] != 1 {
		t.Errorf("7-day OAI-SearchBot count = %v, want 1", counts["OAI-SearchBot"])
	}
	if _, ok := s08bFindKey(s08bDecode(t, r.body), "action"); !ok {
		t.Errorf("crawler stats carry no action dimension")
	}

	denied := h.request(http.MethodGet, "/api/admin/seo/crawler-stats?days=7", "", editorToken, echo.MIMEApplicationJSON, "")
	h.mustNoPanic(denied, "GET crawler-stats as editor")
	if denied.code != http.StatusForbidden {
		t.Errorf("editor reads crawler stats with %d, want 403", denied.code)
	}
}

func TestIndexNowKeyFile(t *testing.T) {
	h := newAccessHarness(t)
	admin := h.user("admin", "s08b-indexnow-admin")
	token := h.token(admin)
	key := "0123456789abcdef0123456789abcdef"

	s08bSetOptions(t, h, token, map[string]string{"seo_indexnow_enabled": "true", "seo_indexnow_key": key})
	r := s08bGet(h, fmt.Sprintf("/%s.txt", key), nil)
	s08bWant(t, "GET /<key>.txt", r, http.StatusOK)
	if got := strings.TrimSpace(r.body); got != key {
		t.Errorf("IndexNow key file body = %q, want the key", got)
	}

	s08bSetOptions(t, h, token, map[string]string{"seo_indexnow_enabled": "false"})
	s08bWant(t, "GET /<key>.txt when IndexNow is off", s08bGet(h, fmt.Sprintf("/%s.txt", key), nil), http.StatusNotFound)
}

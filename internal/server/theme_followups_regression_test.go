package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// These are black-box HTTP regressions for the public search contract. They use the
// isolated PostgreSQL harness and intentionally do not depend on search internals.
func TestFollowupRegressionSearchUnicodeFoldExpansion(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-fold-expansion")
	cases := []struct {
		name, content, query string
	}{
		{"sharp s expands", "<p>Straße</p>", "STRASSE"},
		{"final sigma folds", "<p>ΟΣ</p>", "ος"},
		{"ligature expands", "<p>office ﬃce</p>", "office ffice"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			followupSeed(t, h, author, "Fold Entry "+tc.name, fmt.Sprintf("fu-fold-%d", i), tc.content, i+1)
			r := followupJSONGet(h, "/api/search?q="+url.QueryEscape(tc.query))
			if r.code != 200 {
				t.Fatalf("GET /api/search status = %d, want 200; body=%q", r.code, archivesSnippet(r.body))
			}
			var got struct {
				Posts []struct {
					Title string `json:"title"`
				} `json:"posts"`
			}
			if err := json.Unmarshal([]byte(r.body), &got); err != nil {
				t.Fatalf("decode search response: %v", err)
			}
			if len(got.Posts) != 1 || got.Posts[0].Title != "Fold Entry "+tc.name {
				t.Errorf("GET /api/search?q=%q returned titles %v, want the matching post", tc.query, got.Posts)
			}
		})
	}
}

func TestFollowupRegressionSearchMatchesVisibleTextAcrossMarkup(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-visible-text")
	for i, tc := range []struct{ title, slug, content, query string }{
		{"Inline split", "fu-inline-split", `<p>sweet<b>jelly</b></p>`, "sweetjelly"},
		{"Block boundary", "fu-block-boundary", `<p>first</p><p>second</p>`, "first second"},
		{"Named entity", "fu-named-entity", `<p>Tom &amp; Jerry</p>`, "Tom & Jerry"},
		{"Numeric entity", "fu-numeric-entity", `<p>&#x679C;&#x51CD;</p>`, "果凍"},
	} {
		followupSeed(t, h, author, tc.title, tc.slug, tc.content, i+1)
		r := followupJSONGet(h, "/api/search?q="+url.QueryEscape(tc.query))
		if r.code != 200 || !strings.Contains(r.body, `"title":"`+tc.title+`"`) {
			t.Errorf("GET /api/search?q=%q status=%d body=%q, want %q", tc.query, r.code, archivesSnippet(r.body), tc.title)
		}
	}
}

func TestFollowupRegressionSearchIgnoresNonVisibleMarkup(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-ignore-markup")
	followupSeed(t, h, author, "Hidden Markup Entry", "fu-hidden-markup", `<p>visible</p><script>needle-script</script><style>.needle-style{color:red}</style><p data-search="needle-attribute">still visible</p>`, 1)
	for _, q := range []string{"needle-script", "needle-style", "needle-attribute"} {
		r := followupJSONGet(h, "/api/search?q="+url.QueryEscape(q))
		if r.code != 200 || strings.Contains(r.body, "Hidden Markup Entry") {
			t.Errorf("GET /api/search?q=%q matched non-visible markup: status=%d body=%q", q, r.code, archivesSnippet(r.body))
		}
	}
}

func TestFollowupRegressionSearchTreatsPatternCharactersLiterally(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-literal-pattern")
	for i, q := range []string{"100%", "under_score", `back\slash`} {
		followupSeed(t, h, author, "Literal Entry "+fmt.Sprint(i), fmt.Sprintf("fu-literal-%d", i), "<p>literal "+q+" value</p>", i+1)
	}
	followupSeed(t, h, author, "Percent Decoy", "fu-literal-percent-decoy", "<p>literal 100X value</p>", 4)
	followupSeed(t, h, author, "Underscore Decoy", "fu-literal-underscore-decoy", "<p>literal underXscore value</p>", 5)
	for i, q := range []string{"100%", "under_score", `back\slash`} {
		r := followupJSONGet(h, "/api/search?q="+url.QueryEscape(q))
		wantTitle := fmt.Sprintf("Literal Entry %d", i)
		if r.code != 200 || !strings.Contains(r.body, `"title":"`+wantTitle+`"`) {
			t.Errorf("GET /api/search?q=%q status=%d body=%q, want the literal match", q, r.code, archivesSnippet(r.body))
		}
		var got struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal([]byte(r.body), &got); err != nil || got.Count != 1 {
			t.Errorf("GET /api/search?q=%q count=%d err=%v, want exactly one literal match", q, got.Count, err)
		}
	}
}

func TestFollowupRegressionFontCacheOverHTTP(t *testing.T) {
	h := archivesHarness(t)
	server := httptest.NewServer(h.echo)
	defer server.Close()
	client := server.Client()
	type httpResult struct {
		response *http.Response
		body     []byte
	}
	get := func(path string) httpResult {
		t.Helper()
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Errorf("read GET %s response: %v", path, err)
		}
		if err := response.Body.Close(); err != nil {
			t.Errorf("close GET %s response: %v", path, err)
		}
		return httpResult{response: response, body: body}
	}
	for _, name := range []string{"bricolage-grotesque.woff2", "noto-sans-tc.woff2"} {
		path := "/admin/assets/fonts/" + name
		legacy := get(path)
		legacyCache := legacy.response.Header.Get("Cache-Control")
		if legacy.response.StatusCode != http.StatusOK || len(legacy.body) == 0 || !strings.Contains(legacyCache, "no-cache") {
			t.Errorf("real unhashed GET %s status=%d bytes=%d Cache-Control=%q; want 200 and no-cache", path, legacy.response.StatusCode, len(legacy.body), legacyCache)
			continue
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(legacy.body))
		versioned := get(path + "?v=" + hash)
		cacheControl := versioned.response.Header.Get("Cache-Control")
		if versioned.response.StatusCode != http.StatusOK || !strings.EqualFold(fmt.Sprintf("%x", sha256.Sum256(versioned.body)), hash) {
			t.Errorf("real hashed GET %s status=%d; want 200 with bytes matching %s", path, versioned.response.StatusCode, hash)
		}
		if !strings.Contains(cacheControl, "public") || !strings.Contains(cacheControl, "max-age=31536000") || !strings.Contains(cacheControl, "immutable") {
			t.Errorf("real hashed GET %s Cache-Control=%q; want public one-year immutable", path, cacheControl)
		}
	}
	missing := get("/admin/assets/fonts/followup-missing-font.woff2")
	if missing.response.StatusCode != http.StatusNotFound || strings.Contains(missing.response.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("real GET missing font status=%d Cache-Control=%q; want 404 without immutable caching", missing.response.StatusCode, missing.response.Header.Get("Cache-Control"))
	}
	other := get("/admin/assets/editor.js")
	if other.response.StatusCode != http.StatusOK || !strings.Contains(other.response.Header.Get("Cache-Control"), "no-cache") {
		t.Errorf("real GET other admin asset status=%d Cache-Control=%q; want 200 with no-cache", other.response.StatusCode, other.response.Header.Get("Cache-Control"))
	}
}

func TestFollowupRegressionSearchPaginatesMoreThanTwoHundredTrueMatches(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-many-matches")
	posts := make([]core.Post, 0, 225)
	base := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 225; i++ {
		published := base.Add(time.Duration(i) * time.Hour)
		posts = append(posts, core.Post{Title: fmt.Sprintf("Bulk Entry %03d", i), Slug: fmt.Sprintf("fu-bulk-%03d", i),
			Content: "<p>bulkneedle</p>", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID,
			PublishedAt: &published, CreatedAt: published, UpdatedAt: published})
	}
	if err := h.db.CreateInBatches(&posts, 100).Error; err != nil {
		t.Fatalf("seed 225 matching posts: %v", err)
	}
	for _, page := range []int{1, 2, 23} {
		body := archivesPage(t, h, fmt.Sprintf("/search?q=bulkneedle&page=%d", page))
		if count := followupFoundCount(body); count != 225 {
			t.Errorf("search page %d reports %d matches, want exact total 225", page, count)
		}
		doc := followupParse(t, body)
		pageInfo := followupByClass(doc, "page-info")
		gotPageInfo := ""
		if len(pageInfo) == 1 {
			gotPageInfo = followupText(pageInfo[0])
		}
		if gotPageInfo != fmt.Sprintf("%d / 23", page) {
			t.Errorf("search page %d pagination info = %q, want %d / 23", page, gotPageInfo, page)
		}
		entries := followupByClass(doc, "search-hit")
		want := 10
		if page == 23 {
			want = 5
		}
		if len(entries) != want {
			t.Errorf("search page %d contains %d result cards, want %d", page, len(entries), want)
		}
	}
}

func TestFollowupRegressionSearchLargeFalseCandidateSet(t *testing.T) {
	h := archivesHarness(t)
	author := h.user("author", "fu-false-candidates")
	posts := make([]core.Post, 0, 251)
	base := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 250; i++ {
		published := base.Add(time.Duration(i) * time.Hour)
		posts = append(posts, core.Post{Title: fmt.Sprintf("Decoy Entry %03d", i), Slug: fmt.Sprintf("fu-decoy-%03d", i),
			Content: `<p>decoy text</p><script>rareneedle</script><a data-needle="rareneedle">ordinary link</a>`, Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID,
			PublishedAt: &published, CreatedAt: published, UpdatedAt: published})
	}
	if err := h.db.CreateInBatches(&posts, 100).Error; err != nil {
		t.Fatalf("seed false candidate corpus: %v", err)
	}
	followupSeed(t, h, author, "One Visible Match", "fu-one-visible", "<p>rareneedle</p>", 251)
	r := followupJSONGet(h, "/api/search?q=rareneedle")
	if r.code != 200 || !strings.Contains(r.body, `"title":"One Visible Match"`) || strings.Contains(r.body, "Decoy Entry") {
		t.Errorf("search over false candidates status=%d body=%q, want only the visible match", r.code, archivesSnippet(r.body))
	}
}

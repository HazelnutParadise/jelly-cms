package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestSEOPolicyImageRobotsTitleAndDefaultOpenGraphImage(t *testing.T) {
	h := archivesHarness(t)
	admin := h.user("admin", "seo-policy-admin")
	token := h.token(admin)
	author := h.user("author", "seo-policy-author")
	const title = "Policy Contract Entry"
	s08bSeedPost(t, h, author, s08bPost{title: title, slug: "seo-policy-contract"})
	s08bSetOptions(t, h, token, map[string]string{
		"site_title":           "Policy Site",
		"seo_images_index":     "false",
		"seo_title_format":     "{title} :: {site}",
		"seo_default_og_image": "https://example.test/default-og.png",
	})

	page := s08bGet(h, "/post/seo-policy-contract", nil)
	s08bWant(t, "GET SEO policy post", page, http.StatusOK)
	if !strings.Contains(page.body, "<title>"+title+" :: Policy Site</title>") {
		t.Errorf("page title does not apply seo_title_format: %s", s08bSnippet(page.body))
	}
	if !strings.Contains(page.body, `<meta property="og:image" content="https://example.test/default-og.png"`) {
		t.Errorf("page head has no configured default Open Graph image: %s", s08bSnippet(page.body))
	}
	if !strings.Contains(strings.ToLower(page.body), "noimageindex") {
		t.Errorf("seo_images_index=false did not emit noimageindex: %s", s08bSnippet(page.body))
	}

	part := s08bGet(h, "/sitemap-posts-1.xml", nil)
	s08bWant(t, "GET policy sitemap", part, http.StatusOK)
	if strings.Contains(part.body, "<image:image>") || strings.Contains(part.body, "<image:loc>") {
		t.Errorf("image indexing is disabled but sitemap still lists image URLs")
	}
}

package seo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
)

// llmsPostLimit caps how many posts llms.txt lists.
const llmsPostLimit = 200

// llmsTitle makes a title safe for one Markdown list item on one line.
func llmsTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(title)
}

// LLMSTxt renders /llms.txt: the site name, the admin's intro and the posts
// that allow AI use. A post with seo_ai set to noai is left out. While
// llms_txt_enabled is off the document answers ErrNotFound.
func LLMSTxt(ctx context.Context, siteURL string) ([]byte, error) {
	if err := readyDB(); err != nil {
		return nil, err
	}
	if option(ctx, keyLLMSEnabled) != "true" {
		return nil, ErrNotFound
	}
	db := data.DB.WithContext(ctx)
	urls := urlsFrom(ctx)
	var rows []core.Post
	err := publicPosts(db, "post").Where("seo_ai <> ?", "noai").
		Select("id, slug, type, title, published_at, created_at, updated_at").
		Order("COALESCE(published_at, created_at) DESC, id DESC").
		Limit(llmsPostLimit).Find(&rows).Error
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	name := option(ctx, keySiteTitle)
	if name == "" {
		name = "Jelly CMS"
	}
	fmt.Fprintf(&b, "# %s\n\n", llmsTitle(name))
	if desc := option(ctx, keySiteDescription); desc != "" {
		fmt.Fprintf(&b, "> %s\n\n", strings.Join(strings.Fields(desc), " "))
	}
	if intro := strings.TrimSpace(option(ctx, keyLLMSIntro)); intro != "" {
		b.WriteString(intro + "\n\n")
	}
	b.WriteString("## Posts\n\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "- [%s](%s)\n", llmsTitle(row.Title), absolute(siteURL, urls.Post(row)))
	}
	return []byte(b.String()), nil
}

// tdmEntry is one record of /.well-known/tdmrep.json (TDMRep).
type tdmEntry struct {
	Location       string `json:"location"`
	TDMReservation int    `json:"tdm-reservation"`
	TDMPolicy      string `json:"tdm-policy,omitempty"`
}

// TDMRep renders /.well-known/tdmrep.json. It always answers with the site
// location; tdm-reservation is 1 only while tdm_reservation is on.
func TDMRep(ctx context.Context, siteURL string) ([]byte, error) {
	entry := tdmEntry{Location: absolute(siteURL, "/*")}
	if option(ctx, keyTDMReservation) == "true" {
		entry.TDMReservation = 1
	}
	entry.TDMPolicy = strings.TrimSpace(option(ctx, keyTDMPolicyURL))
	body, err := json.MarshalIndent([]tdmEntry{entry}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

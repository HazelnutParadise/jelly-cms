package server

import (
	"context"
	"log"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
)

// RerenderStalePosts upgrades legacy rendered bodies in bounded batches.
func RerenderStalePosts(ctx context.Context) {
	if data.DB == nil {
		return
	}
	db := data.DB.WithContext(ctx)
	var last uint
	for {
		if ctx.Err() != nil {
			return
		}
		var rows []core.Post
		if err := db.Where("id > ? AND render_version < ?", last, 1).Order("id").Limit(100).Find(&rows).Error; err != nil {
			log.Printf("editor rerender scan: %v", err)
			return
		}
		if len(rows) == 0 {
			return
		}
		widgets := map[string]markup.WidgetSpec{}
		needsWidgets := false
		for _, p := range rows {
			if p.Format == "jmd" || p.Format == "md" {
				needsWidgets = true
				break
			}
		}
		if needsWidgets {
			var err error
			widgets, err = publishedWidgetSpecs(db)
			if err != nil {
				log.Printf("editor rerender widgets: %v", err)
				return
			}
		}
		for _, p := range rows {
			last = p.ID
			trust := p.RenderTrust
			if trust != "rich" {
				trust = markup.TrustStrict
			}
			content := p.Content
			if p.Format == "jmd" || p.Format == "md" {
				format := p.Format
				r, e := markup.Render(ctx, p.Source, markup.Options{Trust: trust, Format: format, Widgets: widgets})
				if e != nil {
					log.Printf("editor rerender post %d: %v", p.ID, e)
					continue
				}
				renderFailed := false
				for _, diagnostic := range r.Diagnostics {
					if diagnostic.Severity == "error" {
						log.Printf("editor rerender post %d: render diagnostic %s", p.ID, diagnostic.Code)
						renderFailed = true
						break
					}
				}
				if renderFailed {
					continue
				}
				content = r.HTML
			} else {
				content = markup.Policy(trust).Sanitize(content)
			}
			if e := db.Model(&core.Post{}).Where("id = ? AND render_version < ? AND updated_at = ? AND COALESCE(source, '') = ? AND COALESCE(format, '') = ?", p.ID, 1, p.UpdatedAt, p.Source, p.Format).UpdateColumns(map[string]any{"content": content, "render_version": 1, "render_trust": trust}).Error; e != nil {
				log.Printf("editor rerender write post %d: %v", p.ID, e)
			}
		}
		time.Sleep(time.Millisecond)
	}
}

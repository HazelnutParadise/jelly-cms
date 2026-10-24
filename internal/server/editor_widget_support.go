package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"gorm.io/gorm"
)

const widgetThemeBootstrap = `<script>(function(){
var allowed=['--primary','--secondary','--accent','--warning','--ink','--muted','--surface','--surface-low','--surface-mid','--outline','--color-ink','--color-surface'];
window.addEventListener('message',function(event){
if(event.source!==window.parent)return;
var message=event.data;
if(!message||message.type!=='jelly-widget-theme'||!message.colors||typeof message.colors!=='object'||Array.isArray(message.colors))return;
allowed.forEach(function(name){
if(!Object.prototype.hasOwnProperty.call(message.colors,name))return;
var value=message.colors[name];
if(typeof value!=='string')return;
value=value.trim();
if(value.length<1||value.length>128||!CSS.supports('color',value))return;
document.documentElement.style.setProperty(name,value);
});
});
})();</script>`

// widgetUsageCounts counts distinct active Jelly Markdown references per
// widget while reading post sources in one bounded, streaming query.
func widgetUsageCounts(ctx context.Context, db *gorm.DB, idsByName map[string]uint) (counts map[uint]int64, err error) {
	counts = make(map[uint]int64)
	if len(idsByName) == 0 {
		return counts, nil
	}
	rows, err := db.Model(&core.Post{}).Where("type IN ?", []string{"post", "page"}).Select("COALESCE(source, ''), COALESCE(format, '')").Rows()
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			counts = nil
		}
	}()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var source, format string
		if err := rows.Scan(&source, &format); err != nil {
			return nil, err
		}
		if format != "jmd" {
			continue
		}
		if !strings.Contains(source, "::widget") {
			continue
		}
		result, err := markup.Render(ctx, source, markup.Options{Trust: markup.TrustStrict, Format: "jmd"})
		if err != nil {
			return nil, fmt.Errorf("render widget usage source: %w", err)
		}
		used := make(map[uint]struct{})
		for _, block := range result.Blocks {
			if block.Kind != "directive" || block.Name != "widget" {
				continue
			}
			if id, ok := idsByName[strings.ToLower(block.Attrs["name"])]; ok {
				used[id] = struct{}{}
			}
		}
		for id := range used {
			counts[id]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return counts, nil
}

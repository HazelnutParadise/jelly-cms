package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/labstack/echo/v4"
	"golang.org/x/net/html"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func widgetRequestAudit(action string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			err := next(c)
			status := c.Response().Status
			if status == 0 {
				status = http.StatusOK
			}
			if he, ok := err.(*echo.HTTPError); ok {
				status = he.Code
			}
			userID := uint(0)
			if u, ok := c.Get("user").(*core.User); ok {
				userID = u.ID
			}
			record, _ := json.Marshal(map[string]any{"event": "widget", "action": action, "status": status, "actor_id": userID, "request_id": c.Response().Header().Get(echo.HeaderXRequestID), "path": c.Request().URL.Path, "ip": c.RealIP(), "user_agent": c.Request().UserAgent()})
			log.Print(string(record))
			return err
		}
	}
}

func setWidgetActor(tx *gorm.DB, c echo.Context) error {
	u, err := currentUser(c)
	if err != nil {
		return err
	}
	return tx.Exec("SELECT set_config('jelly.actor_id', ?, true)", strconv.FormatUint(uint64(u.ID), 10)).Error
}

// publishedWidgetSpecs returns only immutable versions available to renderers.
func publishedWidgetSpecs(db *gorm.DB) (map[string]markup.WidgetSpec, error) {
	var versions []core.WidgetVersion
	if err := db.Where("published = ?", true).Order("version DESC").Find(&versions).Error; err != nil {
		return nil, err
	}
	var widgets []core.Widget
	if err := db.Find(&widgets).Error; err != nil {
		return nil, err
	}
	names := make(map[uint]string, len(widgets))
	for _, w := range widgets {
		names[w.ID] = w.Name
	}
	out := make(map[string]markup.WidgetSpec, len(versions))
	for _, v := range versions {
		name, ok := names[v.WidgetID]
		if !ok {
			continue
		}
		var params []widgetParam
		if err := json.Unmarshal([]byte(v.Params), &params); err != nil {
			return nil, fmt.Errorf("decode published widget %q: %w", name, err)
		}
		spec := markup.WidgetSpec{Version: v.Version, Params: make(map[string]markup.WidgetParamSpec, len(params))}
		for _, p := range params {
			spec.Params[p.Name] = markup.WidgetParamSpec{Type: p.Type, Required: p.Required, Default: p.Default, Values: p.Values, MaxLength: p.MaxLength}
		}
		if current, ok := out[name]; !ok || spec.Version > current.Version {
			out[name] = spec
		}
	}
	return out, nil
}

type widgetParam struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Required  bool     `json:"required"`
	Default   any      `json:"default"`
	Values    []string `json:"values"`
	MaxLength int      `json:"maxLength,omitempty"`
}
type widgetWrite struct {
	Name   string        `json:"name"`
	Title  string        `json:"title"`
	Source string        `json:"source"`
	Params []widgetParam `json:"params"`
}

var errWidgetPublishedImmutable = errors.New("published widget versions are immutable")

func checkWidget(src string, ps []widgetParam) []string {
	var reasons []string
	if len(src) > 64<<10 {
		reasons = append(reasons, "source exceeds 64 KiB")
	}
	low := strings.ToLower(src)
	for _, x := range []string{"eval(", "new function"} {
		if strings.Contains(low, x) {
			reasons = append(reasons, "unsafe source: "+x)
		}
	}
	if root, e := html.Parse(strings.NewReader(src)); e == nil {
		var walk func(*html.Node)
		walk = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "script" {
				for _, a := range n.Attr {
					if a.Key == "src" {
						reasons = append(reasons, "external scripts are forbidden")
					}
				}
			}
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				walk(ch)
			}
		}
		walk(root)
	}
	if strings.Contains(low, "http://") || strings.Contains(low, "https://") {
		reasons = append(reasons, "external resources are forbidden")
	}
	declared := map[string]bool{}
	paramNameRE := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	for _, p := range ps {
		if !paramNameRE.MatchString(p.Name) || p.Name == "__proto__" || p.Name == "constructor" || p.Name == "name" || p.Name == "version" || p.Name == "v" || declared[p.Name] {
			reasons = append(reasons, "invalid or duplicate parameter: "+p.Name)
		}
		if p.Type != "string" && p.Type != "number" && p.Type != "boolean" && p.Type != "date" && p.Type != "datetime" && p.Type != "enum" {
			reasons = append(reasons, "invalid parameter type: "+p.Type)
		}
		if p.Type == "enum" && len(p.Values) == 0 {
			reasons = append(reasons, "enum parameter requires values: "+p.Name)
		}
		if p.Type != "enum" && len(p.Values) > 0 {
			reasons = append(reasons, "values only allowed for enum: "+p.Name)
		}
		if p.Type == "enum" && p.Default != nil {
			valid := false
			for _, v := range p.Values {
				if fmt.Sprint(p.Default) == v {
					valid = true
				}
			}
			if !valid {
				reasons = append(reasons, "default is outside enum values: "+p.Name)
			}
		}
		if p.MaxLength < 0 || p.MaxLength > 2048 {
			reasons = append(reasons, "invalid maximum length: "+p.Name)
		}
		if p.Default != nil {
			one := markup.WidgetSpec{Params: map[string]markup.WidgetParamSpec{p.Name: {Type: p.Type, Default: p.Default, Values: p.Values, MaxLength: p.MaxLength}}}
			if _, err := markup.ValidateWidgetParams(one, nil); err != nil {
				reasons = append(reasons, "invalid default: "+p.Name)
			}
		}
		declared[p.Name] = true
	}
	re := regexp.MustCompile(`props(?:\.([A-Za-z_$][A-Za-z0-9_$]*)|\[\s*["']([A-Za-z][A-Za-z0-9_-]{0,63})["']\s*\])`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if !declared[name] {
			reasons = append(reasons, "undeclared parameter: "+name)
			break
		}
	}
	return reasons
}
func widgetReply(w core.Widget) map[string]any {
	return map[string]any{"id": w.ID, "name": w.Name, "title": w.Title, "version": w.Version, "status": w.Status}
}
func widgetList(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	db := data.DB.WithContext(ctx)
	var ws []core.Widget
	if err := db.Find(&ws).Error; err != nil {
		return c.JSON(500, map[string]string{"error": "Unable to load widgets"})
	}
	ids := make([]uint, 0, len(ws))
	idsByName := make(map[string]uint, len(ws))
	for _, w := range ws {
		ids = append(ids, w.ID)
		idsByName[w.Name] = w.ID
	}
	var versions []core.WidgetVersion
	if len(ids) > 0 {
		if err := db.Where("widget_id IN ?", ids).Order("version DESC").Find(&versions).Error; err != nil {
			return c.JSON(500, map[string]string{"error": "Unable to load widgets"})
		}
	}
	uses, err := widgetUsageCounts(ctx, db, idsByName)
	if err != nil {
		return c.JSON(500, map[string]string{"error": "Unable to load widgets"})
	}
	byWidget := map[uint][]core.WidgetVersion{}
	latest := map[uint]core.WidgetVersion{}
	for _, v := range versions {
		byWidget[v.WidgetID] = append(byWidget[v.WidgetID], v)
		if v.Version == 0 {
			continue
		}
		if _, exists := latest[v.WidgetID]; !exists {
			latest[v.WidgetID] = v
		}
	}
	out := make([]map[string]any, 0, len(ws))
	for _, w := range ws {
		m := widgetReply(w)
		m["uses"] = uses[w.ID]
		if v, ok := latest[w.ID]; ok {
			var params []widgetParam
			_ = json.Unmarshal([]byte(v.Params), &params)
			m["source"] = v.Source
			m["params"] = params
		}
		m["versions"] = byWidget[w.ID]
		out = append(out, m)
	}
	if err := ctx.Err(); err != nil {
		return c.JSON(500, map[string]string{"error": "Unable to load widgets"})
	}
	return c.JSON(200, out)
}
func createWidget(c echo.Context) error {
	var in widgetWrite
	if c.Bind(&in) != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request"})
	}
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`).MatchString(in.Name) {
		return c.JSON(400, map[string]string{"error": "Invalid name"})
	}
	reasons := checkWidget(in.Source, in.Params)
	if len(reasons) > 0 {
		return c.JSON(400, map[string]any{"error": "widget check failed", "reasons": reasons})
	}
	p, _ := json.Marshal(in.Params)
	var w core.Widget
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		if err := setWidgetActor(tx, c); err != nil {
			return err
		}
		w = core.Widget{Name: in.Name, Title: in.Title, Version: 1, Status: "draft"}
		if e := tx.Create(&w).Error; e != nil {
			return e
		}
		return tx.Create(&core.WidgetVersion{WidgetID: w.ID, Version: 1, Source: in.Source, Params: string(p)}).Error
	})
	if err != nil {
		return c.JSON(400, map[string]string{"error": "Unable to create widget"})
	}
	return c.JSON(200, widgetReply(w))
}
func updateWidget(c echo.Context) error {
	id, e := strconv.ParseUint(c.Param("id"), 10, 64)
	if e != nil {
		return c.JSON(404, map[string]string{"error": "Widget not found"})
	}
	var in widgetWrite
	if c.Bind(&in) != nil {
		return c.JSON(400, map[string]string{"error": "Invalid request"})
	}
	reasons := checkWidget(in.Source, in.Params)
	if len(reasons) > 0 {
		return c.JSON(400, map[string]any{"error": "widget check failed", "reasons": reasons})
	}
	var w core.Widget
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		if e := setWidgetActor(tx, c); e != nil {
			return e
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&w, uint(id)).Error; e != nil {
			return e
		}
		v := w.Version + 1
		p, _ := json.Marshal(in.Params)
		if e := tx.Create(&core.WidgetVersion{WidgetID: w.ID, Version: v, Source: in.Source, Params: string(p)}).Error; e != nil {
			return e
		}
		w.Version, w.Title, w.Status, w.UpdatedAt = v, in.Title, "draft", time.Now()
		return tx.Save(&w).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(404, map[string]string{"error": "Widget not found"})
		}
		return c.JSON(500, map[string]string{"error": "Unable to update widget"})
	}
	return c.JSON(200, widgetReply(w))
}
func deleteWidget(c echo.Context) error {
	id, e := strconv.ParseUint(c.Param("id"), 10, 64)
	if e != nil || id == 0 {
		return c.JSON(404, map[string]string{"error": "Widget not found"})
	}
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		if err := setWidgetActor(tx, c); err != nil {
			return err
		}
		var w core.Widget
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&w, uint(id)).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&core.WidgetVersion{}).Where("widget_id = ? AND published = true", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errWidgetPublishedImmutable
		}
		return tx.Delete(&w).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(404, map[string]string{"error": "Widget not found"})
	}
	if err != nil {
		if errors.Is(err, errWidgetPublishedImmutable) {
			return c.JSON(409, map[string]string{"error": "Published widget versions are immutable"})
		}
		return c.JSON(500, map[string]string{"error": "Unable to delete widget"})
	}
	return c.NoContent(204)
}
func publishWidget(c echo.Context) error {
	id, e := strconv.ParseUint(c.Param("id"), 10, 64)
	if e != nil {
		return c.JSON(404, map[string]string{"error": "Widget not found"})
	}
	var w core.Widget
	err := data.DB.Transaction(func(tx *gorm.DB) error {
		if e := setWidgetActor(tx, c); e != nil {
			return e
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&w, uint(id)).Error; e != nil {
			return e
		}
		var version core.WidgetVersion
		if err := tx.Where("widget_id = ? AND version = ?", w.ID, w.Version).First(&version).Error; err != nil {
			return err
		}
		if version.Published {
			if w.Status != "published" {
				w.Status = "published"
				return tx.Save(&w).Error
			}
			return nil
		}
		result := tx.Model(&core.WidgetVersion{}).Where("id = ? AND published = false", version.ID).Update("published", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		w.Status = "published"
		return tx.Save(&w).Error
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(404, map[string]string{"error": "Widget not found"})
		}
		return c.JSON(500, map[string]string{"error": "Unable to publish widget"})
	}
	return c.JSON(200, widgetReply(w))
}
func widgetFrame(c echo.Context) error {
	v, e := strconv.Atoi(c.Param("v"))
	if e != nil || v < 1 {
		return echo.ErrNotFound
	}
	var w core.Widget
	// Deleted widgets disappear from the insert registry, but existing published
	// posts still reference their immutable versioned frames.
	err := data.DB.Unscoped().Where("name = ?", c.Param("name")).First(&w).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(500, map[string]string{"error": "unable to load widget"})
	}
	if err == nil {
		var ver core.WidgetVersion
		e := data.DB.Where("widget_id = ? AND version = ? AND published = true", w.ID, v).First(&ver).Error
		if e == nil {
			return serveWidgetFrame(c, ver)
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return c.JSON(500, map[string]string{"error": "unable to load widget version"})
		}
	}
	if strings.EqualFold(c.Param("name"), "countdown") {
		if builtin, ok := builtinWidgetVersion(c.Param("name"), v); ok {
			return serveBuiltinCountdown(c, builtin)
		}
	}
	if builtin, ok := builtinWidgetVersion(c.Param("name"), v); ok {
		return serveWidgetFrame(c, builtin)
	}
	return echo.ErrNotFound
}

func builtinWidgetVersion(name string, version int) (core.WidgetVersion, bool) {
	if version != 1 {
		return core.WidgetVersion{}, false
	}
	var ps []widgetParam
	var src string
	switch name {
	case "countdown":
		ps = []widgetParam{{Name: "to", Type: "datetime", Required: true}}
		src = `<style>:root{color:var(--color-ink,#222);background:var(--color-surface,#fff)}body{font:inherit;color:inherit;background:transparent;margin:0;padding:1rem}.count{font-size:1.35rem;text-align:center}@media(prefers-reduced-motion:reduce){*{animation:none!important;transition:none!important}}</style><div class="count" id="countdown" role="timer"><time datetime="__JELLY_COUNTDOWN_TO__">__JELLY_COUNTDOWN_TO__</time></div><script>const el=document.getElementById("countdown");const deadline=document.querySelector("time");function update(){const d=Date.parse(deadline.dateTime)-Date.now();el.textContent=d<=0?"__JELLY_COUNTDOWN_DONE__":"__JELLY_COUNTDOWN_REMAINING__"+Math.floor(d/86400000)+"__JELLY_COUNTDOWN_DAYS__"+Math.floor(d%86400000/3600000)+"__JELLY_COUNTDOWN_HOURS__"+Math.floor(d%3600000/60000)+"__JELLY_COUNTDOWN_MINUTES__"}update();setInterval(update,60000);</script>`
	default:
		return core.WidgetVersion{}, false
	}
	raw, _ := json.Marshal(ps)
	return core.WidgetVersion{Version: version, Source: src, Params: string(raw), Published: true}, true
}

func serveBuiltinCountdown(c echo.Context, ver core.WidgetVersion) error {
	return serveWidgetFrameWithMode(c, ver, true)
}

func serveWidgetFrame(c echo.Context, ver core.WidgetVersion) error {
	return serveWidgetFrameWithMode(c, ver, false)
}

func serveWidgetFrameWithMode(c echo.Context, ver core.WidgetVersion, builtin bool) error {
	body, err := buildWidgetDocument(ver, c.QueryParams(), fmt.Sprint(c.Get("lang")), builtin)
	if errors.Is(err, errWidgetDocumentDefinition) {
		return echo.ErrNotFound
	}
	if errors.Is(err, errWidgetDocumentParams) {
		return c.JSON(400, map[string]string{"error": "invalid parameter"})
	}
	if err != nil {
		return c.JSON(500, map[string]string{"error": "unable to build widget frame"})
	}
	c.Response().Header().Set("Content-Security-Policy", widgetFrameCSP)
	c.Response().Header().Set(echo.HeaderContentType, "text/html; charset=utf-8")
	return c.HTML(http.StatusOK, body)
}

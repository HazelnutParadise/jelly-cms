package server

import (
	"bytes"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/goccy/go-json"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Revisions (slot C4b). Same visibility rules as the post itself (authors: own posts only).
//
//	GET  /api/admin/posts/:id/revisions                 → [{id, kind, user:{id,username}, created_at, added, removed}] newest first
//	GET  /api/admin/posts/:id/revisions/:rid            → full revision (title, content, excerpt, meta)
//	GET  /api/admin/posts/:id/revisions/diff?from=&to=  → {title:[DiffOp], content:[DiffOp]}; "to" may be "current"
//	POST /api/admin/posts/:id/revisions/:rid/restore    → 200 post; records a new "restore" revision first
//
// Recording (in handlers_posts.go, via RecordRevision):
//   - createPost: kind "manual" (or "publish" when status is published)
//   - updatePost: when title, content, excerpt or meta changed: kind "autosave" if input has "autosave": true,
//     "publish" if status moved to published, else "manual". Unchanged saves record nothing.
//   - keep only the newest core.RevisionAutosaveKeep autosaves per post; manual/publish/restore are kept forever.
//   - Added/Removed count changed lines of Content versus the previous revision; the first revision
//     counts every content line as added and 0 removed.
//
// Admin page: web/admin/revisions.html per design/project/Revisions.dc.html (left list with avatars and +n −n,
// right side 逐行比較 / 只看 vN / 左右並排 segments, red/green inline diff, 還原到這個版本).

// DiffOp is one line of a diff: Op is "=", "+" or "-".
type DiffOp struct {
	Op   string `json:"op"`
	Text string `json:"text"`
}

func registerRevisionRoutes(content *echo.Group) {
	content.GET("/posts/:id/revisions", listRevisions)
	content.GET("/posts/:id/revisions/diff", diffRevisions)
	content.GET("/posts/:id/revisions/:rid", getRevision)
	content.POST("/posts/:id/revisions/:rid/restore", restoreRevision)
}

// diffMaxCells bounds lines(a)*lines(b) for LineDiff; above it the diff is all -/+.
const diffMaxCells = 20000 * 20000

// RecordRevision stores a snapshot of post for user with kind and trims old
// autosaves. It returns the stored revision.
func RecordRevision(db *gorm.DB, post core.Post, userID uint, kind string) (*core.Revision, error) {
	var previous core.Revision
	var added, removed int
	err := db.Where("post_id = ?", post.ID).Order("id DESC").First(&previous).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		added = len(splitDiffLines(post.Content))
	case err != nil:
		return nil, err
	default:
		for _, op := range LineDiff(previous.Content, post.Content) {
			switch op.Op {
			case "+":
				added++
			case "-":
				removed++
			}
		}
	}

	rev := core.Revision{
		PostID:  post.ID,
		UserID:  userID,
		Kind:    kind,
		Title:   post.Title,
		Content: post.Content,
		Source:  post.Source,
		Format:  post.Format,
		Excerpt: post.Excerpt,
		Meta:    post.Meta,
		Added:   added,
		Removed: removed,
	}
	if err := db.Omit("User").Create(&rev).Error; err != nil {
		return nil, err
	}
	if kind == "autosave" {
		var stale []uint
		if err := db.Model(&core.Revision{}).
			Where("post_id = ? AND kind = ?", post.ID, "autosave").
			Order("id DESC").Offset(core.RevisionAutosaveKeep).
			Pluck("id", &stale).Error; err != nil {
			return nil, err
		}
		if len(stale) > 0 {
			if err := db.Where("id IN ?", stale).Delete(&core.Revision{}).Error; err != nil {
				return nil, err
			}
		}
	}
	return &rev, nil
}

// revisionKindFor decides whether an update is recorded and as which kind. before is the stored
// post, after is the post with the request applied.
func revisionKindFor(before, after core.Post, input map[string]json.RawMessage) (string, bool) {
	changed := before.Title != after.Title || before.Content != after.Content || before.Source != after.Source || before.Format != after.Format ||
		before.Excerpt != after.Excerpt || !sameMeta(before.Meta, after.Meta)
	if !changed {
		return "", false
	}
	if raw, ok := input["autosave"]; ok {
		var autosave bool
		if json.Unmarshal(raw, &autosave) == nil && autosave {
			return "autosave", true
		}
	}
	if before.Status != "published" && after.Status == "published" {
		return "publish", true
	}
	return "manual", true
}

// sameMeta compares two JSON documents by value, so jsonb's reformatting does not count as a change.
func sameMeta(a, b json.RawMessage) bool {
	if len(a) == 0 {
		a = json.RawMessage("null")
	}
	if len(b) == 0 {
		b = json.RawMessage("null")
	}
	if bytes.Equal(a, b) {
		return true
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// LineDiff returns a line-based diff of a against b (LCS based; good enough for
// prose, never quadratic blow-up beyond 20k lines: fall back to all -/+ then).
//
// The ops reproduce both inputs: "=" and "-" read back as a, "=" and "+" as b.
// Within a gap between matched lines, removals come before additions.
func LineDiff(a, b string) []DiffOp {
	oldLines, newLines := splitDiffLines(a), splitDiffLines(b)
	ops := make([]DiffOp, 0, len(oldLines)+len(newLines))
	if len(oldLines)*len(newLines) > diffMaxCells {
		for _, line := range oldLines {
			ops = append(ops, DiffOp{Op: "-", Text: line})
		}
		for _, line := range newLines {
			ops = append(ops, DiffOp{Op: "+", Text: line})
		}
		return ops
	}

	// Intern lines so the LCS compares integers.
	ids := make(map[string]int32)
	intern := func(lines []string) []int32 {
		out := make([]int32, len(lines))
		for i, line := range lines {
			id, ok := ids[line]
			if !ok {
				id = int32(len(ids))
				ids[line] = id
			}
			out[i] = id
		}
		return out
	}
	x, y := intern(oldLines), intern(newLines)
	var matches []lineMatch
	lcsMatches(x, y, 0, 0, &matches)

	oi, yi := 0, 0
	for _, m := range matches {
		for ; oi < m.a; oi++ {
			ops = append(ops, DiffOp{Op: "-", Text: oldLines[oi]})
		}
		for ; yi < m.b; yi++ {
			ops = append(ops, DiffOp{Op: "+", Text: newLines[yi]})
		}
		ops = append(ops, DiffOp{Op: "=", Text: oldLines[m.a]})
		oi, yi = m.a+1, m.b+1
	}
	for ; oi < len(oldLines); oi++ {
		ops = append(ops, DiffOp{Op: "-", Text: oldLines[oi]})
	}
	for ; yi < len(newLines); yi++ {
		ops = append(ops, DiffOp{Op: "+", Text: newLines[yi]})
	}
	return ops
}

// splitDiffLines splits s into lines; the empty string has no lines.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

type lineMatch struct{ a, b int }

// lcsMatches appends the matched index pairs of one LCS of a and b to out, with
// offsets added. It is Hirschberg's divide and conquer: O(len(a)*len(b)) time and
// O(len(b)) memory, so large inputs never allocate the full DP table.
func lcsMatches(a, b []int32, aOff, bOff int, out *[]lineMatch) {
	if len(a) == 0 || len(b) == 0 {
		return
	}
	if len(a) == 1 {
		for j, v := range b {
			if v == a[0] {
				*out = append(*out, lineMatch{aOff, bOff + j})
				return
			}
		}
		return
	}
	mid := len(a) / 2
	forward := lcsRow(a[:mid], b)
	backward := lcsRow(reversedIDs(a[mid:]), reversedIDs(b))
	split, best := 0, -1
	for j := 0; j <= len(b); j++ {
		if s := forward[j] + backward[len(b)-j]; s > best {
			best, split = s, j
		}
	}
	lcsMatches(a[:mid], b[:split], aOff, bOff, out)
	lcsMatches(a[mid:], b[split:], aOff+mid, bOff+split, out)
}

// lcsRow returns row where row[j] is the LCS length of a and b[:j].
func lcsRow(a, b []int32) []int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for _, av := range a {
		cur[0] = 0
		for j, bv := range b {
			switch {
			case av == bv:
				cur[j+1] = prev[j] + 1
			case prev[j+1] >= cur[j]:
				cur[j+1] = prev[j+1]
			default:
				cur[j+1] = cur[j]
			}
		}
		prev, cur = cur, prev
	}
	return prev
}

func reversedIDs(s []int32) []int32 {
	out := make([]int32, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

type revisionUserView struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
}

type revisionSummary struct {
	ID        uint             `json:"id"`
	Kind      string           `json:"kind"`
	User      revisionUserView `json:"user"`
	CreatedAt time.Time        `json:"created_at"`
	Added     int              `json:"added"`
	Removed   int              `json:"removed"`
}

type revisionDetail struct {
	ID        uint             `json:"id"`
	Kind      string           `json:"kind"`
	User      revisionUserView `json:"user"`
	CreatedAt time.Time        `json:"created_at"`
	Added     int              `json:"added"`
	Removed   int              `json:"removed"`
	Title     string           `json:"title"`
	Content   string           `json:"content"`
	Excerpt   string           `json:"excerpt"`
	Meta      json.RawMessage  `json:"meta"`
	Source    string           `json:"source"`
	Format    string           `json:"format"`
}

func revisionUserOf(rev core.Revision) revisionUserView {
	return revisionUserView{ID: rev.UserID, Username: rev.User.Username}
}

// loadVisiblePost returns the post when the caller may see it, otherwise gorm.ErrRecordNotFound.
func loadVisiblePost(user *core.User, id uint) (core.Post, error) {
	var post core.Post
	err := visiblePostQuery(data.DB, user, id).First(&post).Error
	return post, err
}

func visiblePostQuery(db *gorm.DB, user *core.User, id uint) *gorm.DB {
	query := db.Where("id = ?", id)
	if user.Role == "author" {
		query = query.Where("author_id = ? AND type = ?", user.ID, "post")
	}
	return query
}

// loadRevisionOf returns revision rid of post postID, or gorm.ErrRecordNotFound.
func loadRevisionOf(db *gorm.DB, postID, rid uint) (core.Revision, error) {
	var rev core.Revision
	err := db.Where("id = ? AND post_id = ?", rid, postID).First(&rev).Error
	return rev, err
}

func revisionQueryError(c echo.Context, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Revision not found"})
	}
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load revision"})
}

func listRevisions(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	if _, err := loadVisiblePost(user, id); err != nil {
		return postQueryError(c, err)
	}
	var revs []core.Revision
	if err := data.DB.Preload("User").Where("post_id = ?", id).Order("id DESC").Find(&revs).Error; err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to load revisions"})
	}
	items := make([]revisionSummary, 0, len(revs))
	for _, rev := range revs {
		items = append(items, revisionSummary{
			ID: rev.ID, Kind: rev.Kind, User: revisionUserOf(rev), CreatedAt: rev.CreatedAt,
			Added: rev.Added, Removed: rev.Removed,
		})
	}
	return c.JSON(http.StatusOK, items)
}

func getRevision(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	rid, ok := parseTermID(c.Param("rid"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Revision not found"})
	}
	if _, err := loadVisiblePost(user, id); err != nil {
		return postQueryError(c, err)
	}
	rev, err := loadRevisionOf(data.DB.Preload("User"), id, rid)
	if err != nil {
		return revisionQueryError(c, err)
	}
	return c.JSON(http.StatusOK, revisionDetail{
		ID: rev.ID, Kind: rev.Kind, User: revisionUserOf(rev), CreatedAt: rev.CreatedAt,
		Added: rev.Added, Removed: rev.Removed,
		Title: rev.Title, Content: rev.Content, Excerpt: rev.Excerpt, Meta: rev.Meta, Source: rev.Source, Format: rev.Format,
	})
}

// diffTarget is the text a diff compares: a revision or the live post.
type diffTarget struct {
	Title   string
	Content string
}

func diffRevisions(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	post, err := loadVisiblePost(user, id)
	if err != nil {
		return postQueryError(c, err)
	}
	fromID, ok := parseTermID(c.QueryParam("from"))
	if !ok {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "from must be a revision id"})
	}
	fromRev, err := loadRevisionOf(data.DB, id, fromID)
	if err != nil {
		return revisionQueryError(c, err)
	}
	from := diffTarget{Title: fromRev.Title, Content: fromRev.Content}
	fromFormat := fromRev.Format
	to := diffTarget{Title: post.Title, Content: post.Content}
	toFormat := post.Format
	toSource := post.Source
	if raw := c.QueryParam("to"); raw != "" && raw != "current" {
		toID, ok := parseTermID(raw)
		if !ok {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "to must be a revision id or current"})
		}
		toRev, err := loadRevisionOf(data.DB, id, toID)
		if err != nil {
			return revisionQueryError(c, err)
		}
		to = diffTarget{Title: toRev.Title, Content: toRev.Content}
		toFormat = toRev.Format
		toSource = toRev.Source
	}
	basis := "content"
	if fromFormat == toFormat && (fromFormat == "jmd" || fromFormat == "md") {
		basis = "source"
		from.Content = fromRev.Source
		to.Content = toSource
	}
	return c.JSON(http.StatusOK, map[string]any{
		"basis":   basis,
		"title":   LineDiff(from.Title, to.Title),
		"content": LineDiff(from.Content, to.Content),
	})
}

func restoreRevision(c echo.Context) error {
	user, err := currentUser(c)
	if err != nil {
		return err
	}
	id, ok := parseTermID(c.Param("id"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	rid, ok := parseTermID(c.Param("rid"))
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Revision not found"})
	}
	if _, err := loadVisiblePost(user, id); err != nil {
		return postQueryError(c, err)
	}
	snapshot, err := loadRevisionOf(data.DB, id, rid)
	if err != nil {
		return revisionQueryError(c, err)
	}
	format := snapshot.Format
	if format == "" {
		format = "html"
	}
	if format != "html" && format != "md" && format != "jmd" {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid format", "diagnostics": []markup.Diagnostic{}})
	}
	var renderDiagnostics []markup.Diagnostic
	err = data.DB.Transaction(func(tx *gorm.DB) error {
		var current core.Post
		if err := visiblePostQuery(tx, user, id).First(&current).Error; err != nil {
			return err
		}
		if _, err := RecordRevision(tx, current, user.ID, "restore"); err != nil {
			return err
		}
		restored := current
		restored.Title, restored.Source, restored.Format = snapshot.Title, snapshot.Source, format
		restored.Excerpt, restored.Meta = snapshot.Excerpt, snapshot.Meta
		if format == "jmd" || format == "md" {
			widgets, err := publishedWidgetSpecs(tx)
			if err != nil {
				return err
			}
			result, err := markup.Render(c.Request().Context(), restored.Source, markup.Options{Trust: renderTrustFor(user), Format: restored.Format, Widgets: widgets})
			if err != nil {
				return err
			}
			for _, diagnostic := range result.Diagnostics {
				if diagnostic.Severity == "error" {
					renderDiagnostics = result.Diagnostics
					return errRenderDiagnostics
				}
			}
			restored.Content, restored.RenderTrust, restored.RenderVersion = result.HTML, renderTrustFor(user), 1
			restored.Meta = writeLegacyMarkdown(restored.Meta, restored.Source)
		} else {
			restored.Content = markup.Policy(renderTrustFor(user)).Sanitize(snapshot.Content)
			restored.Source = ""
			restored.RenderTrust, restored.RenderVersion = renderTrustFor(user), 1
		}
		result := visiblePostQuery(tx.Model(&core.Post{}), user, id).Updates(map[string]any{
			"title": restored.Title, "content": restored.Content, "source": restored.Source, "format": restored.Format,
			"render_trust": restored.RenderTrust, "render_version": restored.RenderVersion,
			"excerpt": restored.Excerpt, "meta": restored.Meta,
			"updated_at": time.Now(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "Post not found"})
	}
	if errors.Is(err, errRenderDiagnostics) {
		return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error(), "diagnostics": renderDiagnostics})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Unable to restore revision"})
	}
	var restored core.Post
	if err := visiblePostQuery(data.DB, user, id).Preload("Categories").Preload("Tags").First(&restored).Error; err != nil {
		return postQueryError(c, err)
	}
	return c.JSON(http.StatusOK, restored)
}

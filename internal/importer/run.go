package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/markup"
	"github.com/HazelnutParadise/jelly-cms/internal/redirects"
	"github.com/microcosm-cc/bluemonday"
	"gorm.io/gorm"
)

// Host supplies the server pieces an import needs and this package must not
// import: the public path of a post under the current permalinks, the
// reserved page slugs, and the author invitation.
type Host struct {
	// Actor is the admin who started the import. Posts whose author cannot be
	// mapped are assigned to them.
	Actor uint
	// Reserved reports whether a page slug belongs to the application.
	Reserved func(ctx context.Context, slug string) bool
	// PathFor returns the public path of a saved post or page.
	PathFor func(post core.Post) string
	// Author returns the local user for an imported author. An existing user
	// with the same email is reused. Otherwise an invited user is created.
	Author func(ctx context.Context, email, role, note string) (uint, error)
}

// JobRow is one import job. Summary and Authors come from the upload, and
// Report from the latest dry run or run.
type JobRow struct {
	ID          int64
	State       string
	SourceName  string
	SourceBytes int64
	SiteURL     string
	Kind        string
	Mapping     Mapping
	Summary     Summary
	Authors     []Author
	Report      *Report
	LastError   string
	CreatedAt   time.Time
	FinishedAt  *time.Time
}

// storedStats is the JSON kept in import_jobs.stats.
type storedStats struct {
	Summary Summary  `json:"summary"`
	Authors []Author `json:"authors"`
	Report  *Report  `json:"report,omitempty"`
}

type jobRecord struct {
	ID          int64      `gorm:"column:id"`
	State       string     `gorm:"column:state"`
	SourceName  string     `gorm:"column:source_name"`
	SourceBytes int64      `gorm:"column:source_bytes"`
	SiteURL     string     `gorm:"column:site_url"`
	Options     string     `gorm:"column:options"`
	Mapping     string     `gorm:"column:mapping"`
	Stats       string     `gorm:"column:stats"`
	LastError   *string    `gorm:"column:last_error"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	FinishedAt  *time.Time `gorm:"column:finished_at"`
}

const jobColumns = `id, state, source_name, source_bytes, site_url, options::text AS options,
	mapping::text AS mapping, stats::text AS stats, last_error, created_at, finished_at`

func (rec jobRecord) toJob() (JobRow, error) {
	job := JobRow{
		ID: rec.ID, State: rec.State, SourceName: rec.SourceName, SourceBytes: rec.SourceBytes,
		SiteURL: rec.SiteURL, CreatedAt: rec.CreatedAt, FinishedAt: rec.FinishedAt,
		Mapping: defaultMapping(),
	}
	if rec.LastError != nil {
		job.LastError = *rec.LastError
	}
	var opts struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(rec.Options), &opts); err != nil {
		return JobRow{}, err
	}
	job.Kind = opts.Kind
	if strings.TrimSpace(rec.Mapping) != "" && strings.TrimSpace(rec.Mapping) != "{}" {
		var m Mapping
		if err := json.Unmarshal([]byte(rec.Mapping), &m); err != nil {
			return JobRow{}, err
		}
		if m.AuthorRoles == nil {
			m.AuthorRoles = map[string]string{}
		}
		job.Mapping = m
	}
	var st storedStats
	if strings.TrimSpace(rec.Stats) != "" {
		if err := json.Unmarshal([]byte(rec.Stats), &st); err != nil {
			return JobRow{}, err
		}
	}
	job.Summary = st.Summary
	job.Authors = st.Authors
	if job.Authors == nil {
		job.Authors = []Author{}
	}
	job.Report = st.Report
	return job, nil
}

// LoadJob returns import id.
func LoadJob(db *gorm.DB, id int64) (JobRow, error) {
	var recs []jobRecord
	if err := db.Raw("SELECT "+jobColumns+" FROM import_jobs WHERE id = ?", id).Scan(&recs).Error; err != nil {
		return JobRow{}, err
	}
	if len(recs) == 0 {
		return JobRow{}, ErrJobNotFound
	}
	return recs[0].toJob()
}

// ListJobs returns the newest imports first.
func ListJobs(db *gorm.DB, limit int) ([]JobRow, error) {
	var recs []jobRecord
	if err := db.Raw("SELECT "+jobColumns+" FROM import_jobs ORDER BY id DESC LIMIT ?", limit).Scan(&recs).Error; err != nil {
		return nil, err
	}
	out := make([]JobRow, 0, len(recs))
	for _, rec := range recs {
		job, err := rec.toJob()
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, nil
}

// Upload stores the export in ImportsDir, reads its summary and creates the
// job. It returns ErrTooLarge, ErrUnsupportedFile or ErrNotWXR for input it
// refuses, and removes what it wrote in that case.
func Upload(ctx context.Context, db *gorm.DB, actor uint, name string, src io.Reader) (JobRow, error) {
	if !AcceptedName(name) {
		return JobRow{}, ErrUnsupportedFile
	}
	kind := Kind(name)
	id, err := createJob(db, actor, name, kind)
	if err != nil {
		return JobRow{}, err
	}
	dest := FilePath(id, kind)
	tmp := dest + ".part"
	fail := func(err error) (JobRow, error) {
		_ = os.Remove(tmp)
		_ = os.Remove(dest)
		_ = db.Exec("DELETE FROM import_jobs WHERE id = ?", id).Error
		return JobRow{}, err
	}

	if err := os.MkdirAll(ImportsDir, 0700); err != nil {
		return fail(err)
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return fail(err)
	}
	n, err := io.Copy(f, io.LimitReader(src, MaxFileBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxFileBytes {
		err = ErrTooLarge
	}
	if err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fail(err)
	}

	sum, authors, site, err := Summarize(ctx, dest, kind)
	if err != nil {
		return fail(err)
	}
	raw, err := json.Marshal(storedStats{Summary: sum, Authors: authors})
	if err != nil {
		return fail(err)
	}
	if err := db.Exec(
		"UPDATE import_jobs SET source_bytes = ?, site_url = ?, stats = ?::jsonb WHERE id = ?",
		n, truncateRunes(site, 2048), string(raw), id,
	).Error; err != nil {
		return fail(err)
	}
	return LoadJob(db, id)
}

func createJob(db *gorm.DB, actor uint, name, kind string) (int64, error) {
	opts, err := json.Marshal(map[string]string{"kind": kind})
	if err != nil {
		return 0, err
	}
	var actorValue any
	if actor != 0 {
		actorValue = actor
	}
	var ids []int64
	err = db.Raw(`INSERT INTO import_jobs (state, source_name, options, created_by)
		VALUES ('uploaded', ?, ?::jsonb, ?) RETURNING id`,
		truncateRunes(sanitizeFileName(path.Base(name)), 255), string(opts), actorValue).Scan(&ids).Error
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, errors.New("importer: insert returned no id")
	}
	return ids[0], nil
}

// Summarize reads the whole export once for its counts, its author list and
// its base site URL.
func Summarize(ctx context.Context, filePath, kind string) (Summary, []Author, string, error) {
	src, err := OpenSource(filePath, kind)
	if err != nil {
		return Summary{}, nil, "", err
	}
	defer src.Close()
	var sum Summary
	authors := []Author{}
	site := ""
	err = Parse(ctx, src.Reader, DefaultLimits, func(it Item) error {
		switch it.Kind {
		case ItemAuthor:
			sum.Authors++
			authors = append(authors, Author{Login: it.Login, Email: it.Email, DisplayName: it.DisplayName, WPID: it.WPID})
		case ItemSite:
			if site == "" {
				site = it.Link
			}
		case ItemPost:
			sum.Posts++
		case ItemPage:
			sum.Pages++
		case ItemAttachment:
			sum.Attachments++
		case ItemComment:
			if importableComment(it) {
				sum.Comments++
			}
		}
		return nil
	})
	if err != nil {
		return Summary{}, nil, "", err
	}
	return sum, authors, site, nil
}

// Cancel stops an import that has not started. Its stored export is removed.
func Cancel(db *gorm.DB, id int64) (JobRow, error) {
	job, err := LoadJob(db, id)
	if err != nil {
		return JobRow{}, err
	}
	res := db.Exec(`UPDATE import_jobs SET state = 'cancelled', finished_at = now()
		WHERE id = ? AND state IN ('uploaded', 'mapped', 'dry_run', 'failed')`, id)
	if res.Error != nil {
		return JobRow{}, res.Error
	}
	if res.RowsAffected == 0 {
		return JobRow{}, ErrJobBusy
	}
	_ = os.Remove(FilePath(id, job.Kind))
	return LoadJob(db, id)
}

// SetMapping stores the admin's type, attachment and author choices. It is
// refused while the import runs or after it was cancelled.
func SetMapping(db *gorm.DB, id int64, m Mapping) (JobRow, error) {
	if _, err := LoadJob(db, id); err != nil {
		return JobRow{}, err
	}
	if m.AuthorRoles == nil {
		m.AuthorRoles = map[string]string{}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return JobRow{}, err
	}
	res := db.Exec(`UPDATE import_jobs
		SET mapping = ?::jsonb, state = CASE WHEN state = 'uploaded' THEN 'mapped' ELSE state END
		WHERE id = ? AND state NOT IN ('running', 'cancelled')`, string(raw), id)
	if res.Error != nil {
		return JobRow{}, res.Error
	}
	if res.RowsAffected == 0 {
		return JobRow{}, ErrJobBusy
	}
	return LoadJob(db, id)
}

// DryRun reports what a run would do and writes nothing except the state and
// the report.
func DryRun(ctx context.Context, db *gorm.DB, id int64, host Host) (JobRow, Report, error) {
	job, err := LoadJob(db, id)
	if err != nil {
		return JobRow{}, Report{}, err
	}
	if job.State == "running" || job.State == "cancelled" {
		return JobRow{}, Report{}, ErrJobBusy
	}
	r := newRunner(db, job, host, true)
	err = scanJob(ctx, job, func(it Item) error {
		return r.handle(ctx, it)
	})
	if err != nil {
		return JobRow{}, Report{}, err
	}
	rep := r.rep
	if err := saveReport(db, job, rep, "", nil); err != nil {
		return JobRow{}, Report{}, err
	}
	_ = db.Exec(`UPDATE import_jobs SET state = 'dry_run'
		WHERE id = ? AND state IN ('uploaded', 'mapped', 'dry_run')`, id).Error
	out, err := LoadJob(db, id)
	return out, rep, err
}

// Run imports the export into the site. Passes run in this order: attachments,
// then posts, pages and comments, then the links in the imported content are
// rewritten. Rerunning an import skips items already mapped.
func Run(ctx context.Context, db *gorm.DB, id int64, host Host) (JobRow, Report, error) {
	job, err := LoadJob(db, id)
	if err != nil {
		return JobRow{}, Report{}, err
	}
	claim := db.Exec(`UPDATE import_jobs SET state = 'running', last_error = NULL
		WHERE id = ? AND state IN ('uploaded', 'mapped', 'dry_run', 'done', 'failed')`, id)
	if claim.Error != nil {
		return JobRow{}, Report{}, claim.Error
	}
	if claim.RowsAffected == 0 {
		return JobRow{}, Report{}, ErrJobBusy
	}

	rep, runErr := runPasses(ctx, db, job, host)
	state, lastErr := "done", ""
	if runErr != nil {
		state, lastErr = "failed", runErr.Error()
	}
	if err := saveReport(db, job, rep, state, &lastErr); err != nil {
		return JobRow{}, rep, err
	}
	out, err := LoadJob(db, id)
	if err != nil {
		return JobRow{}, rep, err
	}
	return out, rep, runErr
}

func runPasses(ctx context.Context, db *gorm.DB, job JobRow, host Host) (Report, error) {
	r := newRunner(db, job, host, false)
	if job.Mapping.FetchAttachments && job.Mapping.includes("attachment") {
		err := scanJob(ctx, job, func(it Item) error {
			if it.Kind != ItemAttachment {
				return nil
			}
			return r.attachment(ctx, it)
		})
		if err != nil {
			return r.rep, err
		}
	}
	err := scanJob(ctx, job, func(it Item) error {
		return r.handle(ctx, it)
	})
	if err != nil {
		return r.rep, err
	}
	if err := r.rewriteLinks(ctx); err != nil {
		return r.rep, err
	}
	return r.rep, nil
}

// saveReport stores the report and, when state is set, the final state.
func saveReport(db *gorm.DB, job JobRow, rep Report, state string, lastErr *string) error {
	st := storedStats{Summary: job.Summary, Authors: job.Authors, Report: &rep}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if state == "" {
		return db.Exec("UPDATE import_jobs SET stats = ?::jsonb WHERE id = ?", string(raw), job.ID).Error
	}
	var msg any
	if lastErr != nil && *lastErr != "" {
		msg = truncateRunes(*lastErr, 2000)
	}
	return db.Exec(`UPDATE import_jobs SET state = ?, stats = ?::jsonb, last_error = ?, finished_at = now()
		WHERE id = ?`, state, string(raw), msg, job.ID).Error
}

func scanJob(ctx context.Context, job JobRow, fn func(Item) error) error {
	src, err := OpenSource(FilePath(job.ID, job.Kind), job.Kind)
	if err != nil {
		return err
	}
	defer src.Close()
	return Parse(ctx, src.Reader, DefaultLimits, fn)
}

// runner holds the state of one dry run or run.
type runner struct {
	db      *gorm.DB
	job     JobRow
	host    Host
	dry     bool
	authors map[string]Author
	rep     Report
	links   map[string]string
	posts   []uint
	siteRe  *regexp.Regexp
}

func newRunner(db *gorm.DB, job JobRow, host Host, dry bool) *runner {
	r := &runner{
		db: db, job: job, host: host, dry: dry,
		authors: map[string]Author{}, rep: newReport(), links: map[string]string{},
	}
	for _, a := range job.Authors {
		r.authors[a.Login] = a
	}
	if u, err := url.Parse(job.SiteURL); err == nil && u.Host != "" {
		r.siteRe = regexp.MustCompile(`(?i)(?:https?:)?//` + regexp.QuoteMeta(u.Host) + `((?:/[^\s"'<>]*)?)`)
	}
	return r
}

// handle dispatches one item for the post and comment passes. A run fetches
// attachments in its own first pass, so there attachments are skipped here.
// A dry run counts them in this pass instead.
func (r *runner) handle(ctx context.Context, it Item) error {
	switch it.Kind {
	case ItemPost, ItemPage:
		return r.post(ctx, it)
	case ItemComment:
		return r.comment(ctx, it)
	case ItemAttachment:
		if r.dry {
			return r.attachment(ctx, it)
		}
	}
	return nil
}

func (r *runner) mappedPost(ctx context.Context, typ string, it Item) (bool, error) {
	local, ok, err := mappedLocal(r.db, r.job.ID, typ, it.WPID)
	if err != nil || !ok {
		return false, err
	}
	r.rep.Skipped["already"]++
	if local == 0 {
		return true, nil
	}
	var post core.Post
	if err := r.db.WithContext(ctx).First(&post, local).Error; err != nil {
		return true, nil
	}
	if it.Link != "" && !r.dry {
		r.links[linkKey(it.Link)] = r.host.PathFor(post)
	}
	r.posts = append(r.posts, post.ID)
	return true, nil
}

func (r *runner) post(ctx context.Context, it Item) error {
	typ := it.Kind
	if !r.job.Mapping.includes(typ) {
		r.rep.Skipped["filtered"]++
		return nil
	}
	if done, err := r.mappedPost(ctx, typ, it); err != nil || done {
		return err
	}

	slug, err := r.uniqueSlug(ctx, it, typ)
	if err != nil {
		return r.fail(typ, it, err)
	}
	authorID, err := r.author(ctx, it)
	if err != nil {
		return r.fail(typ, it, err)
	}
	countKey := "posts"
	if typ == ItemPage {
		countKey = "pages"
	}
	r.rep.Created[countKey]++
	if r.dry {
		return nil
	}

	status := "draft"
	if it.Status == "publish" {
		status = "published"
	}
	post := core.Post{
		Title:         truncateRunes(it.Title, 255),
		Slug:          slug,
		Content:       markup.Policy(markup.TrustStrict).Sanitize(it.Content),
		Excerpt:       bluemonday.StrictPolicy().Sanitize(it.Excerpt),
		Type:          typ,
		Status:        status,
		AuthorID:      authorID,
		Format:        "html",
		RenderTrust:   markup.TrustStrict,
		RenderVersion: 1,
		PublishedAt:   parseWPDate(it.Date),
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&post).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO import_map (job_id, wp_type, wp_id, local_type, local_id, wp_url)
			VALUES (?, ?, ?, ?, ?, ?)`, r.job.ID, typ, it.WPID, typ, post.ID, truncateRunes(it.Link, 2048)).Error
	})
	if err != nil {
		return r.fail(typ, it, err)
	}

	newPath := r.host.PathFor(post)
	if it.Link != "" {
		r.links[linkKey(it.Link)] = newPath
		if from := linkPath(it.Link); from != "" && from != newPath {
			_, err := redirects.CreateRule(r.db, redirects.Redirect{FromPath: from, ToURL: newPath, Kind: redirects.Moved}, "import", r.host.Actor)
			switch {
			case err == nil, errors.Is(err, redirects.ErrDuplicate):
			default:
				r.rep.Conflicts = append(r.rep.Conflicts, Issue{Kind: "redirect", Item: from, Detail: err.Error()})
			}
		}
	}
	r.posts = append(r.posts, post.ID)
	return nil
}

// uniqueSlug returns the slug an item gets. A slug already in use gets a
// numeric suffix and is reported as a conflict.
func (r *runner) uniqueSlug(ctx context.Context, it Item, typ string) (string, error) {
	base := slugFor(it)
	if base == "" {
		return "", errors.New("no usable slug")
	}
	candidate := base
	for n := 2; n <= 100; n++ {
		taken, err := r.slugInUse(ctx, candidate, typ)
		if err != nil {
			return "", err
		}
		if !taken {
			if candidate != base {
				r.rep.Conflicts = append(r.rep.Conflicts, Issue{Kind: "slug", Item: base, Detail: "slug already in use; imported as " + candidate})
			}
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
	return "", errors.New("no free slug")
}

func (r *runner) slugInUse(ctx context.Context, slug, typ string) (bool, error) {
	var count int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*) FROM posts WHERE slug = ?", slug).Scan(&count).Error; err != nil {
		return false, err
	}
	if count > 0 {
		return true, nil
	}
	if typ == ItemPage && r.host.Reserved != nil {
		return r.host.Reserved(ctx, slug), nil
	}
	return false, nil
}

// author maps the item's author to a local user. An author missing from the
// export, or without an email, is reported and the item goes to the importer.
func (r *runner) author(ctx context.Context, it Item) (uint, error) {
	login := strings.TrimSpace(it.AuthorLogin)
	if login == "" {
		return r.host.Actor, nil
	}
	a, ok := r.authors[login]
	if !ok {
		r.rep.Conflicts = append(r.rep.Conflicts, Issue{Kind: "author_missing", Item: login,
			Detail: "author is not in the export's author list; the item is assigned to the importer"})
		return r.host.Actor, nil
	}
	if strings.TrimSpace(a.Email) == "" {
		r.rep.Conflicts = append(r.rep.Conflicts, Issue{Kind: "author_no_email", Item: login,
			Detail: "author has no email, so no invitation can be sent; the item is assigned to the importer"})
		return r.host.Actor, nil
	}
	if r.dry {
		return r.host.Actor, nil
	}
	if local, ok, err := mappedLocal(r.db, r.job.ID, "author", a.WPID); err != nil {
		return 0, err
	} else if ok && local != 0 {
		return uint(local), nil
	}
	role := r.job.Mapping.AuthorRoles[login]
	if role == "" {
		role = "author"
	}
	email := strings.ToLower(strings.TrimSpace(a.Email))
	uid, err := r.host.Author(ctx, email, role, fmt.Sprintf("Imported from WordPress export %s (import %d)", truncateRunes(r.job.SourceName, 200), r.job.ID))
	if err != nil {
		return 0, err
	}
	if err := r.db.Exec(`INSERT INTO import_map (job_id, wp_type, wp_id, local_type, local_id, wp_url)
		VALUES (?, 'author', ?, 'user', ?, '')`, r.job.ID, a.WPID, uid).Error; err != nil {
		return 0, err
	}
	r.rep.Created["authors"]++
	return uid, nil
}

func (r *runner) comment(ctx context.Context, it Item) error {
	if !r.job.Mapping.includes("comment") {
		r.rep.Skipped["filtered"]++
		return nil
	}
	if !importableComment(it) {
		r.rep.Skipped["spam_or_unapproved_type"]++
		return nil
	}
	if _, ok, err := mappedLocal(r.db, r.job.ID, ItemComment, it.WPID); err != nil {
		return err
	} else if ok {
		r.rep.Skipped["already"]++
		return nil
	}
	if r.dry {
		r.rep.Created["comments"]++
		return nil
	}
	postID, err := r.localPostFor(it.PostWPID)
	if err != nil {
		return err
	}
	if postID == 0 {
		r.rep.Skipped["orphan"]++
		return nil
	}
	var parent *uint
	if it.ParentWPID > 0 {
		if local, ok, err := mappedLocal(r.db, r.job.ID, ItemComment, it.ParentWPID); err != nil {
			return err
		} else if ok && local != 0 {
			p := uint(local)
			parent = &p
		}
	}
	status := "pending"
	if it.Approved == "1" {
		status = "approved"
	}
	created := parseWPDate(it.CommentDate)
	now := time.Now().UTC()
	if created == nil {
		created = &now
	}
	text := it.CommentBody
	comment := core.Comment{
		PostID:    postID,
		ParentID:  parent,
		Name:      truncateRunes(it.CommenterName, 120),
		Email:     truncateRunes(it.CommenterEmail, 255),
		Body:      text,
		BodyHTML:  markup.Policy(markup.TrustStrict).Sanitize(strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")),
		Status:    status,
		CreatedAt: *created,
		UpdatedAt: *created,
	}
	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&comment).Error; err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO import_map (job_id, wp_type, wp_id, local_type, local_id, wp_url)
			VALUES (?, ?, ?, 'comment', ?, '')`, r.job.ID, ItemComment, it.WPID, comment.ID).Error
	})
	if err != nil {
		return r.fail(ItemComment, it, err)
	}
	r.rep.Created["comments"]++
	return nil
}

// localPostFor returns the local post or page id of the WXR post id, or 0.
func (r *runner) localPostFor(wpID int64) (uint, error) {
	for _, typ := range []string{ItemPost, ItemPage} {
		local, ok, err := mappedLocal(r.db, r.job.ID, typ, wpID)
		if err != nil {
			return 0, err
		}
		if ok && local != 0 {
			return uint(local), nil
		}
	}
	return 0, nil
}

// attachment fetches one attachment through safehttp. Addresses it refuses are
// listed as blocked and the import goes on.
func (r *runner) attachment(ctx context.Context, it Item) error {
	raw := strings.TrimSpace(it.AttachmentURL)
	if !r.job.Mapping.includes("attachment") {
		r.rep.Skipped["filtered"]++
		return nil
	}
	if !r.job.Mapping.FetchAttachments {
		r.rep.Skipped["attachment_not_fetched"]++
		return nil
	}
	if raw == "" {
		r.rep.Skipped["attachment_no_url"]++
		return nil
	}
	if _, ok, err := mappedLocal(r.db, r.job.ID, ItemAttachment, it.WPID); err != nil {
		return err
	} else if ok {
		r.links[linkKey(raw)] = attachmentLocalURL(r.job.ID, raw)
		r.rep.Skipped["already"]++
		return nil
	}
	if r.dry {
		r.rep.Created["attachments"]++
		return nil
	}
	local, err := fetchAttachment(ctx, r.job.ID, raw)
	if err != nil {
		switch {
		case isBlocked(err):
			r.rep.Blocked = append(r.rep.Blocked, Issue{Kind: "attachment", Item: raw,
				Detail: "blocked: the address is private, loopback or otherwise internal"})
		default:
			r.rep.Failed = append(r.rep.Failed, Issue{Kind: "attachment", Item: raw, Detail: err.Error()})
		}
		return nil
	}
	if err := r.db.Exec(`INSERT INTO import_map (job_id, wp_type, wp_id, local_type, local_id, wp_url)
		VALUES (?, ?, ?, 'upload', NULL, ?)`, r.job.ID, ItemAttachment, it.WPID, truncateRunes(raw, 2048)).Error; err != nil {
		return err
	}
	r.links[linkKey(raw)] = local
	r.rep.Created["attachments"]++
	return nil
}

// rewriteLinks points links to the old site at the new local paths in every
// post the run touched. Links to unknown paths keep their path and lose the old host.
func (r *runner) rewriteLinks(ctx context.Context) error {
	if r.siteRe == nil || r.dry {
		return nil
	}
	seen := map[uint]bool{}
	for _, id := range r.posts {
		if seen[id] {
			continue
		}
		seen[id] = true
		var content string
		if err := r.db.WithContext(ctx).Raw("SELECT content FROM posts WHERE id = ?", id).Scan(&content).Error; err != nil {
			return err
		}
		next := r.siteRe.ReplaceAllStringFunc(content, func(m string) string {
			p := r.siteRe.FindStringSubmatch(m)[1]
			if key := linkKey(p); key != "" {
				if local, ok := r.links[key]; ok {
					return local
				}
				return p
			}
			return "/"
		})
		if next != content {
			if err := r.db.WithContext(ctx).Model(&core.Post{}).Where("id = ?", id).UpdateColumns(map[string]interface{}{"content": next}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *runner) fail(typ string, it Item, err error) error {
	item := it.Slug
	if item == "" {
		item = it.Title
	}
	r.rep.Failed = append(r.rep.Failed, Issue{Kind: typ, Item: item, Detail: err.Error()})
	return nil
}

// mappedLocal returns the local id that an import already created for a WXR
// item. ok is false for items not yet imported. local is 0 for mappings that
// have no local row, such as attachments.
func mappedLocal(db *gorm.DB, jobID int64, wpType string, wpID int64) (uint, bool, error) {
	var rows []struct {
		LocalID *int64 `gorm:"column:local_id"`
	}
	err := db.Raw("SELECT local_id FROM import_map WHERE job_id = ? AND wp_type = ? AND wp_id = ? LIMIT 1",
		jobID, wpType, wpID).Scan(&rows).Error
	if err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	if rows[0].LocalID == nil {
		return 0, true, nil
	}
	return uint(*rows[0].LocalID), true, nil
}

// importableComment reports whether a WXR comment is imported. Spam, trash
// and pingbacks are skipped.
func importableComment(it Item) bool {
	if it.Approved != "1" && it.Approved != "0" {
		return false
	}
	return it.CommentType == "" || it.CommentType == "comment"
}

// linkKey is the lookup key of a URL or path: its path without query, fragment
// or trailing slash.
func linkKey(raw string) string {
	p := raw
	if strings.Contains(p, "://") {
		if u, err := url.Parse(p); err == nil {
			p = u.Path
		}
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	return strings.TrimRight(p, "/")
}

// linkPath returns the path of a WXR permalink, keeping its trailing slash,
// for use as a redirect source.
func linkPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return ""
	}
	return u.Path
}

// slugFor returns the slug an item asks for, decoded and cleaned. It falls
// back to the title, then to the WXR id.
func slugFor(it Item) string {
	s := strings.TrimSpace(it.Slug)
	if decoded, err := url.PathUnescape(s); err == nil {
		s = decoded
	}
	s = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == '?' || r == '#' || r == '%' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return '-'
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.Trim(s, "-")
	if s == "" {
		s = strings.Trim(strings.Map(func(r rune) rune {
			if r == ' ' {
				return '-'
			}
			return r
		}, strings.ToLower(strings.TrimSpace(it.Title))), "-")
	}
	if s == "" && it.WPID > 0 {
		s = fmt.Sprintf("post-%d", it.WPID)
	}
	return truncateRunes(s, 200)
}

// parseWPDate reads a WXR date, which is local to the site. The zero value
// "0000-00-00 00:00:00" means no date.
func parseWPDate(s string) *time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), time.UTC)
	if err != nil || t.Year() < 1900 {
		return nil
	}
	return &t
}

// truncateRunes shortens s to at most n runes.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	out := []rune(s)
	return string(out[:n])
}

// View returns the JSON body of an import job for the admin screen and API.
func View(job JobRow) map[string]any {
	rep := newReport()
	if job.Report != nil {
		rep = *job.Report
	}
	var mapping any = job.Mapping
	var finished any
	if job.FinishedAt != nil {
		finished = job.FinishedAt.UTC().Format(time.RFC3339)
	}
	var lastErr any
	if job.LastError != "" {
		lastErr = job.LastError
	}
	return map[string]any{
		"id":           job.ID,
		"state":        job.State,
		"source_name":  job.SourceName,
		"source_bytes": job.SourceBytes,
		"site_url":     job.SiteURL,
		"mapping":      mapping,
		"created_at":   job.CreatedAt.UTC().Format(time.RFC3339),
		"finished_at":  finished,
		"last_error":   lastErr,
		"posts":        job.Summary.Posts,
		"pages":        job.Summary.Pages,
		"attachments":  job.Summary.Attachments,
		"comments":     job.Summary.Comments,
		"authors":      job.Summary.Authors,
		"author_list":  job.Authors,
		"created":      rep.Created,
		"skipped":      rep.Skipped,
		"conflicts":    rep.Conflicts,
		"blocked":      rep.Blocked,
		"failed":       rep.Failed,
	}
}

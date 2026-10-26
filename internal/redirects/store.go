package redirects

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
)

// ruleLockKey is the advisory lock that serializes rule writes, so each loop
// and depth check sees every rule saved before it.
const ruleLockKey = 731984203

// maxImportRows bounds one CSV import.
const maxImportRows = 5000

func lockRules(tx *gorm.DB) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(?)", ruleLockKey).Error
}

func loadRules(db *gorm.DB) ([]Redirect, error) {
	rows := []Redirect{}
	if err := db.Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// activeRules returns the unexpired rules except the one with id exclude.
func activeRules(rules []Redirect, now time.Time, exclude uint) []Redirect {
	out := make([]Redirect, 0, len(rules))
	for _, r := range rules {
		if r.ID != exclude && r.active(now) {
			out = append(out, r)
		}
	}
	return out
}

// checkDuplicate refuses a source path that another rule already uses. The
// unique index covers exact rules only, so wildcard rules are checked here too.
func checkDuplicate(rules []Redirect, r Redirect, exclude uint) error {
	for _, x := range rules {
		if x.ID != exclude && strings.EqualFold(x.FromPath, r.FromPath) {
			return ErrDuplicate
		}
	}
	return nil
}

func actorPtr(actor uint) *uint {
	if actor == 0 {
		return nil
	}
	return &actor
}

// CreateRule validates and saves a rule. source is "manual" for admin input.
func CreateRule(db *gorm.DB, in Redirect, source string, actor uint) (Redirect, error) {
	if err := normalize(&in); err != nil {
		return Redirect{}, err
	}
	in.ID = 0
	in.Source = source
	in.Hits = 0
	in.LastHitAt = nil
	in.CreatedBy = actorPtr(actor)
	now := time.Now()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockRules(tx); err != nil {
			return err
		}
		all, err := loadRules(tx)
		if err != nil {
			return err
		}
		if err := checkDuplicate(all, in, 0); err != nil {
			return err
		}
		if err := checkChain(activeRules(all, now, 0), in, now); err != nil {
			return err
		}
		return tx.Create(&in).Error
	})
	return in, err
}

// UpdateRule replaces the fields of rule id. Source, hits and creation data stay as they were.
func UpdateRule(db *gorm.DB, id uint, in Redirect) (Redirect, error) {
	if err := normalize(&in); err != nil {
		return Redirect{}, err
	}
	now := time.Now()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockRules(tx); err != nil {
			return err
		}
		all, err := loadRules(tx)
		if err != nil {
			return err
		}
		var cur *Redirect
		for i := range all {
			if all[i].ID == id {
				cur = &all[i]
				break
			}
		}
		if cur == nil {
			return ErrNotFound
		}
		in.ID = id
		in.Source = cur.Source
		in.Hits = cur.Hits
		in.LastHitAt = cur.LastHitAt
		in.CreatedBy = cur.CreatedBy
		in.CreatedAt = cur.CreatedAt
		if err := checkDuplicate(all, in, id); err != nil {
			return err
		}
		if err := checkChain(activeRules(all, now, id), in, now); err != nil {
			return err
		}
		return tx.Save(&in).Error
	})
	return in, err
}

// DeleteRule removes rule id.
func DeleteRule(db *gorm.DB, id uint) error {
	res := db.Where("id = ?", id).Delete(&Redirect{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListQuery filters the rule list. Zero values mean no filter.
type ListQuery struct {
	Kind    int
	Source  string
	Query   string
	Page    int
	PerPage int
}

// Counts are the totals shown above the rule list.
type Counts struct {
	All    int64 `json:"all" gorm:"column:all_count"`
	K301   int64 `json:"301" gorm:"column:k301"`
	K302   int64 `json:"302" gorm:"column:k302"`
	K410   int64 `json:"410" gorm:"column:k410"`
	Auto   int64 `json:"auto" gorm:"column:auto_count"`
	Broken int64 `json:"broken" gorm:"column:broken"`
}

// ListResult is one page of rules with the totals.
type ListResult struct {
	Items  []Redirect `json:"items"`
	Total  int64      `json:"total"`
	Counts Counts     `json:"counts"`
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func filterRedirects(tx *gorm.DB, q ListQuery) *gorm.DB {
	if q.Kind != 0 {
		tx = tx.Where("kind = ?", q.Kind)
	}
	if q.Source != "" {
		tx = tx.Where("source = ?", q.Source)
	}
	if q.Query != "" {
		like := "%" + likeEscaper.Replace(q.Query) + "%"
		tx = tx.Where("(from_path ILIKE ? OR to_url ILIKE ?)", like, like)
	}
	return tx
}

// ListRedirects returns one page of rules with the counts for the filter tabs.
func ListRedirects(db *gorm.DB, q ListQuery) (ListResult, error) {
	page, perPage := q.Page, q.PerPage
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 50
	}
	var total int64
	if err := filterRedirects(db.Model(&Redirect{}), q).Count(&total).Error; err != nil {
		return ListResult{}, err
	}
	items := []Redirect{}
	err := filterRedirects(db.Model(&Redirect{}), q).
		Order("id").Limit(perPage).Offset((page - 1) * perPage).Find(&items).Error
	if err != nil {
		return ListResult{}, err
	}
	var counts Counts
	err = db.Raw(`SELECT COUNT(*) AS all_count,
		COUNT(*) FILTER (WHERE kind = 301) AS k301,
		COUNT(*) FILTER (WHERE kind = 302) AS k302,
		COUNT(*) FILTER (WHERE kind = 410) AS k410,
		COUNT(*) FILTER (WHERE source = 'auto') AS auto_count,
		COUNT(*) FILTER (WHERE target_status >= 400) AS broken
		FROM redirects`).Scan(&counts).Error
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Items: items, Total: total, Counts: counts}, nil
}

// Hop is one step of a redirect chain.
type Hop struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   int    `json:"kind"`
	Source string `json:"source"`
}

// Trace is the result of following a path through the saved rules.
type Trace struct {
	Path    string `json:"path"`
	Chain   []Hop  `json:"chain"`
	Final   string `json:"final"`
	Status  int    `json:"status"`
	Loop    bool   `json:"loop"`
	TooDeep bool   `json:"too_deep"`
}

// TraceChain follows path through the active rules, the way a visitor would.
func TraceChain(db *gorm.DB, path string) (Trace, error) {
	all, err := loadRules(db)
	if err != nil {
		return Trace{}, err
	}
	now := time.Now()
	active := activeRules(all, now, 0)
	trace := Trace{Path: path, Chain: []Hop{}, Final: path, Status: 200}
	seen := map[string]bool{}
	cur := stripQuery(path)
	for {
		key := strings.ToLower(cur)
		if seen[key] {
			trace.Loop = true
			break
		}
		seen[key] = true
		next, ok := resolve(active, cur, now)
		if !ok {
			break
		}
		if len(trace.Chain) == MaxChainDepth {
			trace.TooDeep = true
			break
		}
		trace.Chain = append(trace.Chain, Hop{From: cur, To: next.ToURL, Kind: next.Kind, Source: next.Source})
		if next.Kind == Gone {
			break
		}
		if isExternal(next.ToURL) {
			trace.Final = next.ToURL
			break
		}
		cur = stripQuery(next.ToURL)
		trace.Final = cur
	}
	if len(trace.Chain) > 0 {
		trace.Status = trace.Chain[0].Kind
	}
	return trace, nil
}

// ImportRow is one CSV line. ParseError is set when the line cannot be read.
type ImportRow struct {
	Line       int
	Rule       Redirect
	ParseError string
}

// ImportIssue names a row that was skipped or refused.
type ImportIssue struct {
	Line     int    `json:"line"`
	FromPath string `json:"from_path"`
	Reason   string `json:"reason"`
}

// ImportReport summarizes a dry run or an import. Created counts the rows a
// dry run would write.
type ImportReport struct {
	DryRun    bool          `json:"dry_run"`
	Total     int           `json:"total"`
	Created   int           `json:"created"`
	Conflicts []ImportIssue `json:"conflicts"`
	Errors    []ImportIssue `json:"errors"`
}

// ParseRedirectCSV reads a CSV whose header names from_path, to_url and kind.
// keep_query and expires_at are optional columns.
func ParseRedirectCSV(src io.Reader) ([]ImportRow, error) {
	r := csv.NewReader(src)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, ruleError("the CSV needs a header row")
	}
	idx := map[string]int{}
	for i, name := range header {
		if i == 0 {
			name = strings.TrimPrefix(name, string(rune(0xFEFF)))
		}
		idx[strings.ToLower(strings.TrimSpace(name))] = i
	}
	for _, need := range []string{"from_path", "to_url", "kind"} {
		if _, ok := idx[need]; !ok {
			return nil, ruleError("the header must include from_path, to_url and kind")
		}
	}
	rows := []ImportRow{}
	line := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return nil, ruleError(fmt.Sprintf("line %d: %v", line, err))
		}
		if len(rows) >= maxImportRows {
			return nil, ruleError("an import holds at most 5000 rows")
		}
		get := func(name string) string {
			i, ok := idx[name]
			if !ok || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		row := ImportRow{Line: line}
		kind := 0
		if v := get("kind"); v != "" {
			k, err := strconv.Atoi(v)
			if err != nil {
				row.ParseError = "kind must be a number"
			}
			kind = k
		}
		row.Rule = Redirect{FromPath: get("from_path"), ToURL: get("to_url"), Kind: kind}
		if v := get("keep_query"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil && row.ParseError == "" {
				row.ParseError = "keep_query must be true or false"
			}
			row.Rule.KeepQuery = b
		}
		if v := get("expires_at"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil && row.ParseError == "" {
				row.ParseError = "expires_at must be RFC 3339"
			}
			if err == nil {
				row.Rule.ExpiresAt = &t
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// planImport checks rows against existing rules and the rows before them. It
// returns the rules to write and the report. Nothing is written here.
func planImport(existing []Redirect, rows []ImportRow, actor uint, now time.Time) ([]Redirect, ImportReport) {
	report := ImportReport{Total: len(rows), Conflicts: []ImportIssue{}, Errors: []ImportIssue{}}
	taken := map[string]bool{}
	for _, x := range existing {
		taken[strings.ToLower(x.FromPath)] = true
	}
	active := activeRules(existing, now, 0)
	var toInsert []Redirect
	for _, row := range rows {
		rule := row.Rule
		issue := ImportIssue{Line: row.Line, FromPath: rule.FromPath}
		if row.ParseError != "" {
			issue.Reason = row.ParseError
			report.Errors = append(report.Errors, issue)
			continue
		}
		if err := normalize(&rule); err != nil {
			issue.Reason = err.Error()
			report.Errors = append(report.Errors, issue)
			continue
		}
		key := strings.ToLower(rule.FromPath)
		if taken[key] {
			issue.Reason = "from_path already has a rule; the row was not imported"
			report.Conflicts = append(report.Conflicts, issue)
			continue
		}
		if err := checkChain(active, rule, now); err != nil {
			issue.Reason = err.Error()
			report.Errors = append(report.Errors, issue)
			continue
		}
		rule.Source = "import"
		rule.CreatedBy = actorPtr(actor)
		taken[key] = true
		active = append(active, rule)
		toInsert = append(toInsert, rule)
	}
	report.Created = len(toInsert)
	return toInsert, report
}

// ImportRedirects checks rows and, unless dry is set, writes them. The write is
// all or nothing: any invalid row refuses the whole file with ErrImportRejected.
// A row whose from_path already has a rule is skipped and reported as a conflict.
func ImportRedirects(db *gorm.DB, rows []ImportRow, dry bool, actor uint) (ImportReport, error) {
	now := time.Now()
	if dry {
		all, err := loadRules(db)
		if err != nil {
			return ImportReport{}, err
		}
		_, report := planImport(all, rows, actor, now)
		report.DryRun = true
		return report, nil
	}
	var report ImportReport
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := lockRules(tx); err != nil {
			return err
		}
		all, err := loadRules(tx)
		if err != nil {
			return err
		}
		toInsert, planned := planImport(all, rows, actor, now)
		report = planned
		if len(planned.Errors) > 0 {
			return ErrImportRejected
		}
		if len(toInsert) > 0 {
			if err := tx.CreateInBatches(&toInsert, 500).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	return report, nil
}

// WriteRedirectCSV writes every rule as CSV. Cells that a spreadsheet could read
// as a formula are prefixed with an apostrophe.
func WriteRedirectCSV(w io.Writer, db *gorm.DB) error {
	rows, err := loadRules(db)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"from_path", "to_url", "kind", "keep_query", "expires_at", "source", "hits"}); err != nil {
		return err
	}
	for _, r := range rows {
		expires := ""
		if r.ExpiresAt != nil {
			expires = r.ExpiresAt.UTC().Format(time.RFC3339)
		}
		record := []string{
			csvSafe(r.FromPath), csvSafe(r.ToURL), strconv.Itoa(r.Kind),
			strconv.FormatBool(r.KeepQuery), expires, csvSafe(r.Source),
			strconv.FormatInt(r.Hits, 10),
		}
		if err := cw.Write(record); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// NotFoundItem is one aggregated miss. Suggestion names an existing page whose
// slug is close to the missing path, or is empty.
type NotFoundItem struct {
	ID           int64     `json:"id" gorm:"column:id"`
	Path         string    `json:"path" gorm:"column:path"`
	Count        int64     `json:"count" gorm:"column:count"`
	FirstSeen    time.Time `json:"first_seen" gorm:"column:first_seen"`
	LastSeen     time.Time `json:"last_seen" gorm:"column:last_seen"`
	ReferrerKind string    `json:"referrer_kind" gorm:"column:referrer_kind"`
	UAKind       string    `json:"ua_kind" gorm:"column:ua_kind"`
	Ignored      bool      `json:"ignored" gorm:"column:ignored"`
	Suggestion   string    `json:"suggestion" gorm:"-"`
}

type slugRow struct {
	Slug string `gorm:"column:slug"`
	Type string `gorm:"column:type"`
}

// ListNotFound returns the most frequent misses seen in the last days days.
// ignored is "" or "false" for open rows, "true" for ignored rows and "all" for both.
func ListNotFound(db *gorm.DB, days int, ignored string) ([]NotFoundItem, error) {
	if days < 1 || days > 365 {
		days = 30
	}
	q := db.Table("not_found_log").Where("last_seen >= ?", time.Now().AddDate(0, 0, -days))
	switch ignored {
	case "all":
	case "true":
		q = q.Where("ignored = TRUE")
	default:
		q = q.Where("ignored = FALSE")
	}
	items := []NotFoundItem{}
	if err := q.Order("count DESC").Order("last_seen DESC").Limit(200).Find(&items).Error; err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return items, nil
	}
	slugs := []slugRow{}
	if err := db.Table("posts").Select("slug, type").Where("deleted_at IS NULL").
		Limit(20000).Find(&slugs).Error; err != nil {
		return nil, err
	}
	for i := range items {
		items[i].Suggestion = suggestFor(items[i].Path, slugs)
	}
	return items, nil
}

// suggestFor returns the public path of the page whose slug is closest to the
// last segment of path, or "" when nothing is close enough.
func suggestFor(path string, slugs []slugRow) string {
	name := path[strings.LastIndex(path, "/")+1:]
	if utf8.RuneCountInString(name) < 4 {
		return ""
	}
	limit := 2
	if len(name) >= 12 {
		limit = 3
	}
	best := -1
	var pick slugRow
	for _, s := range slugs {
		if s.Slug == "" {
			continue
		}
		diff := len(s.Slug) - len(name)
		if diff < 0 {
			diff = -diff
		}
		if diff > limit {
			continue
		}
		d := levenshtein(name, s.Slug)
		if d <= limit && (best < 0 || d < best) {
			best, pick = d, s
		}
	}
	if best < 0 {
		return ""
	}
	if pick.Type == "page" {
		return "/" + pick.Slug
	}
	return "/post/" + pick.Slug
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// validMissPath reports whether path may be stored in the 404 log: a path only,
// at most 512 bytes, valid UTF-8 and free of control characters.
func validMissPath(path string) bool {
	return len(path) > 0 && len(path) <= maxMissBytes && path[0] == '/' &&
		utf8.ValidString(path) && !hasControl(path) && !strings.ContainsAny(path, "?#")
}

// RecordNotFound counts one miss of path. The table keeps NotFoundCap distinct
// paths and drops the least recently seen ones past that.
func RecordNotFound(db *gorm.DB, path, referrerKind, uaKind string) error {
	if !validMissPath(path) {
		return nil
	}
	sum := sha256.Sum256([]byte(path))
	hash := hex.EncodeToString(sum[:])
	return db.Transaction(func(tx *gorm.DB) error {
		err := tx.Exec(`INSERT INTO not_found_log (path, path_hash, count, first_seen, last_seen, referrer_kind, ua_kind, ignored)
			VALUES (?, ?, 1, now(), now(), ?, ?, FALSE)
			ON CONFLICT (path_hash) DO UPDATE SET count = not_found_log.count + 1, last_seen = now(),
				referrer_kind = EXCLUDED.referrer_kind, ua_kind = EXCLUDED.ua_kind`,
			path, hash, referrerKind, uaKind).Error
		if err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM not_found_log WHERE id IN (
			SELECT id FROM not_found_log ORDER BY last_seen ASC, id ASC
			LIMIT GREATEST((SELECT COUNT(*) FROM not_found_log) - ?, 0))`, NotFoundCap).Error
	})
}

// IgnoreNotFound marks one miss as ignored. It stays in the table.
func IgnoreNotFound(db *gorm.DB, id int64) error {
	res := db.Exec("UPDATE not_found_log SET ignored = TRUE WHERE id = ?", id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearNotFound deletes every row of the 404 log and returns how many were removed.
func ClearNotFound(db *gorm.DB) (int64, error) {
	res := db.Exec("DELETE FROM not_found_log")
	return res.RowsAffected, res.Error
}

// ReleaseAutoPath removes the automatic rules that send path elsewhere, so the
// page at path can be served. Admin rules are kept.
func ReleaseAutoPath(tx *gorm.DB, path string) error {
	if err := lockRules(tx); err != nil {
		return err
	}
	return tx.Exec("DELETE FROM redirects WHERE source = 'auto' AND lower(from_path) = lower(?)", path).Error
}

// SlugChangeRedirect records an automatic 301 from the old public path to the
// new one after a slug or type change. It returns a reason, and no error, when
// no rule was written: an admin rule owns the old path, or the rule would loop.
func SlugChangeRedirect(tx *gorm.DB, oldPath, newPath string, actor uint) (string, error) {
	now := time.Now()
	if err := lockRules(tx); err != nil {
		return "", err
	}
	if err := tx.Exec(`DELETE FROM redirects WHERE source = 'auto' AND lower(from_path) IN (lower(?), lower(?))`,
		newPath, oldPath).Error; err != nil {
		return "", err
	}
	all, err := loadRules(tx)
	if err != nil {
		return "", err
	}
	for _, x := range all {
		if strings.EqualFold(x.FromPath, oldPath) {
			return "an admin rule already owns " + oldPath, nil
		}
	}
	rule := Redirect{FromPath: oldPath, ToURL: newPath, Kind: Moved, Source: "auto", CreatedBy: actorPtr(actor)}
	if err := normalize(&rule); err != nil {
		return err.Error(), nil
	}
	if err := checkChain(activeRules(all, now, 0), rule, now); err != nil {
		return err.Error(), nil
	}
	return "", tx.Create(&rule).Error
}

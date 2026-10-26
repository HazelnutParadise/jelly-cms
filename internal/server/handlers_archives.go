package server

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	datapkg "github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/theme"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

// Public archives (slot C5). Each page lists published, public posts, pinned first then newest,
// 12 per page (?page=), 404 for unknown terms or pages beyond the end.
//
//	GET /c/:slug            category.html   data: Term (core.Category), Children ([]core.Category), Posts, Page, TotalPages, Total, Breadcrumbs ([]core.Category from root)
//	                        includes posts of descendant categories unless a descendant has NoCascade; HideFromList categories are still reachable here
//	GET /t/:slug            tag.html        data: Term (core.Tag), Related ([]core.Tag that co-occur most), Posts, Page, TotalPages, Total
//	GET /author/:username   author.html     data: Author (core.User without secrets), Posts, Page, TotalPages, Total, TopCategories ([]core.Category with PostCount)
//	GET /archive            archive.html    data: Categories (tree, with PostCount, hidden ones excluded), Tags (by PostCount desc), Months ([{Year, Month, Count}]), Authors ([]core.User with PostCount), Total
//	GET /archive/:year/:month  archive_month.html  data: Year, Month, Posts, Page, TotalPages, Total
//
// Every data map also carries SiteTitle, Title and CurrentPath. Posts are preloaded with Author, Categories, Tags.
// Templates live in web/themes/default and follow design/project/Archive.dc.html (category: blue head),
// Tag.dc.html (tag: ink head; month: yellow head), Author.dc.html (mint head), ArchiveIndex.dc.html.
// posts.html / index.html / post.html show each post's categories as chips and link to /c/<slug>;
// post.html shows tags and links the author name to /author/<username>.
//
// The /:slug catch-all in handlers_public.go must keep working for pages: register these routes before it
// (RegisterRoutes already calls registerContentRoutes ahead of RegisterPublicRoutes).

func registerArchiveRoutes(e *echo.Echo, tm *theme.Manager) {
	e.Match([]string{http.MethodGet, http.MethodHead}, "/c/:slug", archiveCategory(tm))
	e.Match([]string{http.MethodGet, http.MethodHead}, "/t/:slug", archiveTag(tm))
	e.Match([]string{http.MethodGet, http.MethodHead}, "/author/:username", archiveAuthor(tm))
	e.Match([]string{http.MethodGet, http.MethodHead}, "/archive", archiveIndex(tm))
	e.Match([]string{http.MethodGet, http.MethodHead}, "/archive/:year/:month", archiveMonthHandler(tm))
}

const (
	archiveRelatedMax = 8
	archiveTopCatsMax = 8
	archiveSideMax    = 10
)

var (
	archiveYearPattern  = regexp.MustCompile(`^[0-9]{4}$`)
	archiveMonthPattern = regexp.MustCompile(`^[0-9]{1,2}$`)
)

// archiveMonth is one row of the month list.
type archiveMonth struct {
	Year  int
	Month int
	Count int64 `gorm:"column:cnt"`
}

// archiveCategoryNode is one node of the visible category tree. PostCount counts the published, public
// posts the category archive lists (descendants included unless they are NoCascade).
type archiveCategoryNode struct {
	core.Category
	Children []*archiveCategoryNode
}

// archiveAuthorEntry is an author without secrets plus the number of public posts.
type archiveAuthorEntry struct {
	core.User
	PostCount int64
	Initial   string
}

func archiveInitial(name string) string {
	for _, r := range name {
		return string(r)
	}
	return "?"
}

func newArchiveAuthor(u core.User, count int64) archiveAuthorEntry {
	u.Password = ""
	u.Email = ""
	return archiveAuthorEntry{User: u, PostCount: count, Initial: archiveInitial(u.Username)}
}

// publicPostVisibility scopes posts to what visitors may read, without choosing a content type.
func publicPostVisibility(db *gorm.DB) *gorm.DB {
	return db.Model(&core.Post{}).
		Where("posts.status = ?", "published").
		Where("(posts.visibility = ? OR posts.visibility = '' OR posts.visibility IS NULL)", "public").
		Where("COALESCE(posts.published_at, posts.created_at) <= ?", time.Now())
}

// archivePublicPosts keeps archive pages scoped to public posts, not pages.
func archivePublicPosts(db *gorm.DB) *gorm.DB {
	return publicPostVisibility(db).Where("posts.type = ?", "post")
}

const archivePostDate = "COALESCE(posts.published_at, posts.created_at)"

func archivePageParam(c echo.Context) int {
	page, err := strconv.Atoi(c.QueryParam("page"))
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// archiveList loads one page of public posts matched by scope, pinned first then newest. Pages beyond the
// end answer 404 except page 1, which is always valid.
func archiveList(scope func(*gorm.DB) *gorm.DB, page int, pageSizeKey string) (posts []core.Post, total int64, totalPages int, err error) {
	db := datapkg.DB
	archivePageSize := int64(perPage(context.Background(), pageSizeKey))
	if err = scope(archivePublicPosts(db)).Count(&total).Error; err != nil {
		return nil, 0, 0, err
	}
	totalPages = int((total + archivePageSize - 1) / archivePageSize)
	if page > 1 && page > totalPages {
		return nil, total, totalPages, echo.ErrNotFound
	}
	err = scope(archivePublicPosts(db)).
		Preload("Author").
		Preload("Categories", func(tx *gorm.DB) *gorm.DB {
			return tx.Where("categories.hide_from_list = ?", false).Order("categories.position ASC, categories.name ASC")
		}).
		Preload("Tags", func(tx *gorm.DB) *gorm.DB { return tx.Order("tags.name ASC") }).
		Order("posts.pinned DESC, " + archivePostDate + " DESC, posts.id DESC").
		Limit(int(archivePageSize)).Offset((page - 1) * int(archivePageSize)).
		Find(&posts).Error
	return posts, total, totalPages, err
}

func archiveRender(c echo.Context, tm *theme.Manager, tpl, title string, data map[string]interface{}) error {
	data["SiteTitle"] = getSiteTitle()
	data["Title"] = title
	data["CurrentPath"] = c.Request().URL.Path
	applyPublicSEO(c, datapkg.DB, tpl, data)
	html, err := tm.RenderWithDB(tpl, data, datapkg.DB)
	if err != nil {
		return err
	}
	return c.HTMLBlob(http.StatusOK, html)
}

func archiveAllCategories() ([]core.Category, error) {
	var all []core.Category
	err := datapkg.DB.Order("position ASC, name ASC, id ASC").Find(&all).Error
	return all, err
}

// archiveCascadeIDs returns rootID and the ids of its descendants. A NoCascade descendant, and everything
// below it, is left out; the root itself is always included.
func archiveCascadeIDs(all []core.Category, rootID uint) []uint {
	children := map[uint][]core.Category{}
	for _, cat := range all {
		if cat.ParentID != nil {
			children[*cat.ParentID] = append(children[*cat.ParentID], cat)
		}
	}
	ids := []uint{rootID}
	seen := map[uint]bool{rootID: true}
	for queue := []uint{rootID}; len(queue) > 0; queue = queue[1:] {
		for _, child := range children[queue[0]] {
			if child.NoCascade || seen[child.ID] {
				continue
			}
			seen[child.ID] = true
			ids = append(ids, child.ID)
			queue = append(queue, child.ID)
		}
	}
	return ids
}

// archiveCategoryCounts counts, per category, the distinct public posts its archive page lists.
func archiveCategoryCounts(all []core.Category) (map[uint]int64, error) {
	var pairs []struct {
		CategoryID uint
		PostID     uint
	}
	err := archivePublicPosts(datapkg.DB).
		Select("DISTINCT post_categories.category_id AS category_id, posts.id AS post_id").
		Joins("JOIN post_categories ON post_categories.post_id = posts.id").
		Scan(&pairs).Error
	if err != nil {
		return nil, err
	}
	direct := map[uint]map[uint]struct{}{}
	for _, pair := range pairs {
		if direct[pair.CategoryID] == nil {
			direct[pair.CategoryID] = map[uint]struct{}{}
		}
		direct[pair.CategoryID][pair.PostID] = struct{}{}
	}
	counts := make(map[uint]int64, len(all))
	for _, cat := range all {
		union := map[uint]struct{}{}
		for _, id := range archiveCascadeIDs(all, cat.ID) {
			for postID := range direct[id] {
				union[postID] = struct{}{}
			}
		}
		counts[cat.ID] = int64(len(union))
	}
	return counts, nil
}

// archiveVisibleTree builds the tree of categories that are not hidden from lists. A hidden category takes
// its whole subtree with it.
func archiveVisibleTree(all []core.Category, counts map[uint]int64) []*archiveCategoryNode {
	byID := make(map[uint]core.Category, len(all))
	for _, cat := range all {
		byID[cat.ID] = cat
	}
	visible := func(cat core.Category) bool {
		seen := map[uint]bool{}
		for {
			if cat.HideFromList || seen[cat.ID] {
				return false
			}
			seen[cat.ID] = true
			if cat.ParentID == nil {
				return true
			}
			parent, ok := byID[*cat.ParentID]
			if !ok {
				return true
			}
			cat = parent
		}
	}
	nodes := map[uint]*archiveCategoryNode{}
	for _, cat := range all {
		if visible(cat) {
			cat.PostCount = counts[cat.ID]
			nodes[cat.ID] = &archiveCategoryNode{Category: cat}
		}
	}
	var roots []*archiveCategoryNode
	for _, cat := range all {
		node := nodes[cat.ID]
		if node == nil {
			continue
		}
		if cat.ParentID != nil {
			if parent := nodes[*cat.ParentID]; parent != nil {
				parent.Children = append(parent.Children, node)
				continue
			}
			if _, exists := byID[*cat.ParentID]; exists {
				continue
			}
		}
		roots = append(roots, node)
	}
	return roots
}

// archiveBreadcrumbs lists the categories from the root down to cat.
func archiveBreadcrumbs(all []core.Category, cat core.Category) []core.Category {
	byID := make(map[uint]core.Category, len(all))
	for _, c := range all {
		byID[c.ID] = c
	}
	crumbs := []core.Category{cat}
	seen := map[uint]bool{cat.ID: true}
	for cat.ParentID != nil {
		parent, ok := byID[*cat.ParentID]
		if !ok || seen[parent.ID] {
			break
		}
		seen[parent.ID] = true
		crumbs = append([]core.Category{parent}, crumbs...)
		cat = parent
	}
	return crumbs
}

func archiveMonths() ([]archiveMonth, error) {
	var months []archiveMonth
	expr := archivePostDate + " AT TIME ZONE 'UTC'"
	err := archivePublicPosts(datapkg.DB).
		Select("EXTRACT(YEAR FROM " + expr + ")::int AS year, EXTRACT(MONTH FROM " + expr + ")::int AS month, COUNT(*) AS cnt").
		Group("year, month").Order("year DESC, month DESC").
		Scan(&months).Error
	return months, err
}

// archiveAuthors lists the users with at least one public post, most posts first.
func archiveAuthors() ([]archiveAuthorEntry, error) {
	var rows []struct {
		UserID uint
		Cnt    int64
	}
	err := archivePublicPosts(datapkg.DB).
		Select("posts.author_id AS user_id, COUNT(*) AS cnt").
		Group("posts.author_id").Order("cnt DESC, posts.author_id ASC").
		Scan(&rows).Error
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = row.UserID
	}
	var users []core.User
	if err := datapkg.DB.Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]core.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	authors := make([]archiveAuthorEntry, 0, len(rows))
	for _, row := range rows {
		if u, ok := byID[row.UserID]; ok {
			authors = append(authors, newArchiveAuthor(u, row.Cnt))
		}
	}
	sort.SliceStable(authors, func(i, j int) bool {
		if authors[i].PostCount != authors[j].PostCount {
			return authors[i].PostCount > authors[j].PostCount
		}
		return authors[i].Username < authors[j].Username
	})
	return authors, nil
}

// archiveTagsByCount returns tags with at least one public post, most used first. Limit 0 means all.
func archiveTagsByCount(limit int, scope func(*gorm.DB) *gorm.DB, exclude uint) ([]core.Tag, error) {
	var rows []struct {
		TagID uint
		Cnt   int64
	}
	q := archivePublicPosts(datapkg.DB)
	if scope != nil {
		q = scope(q)
	}
	q = q.Select("post_tags.tag_id AS tag_id, COUNT(DISTINCT posts.id) AS cnt").
		Joins("JOIN post_tags ON post_tags.post_id = posts.id")
	if exclude != 0 {
		q = q.Where("post_tags.tag_id <> ?", exclude)
	}
	q = q.Group("post_tags.tag_id").Order("cnt DESC, post_tags.tag_id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Scan(&rows).Error; err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = row.TagID
	}
	var tags []core.Tag
	if err := datapkg.DB.Where("id IN ?", ids).Find(&tags).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]core.Tag, len(tags))
	for _, t := range tags {
		byID[t.ID] = t
	}
	out := make([]core.Tag, 0, len(rows))
	for _, row := range rows {
		if t, ok := byID[row.TagID]; ok {
			t.PostCount = row.Cnt
			out = append(out, t)
		}
	}
	return out, nil
}

func archiveCategory(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		var term core.Category
		if err := datapkg.DB.Where("slug = ?", c.Param("slug")).First(&term).Error; err != nil {
			return echo.ErrNotFound
		}
		all, err := archiveAllCategories()
		if err != nil {
			return err
		}
		counts, err := archiveCategoryCounts(all)
		if err != nil {
			return err
		}
		ids := archiveCascadeIDs(all, term.ID)
		posts, total, totalPages, err := archiveList(func(tx *gorm.DB) *gorm.DB {
			return tx.Where("posts.id IN (?)", datapkg.DB.Table("post_categories").Select("post_id").Where("category_id IN ?", ids))
		}, archivePageParam(c), keyPerPageTaxonomy)
		if err != nil {
			return err
		}
		term.PostCount = total

		children := []core.Category{}
		for _, cat := range all {
			if cat.ParentID != nil && *cat.ParentID == term.ID && !cat.HideFromList {
				cat.PostCount = counts[cat.ID]
				children = append(children, cat)
			}
		}
		crumbs := archiveBreadcrumbs(all, term)
		others := []core.Category{}
		for _, node := range archiveVisibleTree(all, counts) {
			if node.ID != crumbs[0].ID && len(others) < archiveSideMax {
				others = append(others, node.Category)
			}
		}
		return archiveRender(c, tm, "category.html", term.Name, map[string]interface{}{
			"Term":            term,
			"Children":        children,
			"Breadcrumbs":     crumbs,
			"OtherCategories": others,
			"Posts":           posts,
			"Page":            archivePageParam(c),
			"TotalPages":      totalPages,
			"Total":           total,
		})
	}
}

func archiveTag(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		var term core.Tag
		if err := datapkg.DB.Where("slug = ?", c.Param("slug")).First(&term).Error; err != nil {
			return echo.ErrNotFound
		}
		withTag := func(tx *gorm.DB) *gorm.DB {
			return tx.Where("posts.id IN (?)", datapkg.DB.Table("post_tags").Select("post_id").Where("tag_id = ?", term.ID))
		}
		posts, total, totalPages, err := archiveList(withTag, archivePageParam(c), keyPerPageTaxonomy)
		if err != nil {
			return err
		}
		term.PostCount = total
		related, err := archiveTagsByCount(archiveRelatedMax, withTag, term.ID)
		if err != nil {
			return err
		}
		return archiveRender(c, tm, "tag.html", term.Name, map[string]interface{}{
			"Term":       term,
			"Related":    related,
			"Posts":      posts,
			"Page":       archivePageParam(c),
			"TotalPages": totalPages,
			"Total":      total,
		})
	}
}

func archiveAuthor(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		var user core.User
		if err := datapkg.DB.Where("username = ?", c.Param("username")).First(&user).Error; err != nil {
			return echo.ErrNotFound
		}
		byAuthor := func(tx *gorm.DB) *gorm.DB { return tx.Where("posts.author_id = ?", user.ID) }
		posts, total, totalPages, err := archiveList(byAuthor, archivePageParam(c), keyPerPageAuthor)
		if err != nil {
			return err
		}
		var topRows []struct {
			CategoryID uint
			Cnt        int64
		}
		err = byAuthor(archivePublicPosts(datapkg.DB)).
			Select("post_categories.category_id AS category_id, COUNT(DISTINCT posts.id) AS cnt").
			Joins("JOIN post_categories ON post_categories.post_id = posts.id").
			Joins("JOIN categories ON categories.id = post_categories.category_id").
			Where("categories.hide_from_list = ?", false).
			Group("post_categories.category_id").Order("cnt DESC, post_categories.category_id ASC").
			Limit(archiveTopCatsMax).Scan(&topRows).Error
		if err != nil {
			return err
		}
		top := []core.Category{}
		if len(topRows) > 0 {
			ids := make([]uint, len(topRows))
			for i, row := range topRows {
				ids[i] = row.CategoryID
			}
			var cats []core.Category
			if err := datapkg.DB.Where("id IN ?", ids).Find(&cats).Error; err != nil {
				return err
			}
			byID := make(map[uint]core.Category, len(cats))
			for _, cat := range cats {
				byID[cat.ID] = cat
			}
			for _, row := range topRows {
				if cat, ok := byID[row.CategoryID]; ok {
					cat.PostCount = row.Cnt
					top = append(top, cat)
				}
			}
		}
		authors, err := archiveAuthors()
		if err != nil {
			return err
		}
		others := []archiveAuthorEntry{}
		for _, a := range authors {
			if a.ID != user.ID && len(others) < archiveSideMax {
				others = append(others, a)
			}
		}
		shown := newArchiveAuthor(user, total)
		return archiveRender(c, tm, "author.html", user.Username, map[string]interface{}{
			"Author":        shown.User,
			"AuthorInitial": shown.Initial,
			"Posts":         posts,
			"Page":          archivePageParam(c),
			"TotalPages":    totalPages,
			"Total":         total,
			"TopCategories": top,
			"OtherAuthors":  others,
		})
	}
}

func archiveIndex(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		all, err := archiveAllCategories()
		if err != nil {
			return err
		}
		counts, err := archiveCategoryCounts(all)
		if err != nil {
			return err
		}
		tags, err := archiveTagsByCount(0, nil, 0)
		if err != nil {
			return err
		}
		months, err := archiveMonths()
		if err != nil {
			return err
		}
		authors, err := archiveAuthors()
		if err != nil {
			return err
		}
		var total int64
		if err := archivePublicPosts(datapkg.DB).Count(&total).Error; err != nil {
			return err
		}
		return archiveRender(c, tm, "archive.html", "彙整", map[string]interface{}{
			"Categories": archiveVisibleTree(all, counts),
			"Tags":       tags,
			"Months":     months,
			"Authors":    authors,
			"Total":      total,
		})
	}
}

func archiveMonthParams(c echo.Context) (year, month int, ok bool) {
	if !archiveYearPattern.MatchString(c.Param("year")) || !archiveMonthPattern.MatchString(c.Param("month")) {
		return 0, 0, false
	}
	year, _ = strconv.Atoi(c.Param("year"))
	month, _ = strconv.Atoi(c.Param("month"))
	return year, month, year >= 1 && month >= 1 && month <= 12
}

func archiveMonthHandler(tm *theme.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		year, month, ok := archiveMonthParams(c)
		if !ok {
			return echo.ErrNotFound
		}
		start := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0)
		posts, total, totalPages, err := archiveList(func(tx *gorm.DB) *gorm.DB {
			return tx.Where(archivePostDate+" >= ? AND "+archivePostDate+" < ?", start, end)
		}, archivePageParam(c), keyPerPageTaxonomy)
		if err != nil {
			return err
		}
		months, err := archiveMonths()
		if err != nil {
			return err
		}
		if len(months) > 12 {
			months = months[:12]
		}
		return archiveRender(c, tm, "archive_month.html", strconv.Itoa(year)+" 年 "+strconv.Itoa(month)+" 月", map[string]interface{}{
			"Year":       year,
			"Month":      month,
			"Months":     months,
			"Posts":      posts,
			"Page":       archivePageParam(c),
			"TotalPages": totalPages,
			"Total":      total,
		})
	}
}

package data

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/searchtext"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const searchIndexBatch = 200

func registerSearchIndexCallbacks(db *gorm.DB) error {
	if err := db.Callback().Create().Before("gorm:create").Register("data:search_index:create", preparePostCreate); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").Register("data:search_index:update", preparePostUpdate); err != nil {
		return err
	}
	return nil
}

func isPostStatement(db *gorm.DB) bool {
	return db.Statement != nil && db.Statement.Schema != nil && db.Statement.Schema.ModelType == reflect.TypeOf(core.Post{})
}

var postSearchFields = []struct{ source, derived string }{{"title", "search_title"}, {"content", "search_content"}, {"excerpt", "search_excerpt"}}

func preparePostCreate(db *gorm.DB) {
	if db.Error != nil || !isPostStatement(db) {
		return
	}
	cols, restricted := db.Statement.SelectAndOmitColumns(true, false)
	allowed := func(name string) bool {
		if v, ok := cols[name]; ok {
			return v
		}
		return !restricted
	}
	updateMap := func(m map[string]interface{}) {
		for _, pair := range postSearchFields {
			if !allowed(pair.source) {
				continue
			}
			field := db.Statement.Schema.LookUpField(pair.source)
			if field == nil {
				continue
			}
			value, ok := mapValue(m, field.Name, field.DBName)
			if !ok {
				value = ""
			}
			s, ok := value.(string)
			if !ok {
				db.AddError(fmt.Errorf("search index requires string posts.%s", pair.source))
				return
			}
			m[pair.derived] = derived(pair.source, s)
			if explicitlySelected(db, pair.source) {
				db.Statement.Selects = appendUnique(db.Statement.Selects, pair.derived)
			}
		}
	}
	value := reflect.ValueOf(db.Statement.Dest)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return
	}
	if value.Kind() == reflect.Map {
		m, ok := db.Statement.Dest.(map[string]interface{})
		if ok {
			updateMap(m)
		}
		return
	}
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		for i := 0; i < value.Len(); i++ {
			setPostDerived(db, value.Index(i), allowed)
		}
		return
	}
	setPostDerived(db, value, allowed)
}

func setPostDerived(db *gorm.DB, value reflect.Value, allowed func(string) bool) {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return
	}
	for _, pair := range postSearchFields {
		if !allowed(pair.source) {
			continue
		}
		source := db.Statement.Schema.LookUpField(pair.source)
		derivedField := db.Statement.Schema.LookUpField(pair.derived)
		v, _ := source.ValueOf(db.Statement.Context, value)
		s, ok := v.(string)
		if !ok {
			db.AddError(fmt.Errorf("search index requires string posts.%s", pair.source))
			return
		}
		if err := derivedField.Set(db.Statement.Context, value, derived(pair.source, s)); err != nil {
			db.AddError(err)
			return
		}
		if explicitlySelected(db, pair.source) {
			db.Statement.Selects = appendUnique(db.Statement.Selects, pair.derived)
		}
	}
}

func preparePostUpdate(db *gorm.DB) {
	if db.Error != nil || !isPostStatement(db) {
		return
	}
	cols, restricted := db.Statement.SelectAndOmitColumns(false, true)
	allowed := func(name string) bool {
		if v, ok := cols[name]; ok {
			return v
		}
		return !restricted
	}
	queryValues := map[string]interface{}{}
	if values, ok := db.Statement.Dest.(map[string]interface{}); ok {
		for key, value := range values {
			queryValues[strings.ToLower(key)] = value
		}
	} else {
		value := reflect.ValueOf(db.Statement.Dest)
		for value.IsValid() && value.Kind() == reflect.Pointer {
			if value.IsNil() {
				break
			}
			value = value.Elem()
		}
		if value.IsValid() && value.Kind() == reflect.Struct {
			for _, pair := range postSearchFields {
				field := db.Statement.Schema.LookUpField(pair.source)
				if !allowed(field.DBName) {
					continue
				}
				v, zero := field.ValueOf(db.Statement.Context, value)
				if !zero || explicitlySelected(db, field.DBName) {
					queryValues[field.DBName] = v
				}
			}
		}
	}
	for _, pair := range postSearchFields {
		if !allowed(pair.source) {
			continue
		}
		v, exists := mapValue(queryValues, pair.source, pair.source)
		if !exists {
			continue
		}
		switch v.(type) {
		case clause.Expr, *clause.Expr, clause.NamedExpr:
			db.AddError(fmt.Errorf("search index cannot derive posts.%s from SQL expression", pair.source))
			return
		}
		s, ok := v.(string)
		if !ok {
			if v == nil {
				s = ""
			} else {
				db.AddError(fmt.Errorf("search index requires string posts.%s", pair.source))
				return
			}
		}
		queryValues[pair.derived] = derived(pair.source, s)
		if explicitlySelected(db, pair.source) {
			db.Statement.Selects = appendUnique(db.Statement.Selects, pair.derived)
		}
	}
	if values, ok := db.Statement.Dest.(map[string]interface{}); ok {
		for _, pair := range postSearchFields {
			if value, exists := queryValues[pair.derived]; exists && allowed(pair.source) {
				values[pair.derived] = value
			}
		}
	} else {
		value := reflect.ValueOf(db.Statement.Dest)
		for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
			if value.IsNil() {
				break
			}
			value = value.Elem()
		}
		if value.IsValid() && value.Kind() == reflect.Struct {
			values := map[string]interface{}{}
			for _, name := range db.Statement.Schema.DBNames {
				field := db.Statement.Schema.LookUpField(name)
				if field == nil || field.DBName == "" || !field.Updatable || !allowed(field.DBName) {
					continue
				}
				if field.AutoUpdateTime > 0 && !db.Statement.SkipHooks {
					continue
				}
				v, zero := field.ValueOf(db.Statement.Context, value)
				if explicitlySelected(db, field.DBName) || !zero {
					values[field.DBName] = v
				}
			}
			for _, pair := range postSearchFields {
				if v, ok := queryValues[pair.derived]; ok {
					values[pair.derived] = v
				}
			}
			db.Statement.Dest = values
		}
	}
}

func explicitlySelected(db *gorm.DB, field string) bool {
	for _, selected := range db.Statement.Selects {
		selected = strings.Trim(strings.TrimSpace(selected), "\"`")
		if selected == "*" {
			return true
		}
		if i := strings.LastIndexByte(selected, '.'); i >= 0 {
			selected = selected[i+1:]
		}
		if strings.EqualFold(selected, field) {
			return true
		}
		if f := db.Statement.Schema.LookUpField(selected); f != nil && strings.EqualFold(f.DBName, field) {
			return true
		}
	}
	return false
}
func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if strings.EqualFold(v, value) {
			return values
		}
	}
	return append(values, value)
}
func mapValue(m map[string]interface{}, names ...string) (interface{}, bool) {
	for key, value := range m {
		for _, name := range names {
			if strings.EqualFold(key, name) {
				return value, true
			}
		}
	}
	return nil, false
}
func derived(field, value string) string {
	if field == "title" {
		return searchtext.Fold(value)
	}
	return searchtext.Fold(searchtext.PlainText(value))
}

// FoldPostSearchFields is the canonical index projection for a post row.
func FoldPostSearchFields(title, content, excerpt string) (string, string, string) {
	return derived("title", title), derived("content", content), derived("excerpt", excerpt)
}

// PostContentUpdate carries one locked media rewrite row.
type PostContentUpdate struct {
	ID               uint
	Content, Excerpt *string
}

// UpdatePostContentAndSearchIndex atomically updates source text and derived values in one batch.
func UpdatePostContentAndSearchIndex(tx *gorm.DB, rows []PostContentUpdate) error {
	if len(rows) == 0 {
		return nil
	}
	values, args := make([]string, 0, len(rows)), make([]interface{}, 0, len(rows)*5)
	for _, row := range rows {
		contentSource, excerptSource := "", ""
		if row.Content != nil {
			contentSource = *row.Content
		}
		if row.Excerpt != nil {
			excerptSource = *row.Excerpt
		}
		_, content, excerpt := FoldPostSearchFields("", contentSource, excerptSource)
		values = append(values, "(?::bigint, ?::text, ?::text, ?::text, ?::text)")
		args = append(args, row.ID, row.Content, row.Excerpt, content, excerpt)
	}
	stmt := `UPDATE posts AS p SET content = v.content, excerpt = v.excerpt, search_content = v.search_content, search_excerpt = v.search_excerpt FROM (VALUES ` + strings.Join(values, ",") +
		`) AS v(id, content, excerpt, search_content, search_excerpt) WHERE p.id = v.id`
	return tx.Exec(stmt, args...).Error
}

// RebuildPostSearchIndex recomputes the private derived columns without changing updated_at.
func RebuildPostSearchIndex(tx *gorm.DB, id uint) error {
	var row struct{ Title, Content, Excerpt string }
	if err := tx.Raw(`SELECT COALESCE(title, '') AS title, COALESCE(content, '') AS content, COALESCE(excerpt, '') AS excerpt FROM posts WHERE id = ?`, id).Scan(&row).Error; err != nil {
		return err
	}
	return updatePostSearchIndexBatch(tx, []struct {
		ID                      uint
		Title, Content, Excerpt string
	}{{id, row.Title, row.Content, row.Excerpt}})
}

func backfillPostSearchIndex(tx *gorm.DB) error {
	var last uint
	for {
		var rows []struct {
			ID                      uint
			Title, Content, Excerpt string
		}
		if err := tx.Raw(`SELECT id, COALESCE(title, '') AS title, COALESCE(content, '') AS content, COALESCE(excerpt, '') AS excerpt FROM posts WHERE id > ? ORDER BY id LIMIT ?`, last, searchIndexBatch).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if err := updatePostSearchIndexBatch(tx, rows); err != nil {
			return err
		}
		last = rows[len(rows)-1].ID
	}
}

func updatePostSearchIndexBatch(tx *gorm.DB, rows []struct {
	ID                      uint
	Title, Content, Excerpt string
}) error {
	values, args := make([]string, 0, len(rows)), make([]interface{}, 0, len(rows)*4)
	for _, row := range rows {
		title, content, excerpt := FoldPostSearchFields(row.Title, row.Content, row.Excerpt)
		values = append(values, "(?::bigint, ?::text, ?::text, ?::text)")
		args = append(args, row.ID, title, content, excerpt)
	}
	stmt := `UPDATE posts AS p SET search_title = v.search_title, search_content = v.search_content, search_excerpt = v.search_excerpt FROM (VALUES ` + strings.Join(values, ",") +
		`) AS v(id, search_title, search_content, search_excerpt) WHERE p.id = v.id`
	if err := tx.Exec(stmt, args...).Error; err != nil {
		return fmt.Errorf("update search index batch: %w", err)
	}
	return nil
}

// RebuildAllPostSearchIndex recomputes every post index inside the caller's transaction.
func RebuildAllPostSearchIndex(tx *gorm.DB) error { return backfillPostSearchIndex(tx) }

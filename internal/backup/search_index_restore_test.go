package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
)

func TestSearchIndexRestoreRebuildsMissingAndUntrustedArchiveValues(t *testing.T) {
	cfg := restoreRegressionPostgres(t)
	t.Chdir(t.TempDir())
	if err := config.Save(&config.Config{Database: *cfg}); err != nil {
		t.Fatalf("write isolated restore config: %v", err)
	}
	author := core.User{Username: "restore-search-author", Email: "restore-search-author@example.test", Role: "admin", Provider: "local"}
	if err := data.DB.Create(&author).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	post := core.Post{Title: "Archive Source Truth", Slug: "archive-source-truth", Content: `<p>archive-visible-source-term</p>`, Excerpt: "archive-visible-excerpt", Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &now}
	if err := data.DB.Create(&post).Error; err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// The first archive simulates a version made before the derived columns
	// existed. Restore must accept its rows and rebuild the fields from source.
	legacyID := createSearchRestoreBackup(t, ctx)
	rewriteArchivedPostIndex(t, legacyID, post.ID, false)
	changeSearchRestoreSource(t, post.ID, "Live Before Legacy Restore", "live-before-legacy-restore")
	if _, err := Restore(ctx, RestoreRequest{ID: legacyID, Confirm: RestoreConfirmation}); err != nil {
		t.Fatalf("restore legacy archive without search fields: %v", err)
	}
	assertSearchRestoreSource(t, post.ID, "Archive Source Truth", []string{"archive source truth", "archive-visible-source-term", "archive-visible-excerpt"})

	// The second archive contains all three derived columns, but their values
	// are deliberately wrong. Product Restore must discard them and rebuild.
	corruptID := createSearchRestoreBackup(t, ctx)
	rewriteArchivedPostIndex(t, corruptID, post.ID, true)
	changeSearchRestoreSource(t, post.ID, "Live Before Corrupt Restore", "live-before-corrupt-restore")
	if _, err := Restore(ctx, RestoreRequest{ID: corruptID, Confirm: RestoreConfirmation}); err != nil {
		t.Fatalf("restore archive with corrupt carried search fields: %v", err)
	}
	assertSearchRestoreSource(t, post.ID, "Archive Source Truth", []string{"archive source truth", "archive-visible-source-term", "archive-visible-excerpt"})

	// Partial restores must not rebuild or otherwise update the live posts table.
	// This trigger makes any accidental UPDATE fail the real Restore operation.
	const optionKey = "search-lifecycle-partial-restore"
	option := core.Option{Key: optionKey, Value: "archived setting"}
	if err := data.DB.Create(&option).Error; err != nil {
		t.Fatal(err)
	}
	settingsID := createSearchRestoreBackupContents(t, ctx, []string{ContentSettings})
	if err := data.DB.Model(&core.Option{}).Where("key = ?", optionKey).Update("value", "live setting").Error; err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(attachmentsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	attachmentPath := filepath.Join(attachmentsDir, "partial-restore-search-fixture.txt")
	if err := os.WriteFile(attachmentPath, []byte("archived attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachmentsID := createSearchRestoreBackupContents(t, ctx, []string{ContentAttachments})
	if err := os.WriteFile(attachmentPath, []byte("live attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := data.DB.Exec(`CREATE OR REPLACE FUNCTION jelly_search_lifecycle_reject_post_update()
		RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'partial restore updated posts'; END $$`).Error; err != nil {
		t.Fatalf("create posts update guard function: %v", err)
	}
	if err := data.DB.Exec(`CREATE TRIGGER search_lifecycle_reject_post_update
		BEFORE UPDATE ON posts FOR EACH ROW EXECUTE FUNCTION jelly_search_lifecycle_reject_post_update()`).Error; err != nil {
		t.Fatalf("create posts update guard trigger: %v", err)
	}
	t.Cleanup(func() {
		_ = data.DB.Exec("DROP TRIGGER IF EXISTS search_lifecycle_reject_post_update ON posts").Error
		_ = data.DB.Exec("DROP FUNCTION IF EXISTS jelly_search_lifecycle_reject_post_update()").Error
	})
	for _, fixture := range []struct {
		name string
		id   int64
	}{{"settings-only", settingsID}, {"attachments-only", attachmentsID}} {
		if _, err := Restore(ctx, RestoreRequest{ID: fixture.id, Confirm: RestoreConfirmation}); err != nil {
			t.Fatalf("%s partial restore unexpectedly touched posts or failed: %v", fixture.name, err)
		}
		assertSearchRestoreSource(t, post.ID, "Archive Source Truth", []string{"archive source truth", "archive-visible-source-term", "archive-visible-excerpt"})
	}
	var restoredOption core.Option
	if err := data.DB.Where("key = ?", optionKey).Take(&restoredOption).Error; err != nil || restoredOption.Value != "archived setting" {
		t.Fatalf("settings-only restore value=%q err=%v", restoredOption.Value, err)
	}
	if got, err := os.ReadFile(attachmentPath); err != nil || string(got) != "archived attachment" {
		t.Fatalf("attachments-only restore content=%q err=%v", got, err)
	}
}

func createSearchRestoreBackup(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	return createSearchRestoreBackupContents(t, ctx, []string{ContentDatabase})
}

func createSearchRestoreBackupContents(t *testing.T, ctx context.Context, contents []string) int64 {
	t.Helper()
	id, err := Run(ctx, Request{Contents: contents, Note: "search lifecycle restore fixture"})
	if err != nil {
		t.Fatalf("create backup fixture: %v", err)
	}
	return id
}

func changeSearchRestoreSource(t *testing.T, id uint, title, content string) {
	t.Helper()
	if err := data.DB.Model(&core.Post{}).Where("id = ?", id).Updates(map[string]any{
		"title": title, "content": "<p>" + content + "</p>", "excerpt": "live excerpt " + content,
	}).Error; err != nil {
		t.Fatalf("change live post before restore: %v", err)
	}
}

func assertSearchRestoreSource(t *testing.T, id uint, title string, terms []string) {
	t.Helper()
	var post core.Post
	if err := data.DB.First(&post, id).Error; err != nil {
		t.Fatalf("read restored post: %v", err)
	}
	if post.Title != title || post.SearchTitle != "archive source truth" || post.SearchContent != "archive-visible-source-term" || post.SearchExcerpt != "archive-visible-excerpt" {
		t.Fatalf("restored source/index mismatch: title=%q fields=(%q,%q,%q)", post.Title, post.SearchTitle, post.SearchContent, post.SearchExcerpt)
	}
	for _, term := range terms {
		var count int64
		if err := data.DB.Table("posts").Where("id = ? AND (strpos(search_title, ?) > 0 OR strpos(search_content, ?) > 0 OR strpos(search_excerpt, ?) > 0)", id, term, term, term).Count(&count).Error; err != nil {
			t.Fatalf("search restored term %q: %v", term, err)
		}
		if count != 1 {
			t.Errorf("restored source term %q matched %d posts, want 1", term, count)
		}
	}
	var stale int64
	if err := data.DB.Table("posts").Where("id = ? AND (strpos(search_title, 'live before') > 0 OR strpos(search_content, 'live-before') > 0 OR strpos(search_excerpt, 'live excerpt') > 0 OR strpos(search_title, 'corrupt carried') > 0)", id).Count(&stale).Error; err != nil {
		t.Fatal(err)
	}
	if stale != 0 {
		t.Fatalf("restore retained stale source or carried index values")
	}
}

func rewriteArchivedPostIndex(t *testing.T, backupID int64, postID uint, carryCorrupt bool) {
	t.Helper()
	var row record
	if err := data.DB.First(&row, backupID).Error; err != nil {
		t.Fatalf("load backup record: %v", err)
	}
	if row.FilePath == nil {
		t.Fatal("backup has no local archive path")
	}
	archiveBytes, err := os.ReadFile(*row.FilePath)
	if err != nil {
		t.Fatalf("read backup archive: %v", err)
	}
	dec, err := newDecryptor(bytes.NewReader(archiveBytes), "")
	if err != nil {
		t.Fatalf("decrypt backup archive: %v", err)
	}
	plain, err := io.ReadAll(dec)
	if err != nil {
		t.Fatalf("read backup payload: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		t.Fatalf("open backup payload: %v", err)
	}
	entries := make(map[string][]byte, len(reader.File))
	var manifest Manifest
	for _, file := range reader.File {
		f, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, readErr := io.ReadAll(f)
		closeErr := f.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read archive entry %s: %v", file.Name, errors.Join(readErr, closeErr))
		}
		entries[file.Name] = content
		if file.Name == manifestEntry {
			if err := json.Unmarshal(content, &manifest); err != nil {
				t.Fatalf("decode archive manifest: %v", err)
			}
		}
	}
	postFile := manifest.Tables["posts"].File
	lines := strings.Split(strings.TrimSuffix(string(entries[postFile]), "\n"), "\n")
	found := false
	for i, line := range lines {
		var archived map[string]any
		if err := json.Unmarshal([]byte(line), &archived); err != nil {
			t.Fatal(err)
		}
		if uint(archived["id"].(float64)) != postID {
			continue
		}
		found = true
		if carryCorrupt {
			archived["search_title"], archived["search_content"], archived["search_excerpt"] = "corrupt carried title", "corrupt carried content", "corrupt carried excerpt"
		} else {
			delete(archived, "search_title")
			delete(archived, "search_content")
			delete(archived, "search_excerpt")
		}
		encoded, err := json.Marshal(archived)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = string(encoded)
	}
	if !found {
		t.Fatalf("post %d was absent from archive table", postID)
	}
	entries[postFile] = []byte(strings.Join(lines, "\n") + "\n")
	postInfo := manifest.Tables["posts"]
	postInfo.Rows = int64(len(lines))
	postInfo.Size = int64(len(entries[postFile]))
	sum := sha256.Sum256(entries[postFile])
	postInfo.SHA256 = hex.EncodeToString(sum[:])
	manifest.Tables["posts"] = postInfo
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	entries[manifestEntry] = manifestBytes
	var payload bytes.Buffer
	zw := zip.NewWriter(&payload)
	for _, file := range reader.File {
		w, err := zw.CreateHeader(&file.FileHeader)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(entries[file.Name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	key, err := localKey(false)
	if err != nil {
		t.Fatalf("load archive encryption key: %v", err)
	}
	prefix, err := randomBytes(noncePrefixSize)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted bytes.Buffer
	enc, err := newEncryptor(&encrypted, key, keyModeLocal, make([]byte, saltSize), prefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(payload.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(*row.FilePath, encrypted.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	archiveSum := sha256.Sum256(encrypted.Bytes())
	if err := data.DB.Model(&record{}).Where("id = ?", backupID).Updates(map[string]any{
		"size_bytes": len(encrypted.Bytes()), "sha256": hex.EncodeToString(archiveSum[:]),
	}).Error; err != nil {
		t.Fatalf("update backup archive checksum: %v", err)
	}
}

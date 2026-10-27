package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"path"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/storage"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
)

var errMediaObjectExists = errors.New("media: object already exists")

func putMediaObject(ctx context.Context, drv storage.Driver, p string, data []byte, mime string) error {
	if _, err := drv.Stat(ctx, p); err == nil {
		return errMediaObjectExists
	} else if !mediaObjectMissing(err) {
		return fmt.Errorf("media: check object path: %w", err)
	}
	if exclusive, ok := drv.(interface {
		PutExclusive(context.Context, string, io.Reader, int64, string) error
	}); ok {
		if err := exclusive.PutExclusive(ctx, p, bytes.NewReader(data), int64(len(data)), mime); err != nil {
			return err
		}
		return nil
	}
	if err := drv.Put(ctx, p, bytes.NewReader(data), int64(len(data)), mime); err != nil {
		return err
	}
	return nil
}

func mediaObjectMissing(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"nosuchkey", "no such key", "no such file", "not found", "does not exist", "status code: 404", "status: 404"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

type mediaCountReader struct {
	r io.Reader
	n int64
}

func (r *mediaCountReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.n += int64(n)
	return n, err
}

func copyMediaObject(ctx context.Context, src, dst storage.Driver, sourcePath, destinationPath string, row mediaFileRow) error {
	if row.Size < 0 || row.Size == int64(^uint64(0)>>1) {
		return errors.New("media: source size is invalid")
	}
	if _, err := dst.Stat(ctx, destinationPath); err == nil {
		return errMediaObjectExists
	} else if !mediaObjectMissing(err) {
		return fmt.Errorf("media: check destination object: %w", err)
	}
	in, err := src.Open(ctx, sourcePath)
	if err != nil {
		return fmt.Errorf("media: open source object %q: %w", sourcePath, err)
	}
	defer in.Close()
	counted := &mediaCountReader{r: &io.LimitedReader{R: in, N: row.Size + 1}}
	digest := sha256.New()
	reader := io.TeeReader(counted, digest)
	if exclusive, ok := dst.(interface {
		PutExclusive(context.Context, string, io.Reader, int64, string) error
	}); ok {
		err = exclusive.PutExclusive(ctx, destinationPath, reader, row.Size, row.Mime)
	} else {
		err = dst.Put(ctx, destinationPath, reader, row.Size, row.Mime)
	}
	if err != nil {
		// A failed write does not establish ownership of the destination.
		return fmt.Errorf("media: write destination object: %w", err)
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		cleanupMediaObject(ctx, dst, destinationPath)
		return fmt.Errorf("media: finish source object read: %w", err)
	}
	if counted.n != row.Size {
		cleanupMediaObject(ctx, dst, destinationPath)
		return fmt.Errorf("media: source size mismatch (got %d, want %d)", counted.n, row.Size)
	}
	if row.Sha256 != "" && fmt.Sprintf("%x", digest.Sum(nil)) != row.Sha256 {
		cleanupMediaObject(ctx, dst, destinationPath)
		return errors.New("media: source checksum mismatch")
	}
	return nil
}

// insertMediaFile reserves its unique database key before putting bytes. The
// policy row lock serializes filename allocation per policy; object writes are
// compensated if the insert or transaction commit fails.
func insertMediaFile(ctx context.Context, in mediaStoreInput) (mediaFileRow, error) {
	drv, err := policyDriver(in.Policy, ctx)
	if err != nil {
		return mediaFileRow{}, err
	}
	preferredPath := in.Path
	if in.Policy.Driver == storage.DriverS3 {
		basePath := preferredPath
		if basePath == "" {
			basePath = time.Now().UTC().Format(mediaFolderLayout) + "/" + in.Prepared.Name
		}
		preferredPath, err = randomMediaObjectPath(in.Policy.ID, basePath)
		if err != nil {
			return mediaFileRow{}, err
		}
	}
	var row mediaFileRow
	var storedPath string
	err = mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
		var policyID int64
		if err := tx.Raw("SELECT id FROM media_policies WHERE id = ? AND deleted_at IS NULL FOR UPDATE", in.Policy.ID).Scan(&policyID).Error; err != nil {
			return err
		}
		if policyID == 0 {
			return gorm.ErrRecordNotFound
		}
		p, err := freeMediaPathOn(ctx, tx, drv, in.Policy.ID, in.Prepared.Name, preferredPath)
		if err != nil {
			return err
		}
		row = mediaFileRow{
			PolicyID: in.Policy.ID, GroupID: in.GroupID, OwnerID: actorPtr(in.Actor), Path: p,
			Filename: in.Prepared.Name, Mime: in.Prepared.Mime, Size: int64(len(in.Prepared.Data)),
			Width: in.Prepared.Width, Height: in.Prepared.Height, Alt: in.Alt, Sha256: in.Prepared.Sha256,
			Source: in.Source, ExifStripped: in.Prepared.ExifStripped,
		}
		if err := tx.Table("media_files").Create(&row).Error; err != nil {
			return err
		}
		if err := putMediaObject(ctx, drv, p, in.Prepared.Data, in.Prepared.Mime); err != nil {
			return err
		}
		storedPath = p
		return nil
	})
	if err != nil && storedPath != "" {
		cleanupMediaObject(ctx, drv, storedPath)
	}
	return row, err
}

func cleanupMediaObject(ctx context.Context, drv storage.Driver, p string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := drv.Delete(cleanupCtx, p); err != nil && !mediaObjectMissing(err) {
		log.Printf("media: cleanup failed object %q: %v", p, err)
	}
}

func freeMediaPathOn(ctx context.Context, db *gorm.DB, drv storage.Driver, policyID int64, name, preferred string) (string, error) {
	folder := time.Now().UTC().Format(mediaFolderLayout)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n <= mediaCollisionLimit; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		p := folder + "/" + candidate
		if preferred != "" {
			p = preferred
			if n > 1 {
				p = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(preferred, path.Ext(preferred)), n, path.Ext(preferred))
			}
		}
		var taken int64
		if err := db.WithContext(ctx).Raw("SELECT COUNT(*) FROM media_files WHERE policy_id = ? AND path = ?", policyID, p).Scan(&taken).Error; err != nil {
			return "", err
		}
		if taken > 0 {
			continue
		}
		if _, err := drv.Stat(ctx, p); err == nil {
			continue
		} else if !mediaObjectMissing(err) {
			return "", fmt.Errorf("media: check candidate path %q: %w", p, err)
		}
		return p, nil
	}
	return "", errors.New("media: no free file name")
}

func randomMediaObjectPath(policyID int64, name string) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("media: allocate unique object path: %w", err)
	}
	base := path.Base(name)
	if base == "." || base == "/" || base == "" {
		return "", errors.New("media: object filename is invalid")
	}
	return fmt.Sprintf("objects/%d/%s/%s", policyID, hex.EncodeToString(id[:]), base), nil
}

// replaceMediaObject keeps local URLs stable and restores their prior bytes on
// a database failure. Remote drivers use immutable candidate objects and only
// switch the row after the candidate has been stored.
func replaceMediaObject(ctx context.Context, row mediaFileRow, policy mediaPolicyRow, prepared mediaPrepared) (mediaFileRow, error) {
	drv, err := policyDriver(policy, ctx)
	if err != nil {
		return mediaFileRow{}, err
	}
	if policy.Driver == storage.DriverLocal {
		var original mediaFileRow
		var oldBytes []byte
		written := false
		err := mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT * FROM media_files WHERE id = ? AND deleted_at IS NULL FOR UPDATE", row.ID).Scan(&original).Error; err != nil {
				return err
			}
			if original.ID == 0 || original.PolicyID != policy.ID {
				return gorm.ErrRecordNotFound
			}
			limit := original.Size
			if limit < 1 {
				limit = 1
			}
			oldBytes, err = readDriverObject(ctx, drv, original.Path, limit)
			if err != nil {
				return fmt.Errorf("media: read original before replacement: %w", err)
			}
			if original.Sha256 != "" && mediaSHA256(oldBytes) != original.Sha256 {
				return errors.New("media: original object checksum does not match metadata")
			}
			if err := drv.Put(ctx, original.Path, bytes.NewReader(prepared.Data), int64(len(prepared.Data)), prepared.Mime); err != nil {
				return err
			}
			written = true
			res := tx.Table("media_files").Where("id = ? AND policy_id = ? AND deleted_at IS NULL", original.ID, policy.ID).Updates(mediaReplacementUpdates(prepared))
			if res.Error == nil && res.RowsAffected == 1 {
				return nil
			}
			updateErr := res.Error
			if updateErr == nil {
				updateErr = gorm.ErrRecordNotFound
			}
			restoreErr := restoreLocalMediaBytes(ctx, drv, original.Path, oldBytes, original.Mime)
			if restoreErr == nil {
				written = false
			}
			return errors.Join(updateErr, restoreErr)
		})
		if err != nil && written {
			if restoreErr := restoreLocalMediaReplacement(ctx, drv, original, oldBytes, prepared); restoreErr != nil {
				return mediaFileRow{}, errors.Join(err, restoreErr)
			}
		}
		if err != nil {
			return mediaFileRow{}, err
		}
		applyMediaReplacement(&original, prepared)
		return original, nil
	}
	path, err := freeReplacementPath(ctx, drv, row, policy.ID)
	if err != nil {
		return mediaFileRow{}, err
	}
	if err := putMediaObject(ctx, drv, path, prepared.Data, prepared.Mime); err != nil {
		return mediaFileRow{}, err
	}
	updated, err := updateMediaReplacement(ctx, row, path, prepared)
	if err != nil {
		cleanupMediaObject(ctx, drv, path)
		return mediaFileRow{}, err
	}
	if err := drv.Delete(ctx, row.Path); err != nil && !mediaObjectMissing(err) {
		return updated, fmt.Errorf("media: replacement committed but old object cleanup failed: %w", err)
	}
	return updated, nil
}

func updateMediaReplacement(ctx context.Context, row mediaFileRow, objectPath string, prepared mediaPrepared) (mediaFileRow, error) {
	updates := mediaReplacementUpdates(prepared)
	updates["path"] = objectPath
	err := mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Table("media_files").Where("id = ? AND deleted_at IS NULL", row.ID).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return mediaFileRow{}, err
	}
	row.Path = objectPath
	applyMediaReplacement(&row, prepared)
	return row, nil
}

func mediaReplacementUpdates(prepared mediaPrepared) map[string]any {
	return map[string]any{"size": int64(len(prepared.Data)), "sha256": prepared.Sha256, "width": prepared.Width, "height": prepared.Height, "exif_stripped": prepared.ExifStripped, "updated_at": time.Now()}
}

func applyMediaReplacement(row *mediaFileRow, prepared mediaPrepared) {
	row.Size, row.Sha256 = int64(len(prepared.Data)), prepared.Sha256
	row.Width, row.Height, row.ExifStripped = prepared.Width, prepared.Height, prepared.ExifStripped
	row.UpdatedAt = time.Now()
}

func restoreLocalMediaBytes(ctx context.Context, drv storage.Driver, objectPath string, data []byte, mime string) error {
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := drv.Put(restoreCtx, objectPath, bytes.NewReader(data), int64(len(data)), mime); err != nil {
		return fmt.Errorf("media: restore original object: %w", err)
	}
	return nil
}

// restoreLocalMediaReplacement compensates a failed DB commit while holding
// the row lock again. It never overwrites bytes from a later successful edit.
func restoreLocalMediaReplacement(ctx context.Context, drv storage.Driver, original mediaFileRow, oldBytes []byte, prepared mediaPrepared) error {
	restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return mediaDB(restoreCtx).Transaction(func(tx *gorm.DB) error {
		var current mediaFileRow
		if err := tx.Raw("SELECT * FROM media_files WHERE id = ? AND deleted_at IS NULL FOR UPDATE", original.ID).Scan(&current).Error; err != nil {
			return err
		}
		if current.ID == 0 || current.Path != original.Path || current.Size != original.Size || current.Sha256 != original.Sha256 {
			return nil
		}
		limit := int64(len(prepared.Data))
		if limit < current.Size {
			limit = current.Size
		}
		if limit < 1 {
			limit = 1
		}
		currentBytes, err := readDriverObject(ctx, drv, current.Path, limit)
		if err != nil {
			return err
		}
		currentHash := mediaSHA256(currentBytes)
		if currentHash == mediaSHA256(oldBytes) {
			return nil
		}
		if currentHash != prepared.Sha256 {
			return errors.New("media: cannot restore original because the object changed concurrently")
		}
		return restoreLocalMediaBytes(ctx, drv, current.Path, oldBytes, original.Mime)
	})
}

func freeReplacementPath(ctx context.Context, drv storage.Driver, row mediaFileRow, policyID int64) (string, error) {
	for n := 0; n < mediaCollisionLimit; n++ {
		candidate, err := randomMediaObjectPath(policyID, row.Filename)
		if err != nil {
			return "", err
		}
		if _, err := drv.Stat(ctx, candidate); err == nil {
			continue
		} else if !mediaObjectMissing(err) {
			return "", err
		}
		return candidate, nil
	}
	return "", errors.New("media: cannot allocate replacement object")
}

func moveLegacyMedia(c echo.Context, oldName, newName string) error {
	ctx := c.Request().Context()
	oldRow, policy, err := legacyMediaRow(ctx, oldName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(404, map[string]string{"error": "File not found"})
		}
		return mediaServerError(c, "load legacy file", err)
	}
	if policy.Driver != storage.DriverLocal {
		return c.JSON(409, map[string]string{"error": "File is not stored in the local uploads folder"})
	}
	newName = sanitizeFilename(newName)
	if newName == "" || newName == oldName {
		return c.JSON(400, map[string]string{"error": "Invalid filename"})
	}
	if path.Ext(newName) != path.Ext(oldName) {
		return c.JSON(415, map[string]string{"error": "Renaming must keep the file type"})
	}
	newPath := newName
	drv, err := policyDriver(policy, ctx)
	if err != nil {
		return mediaStoreError(c, err)
	}
	if _, err := drv.Stat(ctx, newPath); err == nil {
		return c.JSON(409, map[string]string{"error": "A file with that name already exists"})
	} else if !mediaObjectMissing(err) {
		return mediaStoreError(c, err)
	}
	if err := copyMediaObject(ctx, drv, drv, oldRow.Path, newPath, oldRow); err != nil {
		if errors.Is(err, errMediaObjectExists) {
			return c.JSON(409, map[string]string{"error": "A file with that name already exists"})
		}
		return mediaStoreError(c, err)
	}
	oldURL := storage.NewLocal(uploadsRoot).URL(oldRow.Path)
	newURL := storage.NewLocal(uploadsRoot).URL(newPath)
	err = mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("media_files").Where("id = ? AND path = ? AND deleted_at IS NULL", oldRow.ID, oldRow.Path).
			Updates(map[string]any{"path": newPath, "filename": newName, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		return rewriteKnownMediaReferences(tx, oldURL, newURL)
	})
	if err != nil {
		cleanupMediaObject(ctx, drv, newPath)
		return mediaServerError(c, "rename legacy file", err)
	}
	if err := drv.Delete(ctx, oldRow.Path); err != nil && !mediaObjectMissing(err) {
		return c.JSON(500, map[string]string{"error": "Rename committed but the old file could not be removed"})
	}
	return c.JSON(200, map[string]string{"message": "Renamed"})
}

func rewriteKnownMediaReferences(tx *gorm.DB, oldURL, newURL string) error {
	oldURL, newURL = strings.ReplaceAll(oldURL, "\\", "%5C"), strings.ReplaceAll(newURL, "\\", "%5C")
	var lastID uint
	for {
		var posts []struct {
			ID      uint
			Content sql.NullString
			Excerpt sql.NullString
		}
		if err := tx.Raw(`SELECT id, content, excerpt FROM posts WHERE id > ? AND (strpos(content, ?) > 0 OR strpos(excerpt, ?) > 0) ORDER BY id LIMIT 200 FOR UPDATE`, lastID, oldURL, oldURL).Scan(&posts).Error; err != nil {
			return fmt.Errorf("lock posts with media references: %w", err)
		}
		if len(posts) == 0 {
			break
		}
		updates := make([]data.PostContentUpdate, 0, len(posts))
		for _, post := range posts {
			var content, excerpt *string
			if post.Content.Valid {
				next := strings.ReplaceAll(post.Content.String, oldURL, newURL)
				content = &next
			}
			if post.Excerpt.Valid {
				next := strings.ReplaceAll(post.Excerpt.String, oldURL, newURL)
				excerpt = &next
			}
			updates = append(updates, data.PostContentUpdate{ID: post.ID, Content: content, Excerpt: excerpt})
			lastID = post.ID
		}
		if err := data.UpdatePostContentAndSearchIndex(tx, updates); err != nil {
			return fmt.Errorf("rewrite post media references: %w", err)
		}
	}
	for _, col := range []string{"source", "cover"} {
		query := fmt.Sprintf("UPDATE posts SET %s = replace(%s, ?, ?) WHERE strpos(%s, ?) > 0", col, col, col)
		if err := tx.Exec(query, oldURL, newURL, oldURL).Error; err != nil {
			return fmt.Errorf("rewrite posts.%s media URL: %w", col, err)
		}
	}
	if err := tx.Exec("UPDATE products SET description = replace(description, ?, ?) WHERE strpos(description, ?) > 0", oldURL, newURL, oldURL).Error; err != nil {
		return fmt.Errorf("rewrite product descriptions: %w", err)
	}
	if err := tx.Exec("UPDATE products SET images = replace(images::text, ?, ?)::jsonb WHERE strpos(images::text, ?) > 0", oldURL, newURL, oldURL).Error; err != nil {
		return fmt.Errorf("rewrite product image URLs: %w", err)
	}
	for _, table := range []string{"categories", "tags"} {
		query := fmt.Sprintf("UPDATE %s SET cover = replace(cover, ?, ?) WHERE strpos(cover, ?) > 0", table)
		if err := tx.Exec(query, oldURL, newURL, oldURL).Error; err != nil {
			return fmt.Errorf("rewrite %s media URL: %w", table, err)
		}
	}
	return nil
}

func rewriteForMediaFile(tx *gorm.DB, row mediaFileRow, oldDriver, newDriver string, oldPath string) error {
	oldURL := ""
	if oldDriver == storage.DriverLocal {
		oldURL = storage.NewLocal(uploadsRoot).URL(oldPath)
	} else {
		oldURL = "/media/" + fmt.Sprint(row.ID)
	}
	newURL := ""
	if newDriver == storage.DriverLocal {
		newURL = storage.NewLocal(uploadsRoot).URL(row.Path)
	}
	if newURL == "" {
		newURL = "/media/" + fmt.Sprint(row.ID)
	}
	if oldURL == newURL {
		return nil
	}
	return rewriteKnownMediaReferences(tx, oldURL, newURL)
}

func mediaStorageSame(a, b mediaPolicyRow) bool {
	if a.Driver != b.Driver {
		return false
	}
	switch a.Driver {
	case storage.DriverLocal:
		// policyDriver uses distinct fixed roots for backups and attachments.
		return (a.Purpose == "backup") == (b.Purpose == "backup")
	case storage.DriverS3:
		normalize := func(raw string) (*storage.S3, bool) {
			var cfg mediaS3StoredConfig
			if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
				return nil, false
			}
			drv, err := storage.NewS3WithConfig(storage.S3Config{
				Endpoint: cfg.Endpoint, Bucket: cfg.Bucket, Prefix: cfg.Prefix, Secure: cfg.Secure,
			})
			if err != nil {
				return nil, false
			}
			// S3 exposes the normalized endpoint without its scheme. Match the
			// constructor's scheme override when comparing the transport as well.
			secure := cfg.Secure
			endpoint := strings.ToLower(strings.TrimSpace(cfg.Endpoint))
			if strings.HasPrefix(endpoint, "https://") {
				secure = true
			} else if strings.HasPrefix(endpoint, "http://") {
				secure = false
			}
			return drv, secure
		}
		left, leftSecure := normalize(a.Config)
		right, rightSecure := normalize(b.Config)
		return left != nil && right != nil && left.Endpoint == right.Endpoint &&
			left.Bucket == right.Bucket && left.Prefix == right.Prefix && leftSecure == rightSecure
	default:
		return mediaPluginDriver.MatchString(a.Driver)
	}
}

func verifyMediaMoveTarget(ctx context.Context, drv storage.Driver, row mediaFileRow) error {
	if row.Size < 0 || row.Size == int64(^uint64(0)>>1) {
		return errors.New("media: target size is invalid")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	in, err := drv.Open(ctx, row.Path)
	if err != nil {
		return fmt.Errorf("media: open target object %q: %w", row.Path, err)
	}
	defer in.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(in, row.Size+1))
	if err != nil {
		return fmt.Errorf("media: read target object %q: %w", row.Path, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if size != row.Size {
		return fmt.Errorf("media: target size mismatch (got %d, want %d)", size, row.Size)
	}
	if row.Sha256 != "" && fmt.Sprintf("%x", digest.Sum(nil)) != row.Sha256 {
		return errors.New("media: target checksum mismatch")
	}
	return nil
}

func mediaMoveDestinationPath(ctx context.Context, tx *gorm.DB, drv storage.Driver, policyID int64, row mediaFileRow, sameStorage bool) (string, bool, error) {
	var rowCollision int64
	if err := tx.Raw("SELECT COUNT(*) FROM media_files WHERE policy_id = ? AND path = ?", policyID, row.Path).Scan(&rowCollision).Error; err != nil {
		return "", false, err
	}
	objectCollision := false
	if !sameStorage {
		if _, err := drv.Stat(ctx, row.Path); err == nil {
			objectCollision = true
		} else if !mediaObjectMissing(err) {
			return "", false, fmt.Errorf("media: check target object %q: %w", row.Path, err)
		}
	}
	if rowCollision == 0 && !objectCollision {
		return row.Path, false, nil
	}
	preferred, err := randomMediaObjectPath(policyID, row.Filename)
	if err != nil {
		return "", false, err
	}
	newPath, err := freeMediaPathOn(ctx, tx, drv, policyID, row.Filename, preferred)
	if err != nil {
		return "", false, err
	}
	return newPath, true, nil
}

func moveMediaPolicy(ctx context.Context, fromID, toID int64) (int64, error) {
	var src, dst mediaPolicyRow
	if err := loadMediaPolicyInto(ctx, fromID, &src); err != nil {
		return 0, err
	}
	if err := loadMediaPolicyInto(ctx, toID, &dst); err != nil {
		return 0, err
	}
	if src.Purpose != dst.Purpose || src.Purpose != mediaPurposeMedia {
		return 0, errors.New("media: policies must both be attachment policies")
	}
	srcDriver, err := policyDriver(src, ctx)
	if err != nil {
		return 0, err
	}
	dstDriver, err := policyDriver(dst, ctx)
	if err != nil {
		return 0, err
	}
	sameStorage := mediaStorageSame(src, dst)
	var moved int64
	var copied []string
	var originals []string
	err = mediaDB(ctx).Transaction(func(tx *gorm.DB) error {
		ids := []int64{fromID, toID}
		if ids[0] > ids[1] {
			ids[0], ids[1] = ids[1], ids[0]
		}
		for _, id := range ids {
			var locked int64
			if err := tx.Raw("SELECT id FROM media_policies WHERE id = ? AND deleted_at IS NULL FOR UPDATE", id).Scan(&locked).Error; err != nil {
				return err
			}
			if locked == 0 {
				return gorm.ErrRecordNotFound
			}
		}
		var rows []mediaFileRow
		if err := tx.Table("media_files").Where("policy_id = ?", fromID).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return err
			}
			destinationPath, pathChanged, err := mediaMoveDestinationPath(ctx, tx, dstDriver, toID, row, sameStorage)
			if err != nil {
				return err
			}
			if !sameStorage || pathChanged {
				if err := copyMediaObject(ctx, srcDriver, dstDriver, row.Path, destinationPath, row); err != nil {
					return fmt.Errorf("media: move object %q: %w", row.Path, err)
				}
				copied = append(copied, destinationPath)
				if !sameStorage {
					originals = append(originals, row.Path)
				}
			} else if dst.Driver == storage.DriverS3 {
				if err := verifyMediaMoveTarget(ctx, dstDriver, row); err != nil {
					return err
				}
			}
			newRow := row
			newRow.Path = destinationPath
			if err := rewriteForMediaFile(tx, newRow, src.Driver, dst.Driver, row.Path); err != nil {
				return err
			}
			res := tx.Table("media_files").Where("id = ? AND policy_id = ?", row.ID, fromID).Updates(map[string]any{"policy_id": toID, "path": destinationPath, "updated_at": time.Now()})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
			moved += res.RowsAffected
		}
		return nil
	})
	if err != nil {
		for _, p := range copied {
			cleanupMediaObject(ctx, dstDriver, p)
		}
		return 0, err
	}
	if moved > 0 && !sameStorage {
		var cleanupErrors []error
		for _, p := range originals {
			if err := srcDriver.Delete(ctx, p); err != nil && !mediaObjectMissing(err) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("%s: %w", p, err))
			}
		}
		if len(cleanupErrors) > 0 {
			return moved, fmt.Errorf("media: policy move committed but source cleanup failed: %w", errors.Join(cleanupErrors...))
		}
	}
	return moved, nil
}

func loadMediaPolicyInto(ctx context.Context, id int64, out *mediaPolicyRow) error {
	return mediaDB(ctx).Table("media_policies").Where("id = ? AND deleted_at IS NULL", id).Take(out).Error
}

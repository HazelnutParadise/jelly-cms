package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/HazelnutParadise/jelly-cms/internal/queue"
	"github.com/HazelnutParadise/jelly-cms/internal/syslog"
	"gorm.io/gorm"
)

type mediaFetchJobPayload struct {
	URL      string `json:"url"`
	PolicyID int64  `json:"policy_id"`
	ActorID  uint   `json:"actor_id"`
}

type mediaMoveJobPayload struct {
	FromPolicyID int64 `json:"from_policy_id"`
	ToPolicyID   int64 `json:"to_policy_id"`
	ActorID      uint  `json:"actor_id"`
}

func init() {
	RegisterSystemJobHandler("media.fetch_url", runMediaFetchJob)
	RegisterSystemJobHandler("media.fetch", runMediaFetchJob)
	RegisterSystemJobHandler("media.move_policy", runMediaMoveJob)
}

func runMediaFetchJob(ctx context.Context, db *gorm.DB, job queue.Job) error {
	if db == nil {
		return errors.New("media fetch: database is unavailable")
	}
	var payload mediaFetchJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.PolicyID <= 0 || payload.URL == "" {
		return errors.New("media fetch: job payload is invalid")
	}
	ctx, cancel := mediaJobContext(ctx)
	defer cancel()
	ctx = mediaWithDB(ctx, db)
	policy, err := loadMediaPolicy(ctx, payload.PolicyID)
	if err != nil {
		return fmt.Errorf("media fetch: load destination policy: %w", err)
	}
	if policy.Purpose != mediaPurposeMedia {
		return errors.New("media fetch: destination is not an attachment policy")
	}
	res := fetchMediaURL(ctx, policy, payload.URL, mediaMaxBytes(ctx), payload.ActorID)
	syslog.Audit(ctx, payload.ActorID, "media.fetch.result", map[string]any{
		"job_id": job.ID, "policy_id": payload.PolicyID, "status": res.Status,
	})
	if res.Status == "stored" {
		return nil
	}
	if res.Status == "blocked" || res.Status == "rejected" {
		return nil
	}
	return fmt.Errorf("media fetch: %s", res.Status)
}

func runMediaMoveJob(ctx context.Context, db *gorm.DB, job queue.Job) error {
	if db == nil {
		return errors.New("media move: database is unavailable")
	}
	var payload mediaMoveJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.FromPolicyID <= 0 || payload.ToPolicyID <= 0 {
		return errors.New("media move: job payload is invalid")
	}
	ctx, cancel := mediaJobContext(ctx)
	defer cancel()
	ctx = mediaWithDB(ctx, db)
	moved, err := moveMediaPolicy(ctx, payload.FromPolicyID, payload.ToPolicyID)
	if err != nil {
		return err
	}
	if moved == 0 && job.Attempts > 1 {
		return errors.New("media move: a previous attempt may have committed the move before source cleanup failed")
	}
	syslog.Audit(ctx, payload.ActorID, "media.policy.move", map[string]any{"from_policy_id": payload.FromPolicyID, "to_policy_id": payload.ToPolicyID, "count": moved})
	return nil
}

func validateMediaFetchURL(raw string) error {
	if len(raw) == 0 || len(raw) > 4096 || strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("URL is invalid")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("only absolute http and https URLs without credentials or fragments are accepted")
	}
	return nil
}

func mediaFetchDedupeKey(policyID int64, actor uint, raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("media.fetch:%d:%d:%s", policyID, actor, hex.EncodeToString(sum[:]))
}

func mediaMoveDedupeKey(ctx context.Context, fromID, toID int64) (string, error) {
	var state struct {
		Count   int64
		MaxID   int64
		Updated time.Time
	}
	if err := mediaDB(ctx).Table("media_files").Select("COUNT(*) AS count, COALESCE(MAX(id),0) AS max_id, COALESCE(MAX(updated_at), TIMESTAMPTZ 'epoch') AS updated").Where("policy_id = ?", fromID).Scan(&state).Error; err != nil {
		return "", err
	}
	return fmt.Sprintf("media.move:%d:%d:%d:%d:%d", fromID, toID, state.Count, state.MaxID, state.Updated.UnixNano()), nil
}

func queueMediaFetch(ctx context.Context, payload mediaFetchJobPayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return EnqueueSystemJob(ctx, data.DB, queue.Job{
		Queue: queue.QueueMedia, Kind: "media.fetch_url", DedupeKey: mediaFetchDedupeKey(payload.PolicyID, payload.ActorID, payload.URL),
		Payload: encoded, MaxAttempts: queue.DefaultMaxAttempts,
	})
}

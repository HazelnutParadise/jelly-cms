package queue

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisQueuePrefix = "jelly:queue:v1:"
const redisQueueGroup = "jelly-workers"
const redisQueueRecordTTL = 14 * 24 * time.Hour

type redisQueue struct{ client *redis.Client }

func newRedisQueue(raw string) (Queue, error) {
	opt, err := redis.ParseURL(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("queue: parse redis URL: %w", err)
	}
	return &redisQueue{client: redis.NewClient(opt)}, nil
}
func redisQueueName(q string) string { return base64.RawURLEncoding.EncodeToString([]byte(q)) }
func (q *redisQueue) stream(name string) string {
	return redisQueuePrefix + redisQueueName(name) + ":stream"
}
func (q *redisQueue) scheduled(name string) string {
	return redisQueuePrefix + redisQueueName(name) + ":scheduled"
}
func redisJobKey(id string) string {
	return redisQueuePrefix + "job:" + base64.RawURLEncoding.EncodeToString([]byte(id))
}
func redisDedupeKey(key string) string {
	return redisQueuePrefix + "dedupe:" + base64.RawURLEncoding.EncodeToString([]byte(key))
}
func (q *redisQueue) ensureGroup(ctx context.Context, queue string) error {
	err := q.client.XGroupCreateMkStream(ctx, q.stream(queue), redisQueueGroup, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("queue: redis create consumer group: %w", err)
	}
	return nil
}

var redisEnqueueScript = redis.NewScript(`if ARGV[1]~='' then local old=redis.call('GET',KEYS[3]); if old then return old end end; if redis.call('EXISTS',KEYS[1])==1 then return redis.call('HGET',KEYS[1],'id') end; redis.call('HSET',KEYS[1],'id',ARGV[2],'job',ARGV[3],'queue',ARGV[4],'state','queued','attempts',ARGV[5],'max_attempts',ARGV[6],'run_at',ARGV[7],'last_error','','entry','','dedupe_key',ARGV[9]); redis.call('SADD',KEYS[4],ARGV[2]); if ARGV[1]~='' then redis.call('SET',KEYS[3],ARGV[2]) end; redis.call('ZADD',KEYS[2],ARGV[7],KEYS[1]); return ARGV[2]`)

func (q *redisQueue) Enqueue(ctx context.Context, j Job) (string, error) {
	if strings.TrimSpace(j.Queue) == "" || strings.TrimSpace(j.Kind) == "" {
		return "", errors.New("queue: job queue and kind are required")
	}
	if j.ID == "" {
		j.ID = newJobID()
	}
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = DefaultMaxAttempts
	}
	if j.RunAt.IsZero() {
		j.RunAt = time.Now().UTC()
	}
	if len(strings.TrimSpace(string(j.Payload))) == 0 {
		j.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(j.Payload) {
		return "", errors.New("queue: invalid job payload")
	}
	encoded, err := json.Marshal(j)
	if err != nil {
		return "", fmt.Errorf("queue: encode redis job: %w", err)
	}
	got, err := redisEnqueueScript.Run(ctx, q.client, []string{redisJobKey(j.ID), q.scheduled(j.Queue), redisDedupeKey(j.DedupeKey), redisQueuePrefix + "ids"}, j.DedupeKey, j.ID, string(encoded), j.Queue, j.Attempts, j.MaxAttempts, j.RunAt.UnixMilli(), int(redisQueueRecordTTL.Seconds()), j.DedupeKey).Text()
	if err != nil {
		return "", fmt.Errorf("queue: redis enqueue: %w", err)
	}
	return got, nil
}

var redisPublishDueScript = redis.NewScript(`local ids=redis.call('ZRANGEBYSCORE',KEYS[1],'-inf',ARGV[1],'LIMIT',0,ARGV[2]); local n=0; for _,jk in ipairs(ids) do local job=redis.call('HGET',jk,'job'); local id=redis.call('HGET',jk,'id'); if job and id then local sid=redis.call('XADD',KEYS[2],'*','id',id); redis.call('HSET',jk,'entry',sid); n=n+1 end; redis.call('ZREM',KEYS[1],jk) end; return n`)

func (q *redisQueue) publishDue(ctx context.Context, name string) error {
	_, err := redisPublishDueScript.Run(ctx, q.client, []string{q.scheduled(name), q.stream(name)}, time.Now().UnixMilli(), 100).Result()
	if err != nil {
		return fmt.Errorf("queue: publish scheduled redis jobs: %w", err)
	}
	return nil
}

func (q *redisQueue) Reserve(ctx context.Context, queues []string, worker string, lease time.Duration) (*Job, error) {
	if len(queues) == 0 {
		return nil, nil
	}
	if worker == "" {
		return nil, errors.New("queue: worker name is required")
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	for _, name := range queues {
		if err := q.ensureGroup(ctx, name); err != nil {
			return nil, err
		}
		if err := q.publishDue(ctx, name); err != nil {
			return nil, err
		}
		stream := q.stream(name)
		msgs, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: stream, Group: redisQueueGroup, Consumer: worker, MinIdle: lease, Start: "0-0", Count: 1}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("queue: reclaim redis lease: %w", err)
		}
		if len(msgs) == 0 {
			streams, readErr := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{Group: redisQueueGroup, Consumer: worker, Streams: []string{stream, ">"}, Count: 1, Block: time.Millisecond}).Result()
			if readErr != nil && !errors.Is(readErr, redis.Nil) {
				return nil, fmt.Errorf("queue: reserve redis stream: %w", readErr)
			}
			if len(streams) > 0 && len(streams[0].Messages) > 0 {
				msgs = streams[0].Messages
			}
		}
		if len(msgs) == 0 {
			continue
		}
		msg := msgs[0]
		id, _ := msg.Values["id"].(string)
		if id == "" {
			_ = q.client.XAck(ctx, stream, redisQueueGroup, msg.ID).Err()
			_ = q.client.XDel(ctx, stream, msg.ID).Err()
			continue
		}
		key := redisJobKey(id)
		attempts, err := q.client.HIncrBy(ctx, key, "attempts", 1).Result()
		if err != nil {
			return nil, fmt.Errorf("queue: count redis delivery: %w", err)
		}
		values, err := q.client.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, fmt.Errorf("queue: read redis job: %w", err)
		}
		if len(values) == 0 {
			_ = q.client.XAck(ctx, stream, redisQueueGroup, msg.ID).Err()
			_ = q.client.XDel(ctx, stream, msg.ID).Err()
			continue
		}
		var job Job
		if err := json.Unmarshal([]byte(values["job"]), &job); err != nil {
			return nil, fmt.Errorf("queue: decode redis job %s: %w", id, err)
		}
		max, _ := strconv.Atoi(values["max_attempts"])
		if max > 0 && attempts > int64(max) {
			pipe := q.client.TxPipeline()
			pipe.HSet(ctx, key, "state", "failed", "last_error", "delivery lease expired on final attempt", "finished_at", time.Now().UTC().Format(time.RFC3339Nano))
			pipe.XAck(ctx, stream, redisQueueGroup, msg.ID)
			pipe.XDel(ctx, stream, msg.ID)
			if _, err := pipe.Exec(ctx); err != nil {
				return nil, fmt.Errorf("queue: fail expired redis job: %w", err)
			}
			continue
		}
		job.Attempts = int(attempts)
		pipe := q.client.TxPipeline()
		pipe.HSet(ctx, key, "state", "running", "locked_by", worker, "locked_until", time.Now().Add(lease).UTC().Format(time.RFC3339Nano), "entry", msg.ID)
		_, err = pipe.Exec(ctx)
		if err != nil {
			return nil, fmt.Errorf("queue: lease redis job: %w", err)
		}
		return &job, nil
	}
	return nil, nil
}

func (q *redisQueue) jobMeta(ctx context.Context, id string) (map[string]string, error) {
	m, err := q.client.HGetAll(ctx, redisJobKey(id)).Result()
	if err != nil {
		return nil, fmt.Errorf("queue: redis job metadata: %w", err)
	}
	if len(m) == 0 {
		return nil, ErrNotFound
	}
	return m, nil
}
func (q *redisQueue) Ack(ctx context.Context, id string) error {
	return q.finish(ctx, id, "done", "", nil)
}
func (q *redisQueue) ExtendLease(ctx context.Context, id, worker string, lease time.Duration) error {
	if lease <= 0 {
		return errors.New("queue: lease duration must be positive")
	}
	m, err := q.jobMeta(ctx, id)
	if err != nil {
		return err
	}
	if m["state"] != "running" || m["locked_by"] != worker || m["entry"] == "" {
		return errors.New("queue: lease is no longer owned by worker")
	}
	lockedUntil, parseErr := time.Parse(time.RFC3339Nano, m["locked_until"])
	if parseErr != nil || !time.Now().Before(lockedUntil) {
		return errors.New("queue: lease is no longer owned by worker")
	}
	ids, err := q.client.XClaimJustID(ctx, &redis.XClaimArgs{Stream: q.stream(m["queue"]), Group: redisQueueGroup, Consumer: worker, MinIdle: 0, Messages: []string{m["entry"]}}).Result()
	if err != nil {
		return fmt.Errorf("queue: renew redis stream lease: %w", err)
	}
	if len(ids) == 0 {
		return errors.New("queue: lease is no longer owned by worker")
	}
	if err := q.client.HSet(ctx, redisJobKey(id), "locked_until", time.Now().Add(lease).UTC().Format(time.RFC3339Nano)).Err(); err != nil {
		return fmt.Errorf("queue: renew redis lease metadata: %w", err)
	}
	return nil
}
func (q *redisQueue) finish(ctx context.Context, id, state, message string, retryAt *time.Time) error {
	m, err := q.jobMeta(ctx, id)
	if err != nil {
		return err
	}
	stream := q.stream(m["queue"])
	args := []any{state, message, time.Now().UTC().Format(time.RFC3339Nano)}
	jobJSON := ""
	if retryAt != nil {
		args = append(args, retryAt.UTC().UnixMilli())
		var job Job
		if err := json.Unmarshal([]byte(m["job"]), &job); err != nil {
			return fmt.Errorf("queue: decode redis job before retry: %w", err)
		}
		job.RunAt = retryAt.UTC()
		b, err := json.Marshal(job)
		if err != nil {
			return err
		}
		jobJSON = string(b)
	} else {
		args = append(args, 0)
	}
	script := redis.NewScript(`local entry=redis.call('HGET',KEYS[1],'entry'); if entry and entry~='' then redis.call('XACK',KEYS[2],ARGV[4],entry); redis.call('XDEL',KEYS[2],entry) end; redis.call('HSET',KEYS[1],'state',ARGV[1],'last_error',ARGV[2],'locked_by','','locked_until',''); if tonumber(ARGV[5])>0 then redis.call('HSET',KEYS[1],'run_at',ARGV[5],'job',ARGV[7]); redis.call('ZADD',KEYS[3],ARGV[5],KEYS[1]); redis.call('HSET',KEYS[1],'entry','','state','queued'); redis.call('PERSIST',KEYS[1]); if ARGV[9]~='' then redis.call('PERSIST',KEYS[4]) end else redis.call('HSET',KEYS[1],'finished_at',ARGV[3]); redis.call('EXPIRE',KEYS[1],ARGV[8]); if ARGV[9]~='' then redis.call('EXPIRE',KEYS[4],ARGV[8]) end end; return 1`)
	retention := redisQueueRecordTTL
	if state == "failed" {
		retention = FailedRetention
	}
	_, err = script.Run(ctx, q.client, []string{redisJobKey(id), stream, q.scheduled(m["queue"]), redisDedupeKey(m["dedupe_key"])}, state, message, args[2], redisQueueGroup, args[3], redisJobKey(id), jobJSON, int(retention.Seconds()), m["dedupe_key"]).Result()
	if err != nil {
		return fmt.Errorf("queue: update redis job: %w", err)
	}
	return nil
}
func (q *redisQueue) Fail(ctx context.Context, id string, cause error, retryAt *time.Time) error {
	m, err := q.jobMeta(ctx, id)
	if err != nil {
		return err
	}
	attempts, _ := strconv.Atoi(m["attempts"])
	max, _ := strconv.Atoi(m["max_attempts"])
	message := ""
	if cause != nil {
		message = cause.Error()
	}
	if retryAt != nil && attempts < max {
		return q.finish(ctx, id, "queued", message, retryAt)
	}
	return q.finish(ctx, id, "failed", message, nil)
}
func (q *redisQueue) Retry(ctx context.Context, id string) error {
	m, err := q.jobMeta(ctx, id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_ = q.client.HSet(ctx, redisJobKey(id), "attempts", 0).Err()
	var job Job
	if err := json.Unmarshal([]byte(m["job"]), &job); err != nil {
		return fmt.Errorf("queue: decode redis job before retry: %w", err)
	}
	job.Attempts = 0
	b, err := json.Marshal(job)
	if err != nil {
		return err
	}
	if err := q.client.HSet(ctx, redisJobKey(id), "job", string(b)).Err(); err != nil {
		return fmt.Errorf("queue: reset redis job attempts: %w", err)
	}
	return q.finish(ctx, id, "queued", m["last_error"], &now)
}

func (q *redisQueue) Stats(ctx context.Context) ([]QueueStats, error) {
	ids, err := q.client.SMembers(ctx, redisQueuePrefix+"ids").Result()
	if err != nil {
		return nil, fmt.Errorf("queue: redis stats ids: %w", err)
	}
	by := map[string]*QueueStats{}
	for _, id := range ids {
		m, e := q.client.HMGet(ctx, redisJobKey(id), "queue", "state").Result()
		if e != nil {
			return nil, e
		}
		if len(m) < 2 || m[0] == nil || m[1] == nil {
			_ = q.client.SRem(ctx, redisQueuePrefix+"ids", id).Err()
			continue
		}
		name, state := fmt.Sprint(m[0]), fmt.Sprint(m[1])
		s := by[name]
		if s == nil {
			s = &QueueStats{Queue: name}
			by[name] = s
		}
		switch state {
		case "queued":
			s.Queued++
		case "running":
			s.Running++
		case "done":
			s.Done++
		case "failed":
			s.Failed++
		}
	}
	out := make([]QueueStats, 0, len(by))
	for _, s := range by {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Queue < out[j].Queue })
	return out, nil
}

func (q *redisQueue) Get(ctx context.Context, id string) (JobInfo, error) {
	m, err := q.jobMeta(ctx, id)
	if err != nil {
		return JobInfo{}, err
	}
	var j Job
	if err := json.Unmarshal([]byte(m["job"]), &j); err != nil {
		return JobInfo{}, err
	}
	j.Attempts, _ = strconv.Atoi(m["attempts"])
	return JobInfo{Job: j, State: m["state"], LastError: m["last_error"]}, nil
}
func (q *redisQueue) List(ctx context.Context, f ListFilter) ([]JobInfo, error) {
	ids, err := q.client.SMembers(ctx, redisQueuePrefix+"ids").Result()
	if err != nil {
		return nil, err
	}
	out := make([]JobInfo, 0)
	for _, id := range ids {
		m, err := q.client.HMGet(ctx, redisJobKey(id), "job", "state", "last_error", "attempts").Result()
		if err != nil {
			return nil, err
		}
		if len(m) < 4 || m[0] == nil || m[1] == nil {
			_ = q.client.SRem(ctx, redisQueuePrefix+"ids", id).Err()
			continue
		}
		var j Job
		if err := json.Unmarshal([]byte(fmt.Sprint(m[0])), &j); err != nil {
			continue
		}
		j.Attempts, _ = strconv.Atoi(fmt.Sprint(m[3]))
		state := fmt.Sprint(m[1])
		if f.Queue != "" && f.Queue != j.Queue || f.State != "" && f.State != state {
			continue
		}
		last := ""
		if m[2] != nil {
			last = fmt.Sprint(m[2])
		}
		out = append(out, JobInfo{Job: j, State: state, LastError: last})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].RunAt.Equal(out[j].RunAt) {
			return out[i].RunAt.After(out[j].RunAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > f.limit() {
		out = out[:f.limit()]
	}
	return out, nil
}
func (q *redisQueue) RetryFailed(ctx context.Context, name string) (int64, error) {
	rows, err := q.List(ctx, ListFilter{Queue: name, State: "failed", Limit: 200})
	if err != nil {
		return 0, err
	}
	var n int64
	for _, r := range rows {
		if err := q.Retry(ctx, r.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
func (q *redisQueue) Remove(ctx context.Context, id string) error {
	m, err := q.jobMeta(ctx, id)
	if err != nil {
		return err
	}
	return q.removeRecord(ctx, id, m, false)
}

var redisRemoveScript = redis.NewScript(`local state=redis.call('HGET',KEYS[1],'state'); if not state then return -1 end; if ARGV[1]=='queued' and state~='queued' then return -2 end; if ARGV[1]=='nonrunning' and state=='running' then return -2 end; local entry=redis.call('HGET',KEYS[1],'entry'); if entry and entry~='' then redis.call('XACK',KEYS[2],ARGV[2],entry); redis.call('XDEL',KEYS[2],entry) end; redis.call('ZREM',KEYS[3],KEYS[1]); redis.call('SREM',KEYS[4],ARGV[3]); if ARGV[4]~='' then redis.call('DEL',KEYS[5]) end; redis.call('DEL',KEYS[1]); return 1`)

func (q *redisQueue) removeRecord(ctx context.Context, id string, m map[string]string, queuedOnly bool) error {
	mode := "nonrunning"
	if queuedOnly {
		mode = "queued"
	}
	v, err := redisRemoveScript.Run(ctx, q.client, []string{redisJobKey(id), q.stream(m["queue"]), q.scheduled(m["queue"]), redisQueuePrefix + "ids", redisDedupeKey(m["dedupe_key"])}, mode, redisQueueGroup, id, m["dedupe_key"]).Int()
	if err != nil {
		return fmt.Errorf("queue: remove redis job: %w", err)
	}
	if v == -1 {
		return ErrNotFound
	}
	if v == -2 {
		if queuedOnly {
			return fmt.Errorf("queue: job %s changed state during transfer", id)
		}
		return ErrJobRunning
	}
	return nil
}

func (q *redisQueue) Drain(ctx context.Context, limit int) ([]Job, error) {
	jobs, err := q.SnapshotQueued(ctx, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	if err := q.ConfirmQueued(ctx, ids); err != nil {
		return nil, err
	}
	return jobs, nil
}
func (q *redisQueue) SnapshotQueued(ctx context.Context, limit int) ([]Job, error) {
	ids, err := q.client.SMembers(ctx, redisQueuePrefix+"ids").Result()
	if err != nil {
		return nil, fmt.Errorf("queue: redis snapshot: %w", err)
	}
	out := make([]Job, 0)
	for _, id := range ids {
		m, err := q.client.HMGet(ctx, redisJobKey(id), "job", "state", "attempts").Result()
		if err != nil {
			return nil, err
		}
		if len(m) < 3 || m[0] == nil || fmt.Sprint(m[1]) != "queued" {
			if len(m) < 2 || m[0] == nil {
				_ = q.client.SRem(ctx, redisQueuePrefix+"ids", id).Err()
			}
			continue
		}
		var j Job
		if err := json.Unmarshal([]byte(fmt.Sprint(m[0])), &j); err != nil {
			return nil, err
		}
		j.Attempts, _ = strconv.Atoi(fmt.Sprint(m[2]))
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunAt.Before(out[j].RunAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (q *redisQueue) ConfirmQueued(ctx context.Context, ids []string) error {
	for _, id := range ids {
		m, err := q.jobMeta(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err := q.removeRecord(ctx, id, m, true); err != nil {
			return fmt.Errorf("queue: confirm redis transfer: %w", err)
		}
	}
	return nil
}
func (q *redisQueue) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	if err := q.client.Ping(ctx).Err(); err != nil {
		return 0, fmt.Errorf("queue: redis ping: %w", err)
	}
	return time.Since(start), nil
}
func (q *redisQueue) Close() error { return q.client.Close() }

var _ Queue = (*redisQueue)(nil)
var _ Inspector = (*redisQueue)(nil)
var _ TransferSource = (*redisQueue)(nil)
var _ LeaseExtender = (*redisQueue)(nil)

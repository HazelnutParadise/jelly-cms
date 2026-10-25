package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/settings"
	"github.com/labstack/echo/v4"
)

type indexNowRoundTripFunc func(*http.Request) (*http.Response, error)

func (f indexNowRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func indexNowResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    nil,
	}
}

func TestIndexNowRegressionSubmitsBoundedDeduplicatedPayload(t *testing.T) {
	key := "indexnow-test-key"
	urls := []string{"https://example.test/post/a", "https://example.test/post/a"}
	for i := 0; i < indexNowBatchMax+1; i++ {
		urls = append(urls, fmt.Sprintf("https://example.test/post/%d", i))
	}
	var calls int
	client := &http.Client{Transport: indexNowRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost || req.URL.Scheme != "https" || req.URL.Host != "api.indexnow.org" {
			t.Errorf("IndexNow request target = %s %s, want official HTTPS endpoint", req.Method, req.URL)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
			t.Errorf("IndexNow Content-Type = %q", got)
		}
		var payload indexNowRequest
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Errorf("decode IndexNow body: %v", err)
		}
		if payload.Host != "example.test" || payload.Key != key || payload.KeyLocation != "https://example.test/"+key+".txt" {
			t.Errorf("IndexNow host/key location = %+v", payload)
		}
		if len(payload.URLList) != indexNowBatchMax {
			t.Errorf("IndexNow URL count = %d, want capped at %d", len(payload.URLList), indexNowBatchMax)
		}
		seen := map[string]bool{}
		for _, value := range payload.URLList {
			if seen[value] {
				t.Errorf("IndexNow payload contains duplicate URL %q", value)
			}
			seen[value] = true
		}
		return indexNowResponse(http.StatusAccepted, ""), nil
	})}
	if err := submitIndexNow(context.Background(), indexNowEndpoint, "https://example.test", key, urls, client); err != nil {
		t.Fatalf("submitIndexNow() = %v", err)
	}
	if calls != 1 {
		t.Fatalf("IndexNow HTTP call count = %d, want 1", calls)
	}
}

func TestIndexNowRegression429AndOversizedResponseAreErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, body: "retry later"},
		{name: "response over limit", status: http.StatusAccepted, body: strings.Repeat("x", indexNowMaxBody+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: indexNowRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return indexNowResponse(tc.status, tc.body), nil
			})}
			err := submitIndexNow(context.Background(), indexNowEndpoint, "https://example.test", "indexnow-test-key", []string{"https://example.test/post/a"}, client)
			if err == nil {
				t.Fatal("submitIndexNow() succeeded for a retryable or oversized response")
			}
		})
	}
}

func TestIndexNowRegressionRejectsNonOfficialEndpointWithoutNetwork(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: indexNowRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("transport must not be called")
	})}
	err := submitIndexNow(context.Background(), "https://attacker.example/submit", "https://example.test", "indexnow-test-key", []string{"https://example.test/post/a"}, client)
	if err == nil {
		t.Fatal("submitIndexNow() accepted a non-official endpoint")
	}
	if calls != 0 {
		t.Fatalf("non-official endpoint reached transport %d times", calls)
	}
}

func indexNowSetEnabled(t *testing.T, h *accessHarness, actor uint) {
	t.Helper()
	if err := settings.Set(context.Background(), actor, map[string]string{
		"seo_indexnow_enabled": "true",
		"seo_indexnow_key":     "indexnow-test-key-12345678",
		"seo_index_site":       "true",
	}); err != nil {
		t.Fatalf("enable IndexNow in isolated database: %v", err)
	}
}

func indexNowPublishPost(t *testing.T, h *accessHarness, token, title, slug string, categoryID uint) (accessResult, uint) {
	t.Helper()
	request := map[string]any{"title": title, "slug": slug, "type": "post", "status": "published", "source": "published body", "format": "md"}
	if categoryID != 0 {
		request["category_ids"] = []uint{categoryID}
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := h.request(http.MethodPost, "/api/admin/posts", string(body), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(response, "publish IndexNow post")
	var result struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(response.body), &result); err != nil {
		t.Fatalf("decode published post response %q: %v", response.body, err)
	}
	return response, result.ID
}

func indexNowPublishProduct(t *testing.T, h *accessHarness, token, title, slug string, categoryID uint) (accessResult, uint) {
	t.Helper()
	request := map[string]any{"title": title, "slug": slug, "status": "active", "price": "10"}
	if categoryID != 0 {
		request["categories"] = []uint{categoryID}
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response := h.request(http.MethodPost, "/api/admin/products", string(body), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(response, "publish IndexNow product")
	var result struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal([]byte(response.body), &result); err != nil {
		t.Fatalf("decode published product response %q: %v", response.body, err)
	}
	return response, result.ID
}

func indexNowPendingOption(t *testing.T, h *accessHarness, kind string, id uint) (map[string]any, bool) {
	t.Helper()
	key := "system_indexnow_pending:" + kind + ":" + strconv.FormatUint(uint64(id), 10)
	var value string
	if err := h.db.Raw("SELECT value FROM options WHERE key = ?", key).Scan(&value).Error; err != nil {
		t.Fatalf("read pending IndexNow option %q: %v", key, err)
	}
	if value == "" {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		t.Fatalf("decode pending IndexNow option %q: %v", key, err)
	}
	return payload, true
}

func indexNowCheckPendingOption(t *testing.T, h *accessHarness, kind string, id uint) map[string]any {
	t.Helper()
	payload, ok := indexNowPendingOption(t, h, kind, id)
	if !ok {
		t.Errorf("published %s %d has no durable IndexNow intent option", kind, id)
		return nil
	}
	if payload["version"] != float64(1) || payload["kind"] != kind || payload["id"] != float64(id) || payload["generation"] == "" || payload["site_url"] == "" || payload["not_before"] == nil {
		t.Errorf("pending IndexNow intent for %s %d is incomplete: %+v", kind, id, payload)
	}
	return payload
}

func TestIndexNowRegressionPublishedPostAndProductKeepDurableIntentWithoutRuntime(t *testing.T) {
	t.Setenv("APP_URL", "https://indexnow.example.test")
	h := newAccessHarness(t)
	admin := h.user("admin", "indexnow-pending-no-runtime-admin")
	indexNowSetEnabled(t, h, admin.ID)
	if systemRuntimeState.active != nil {
		t.Fatal("test requires RegisterRoutes without starting background services")
	}

	postResponse, postID := indexNowPublishPost(t, h, h.token(admin), "IndexNow Pending Post", "indexnow-pending-post", 0)
	if postResponse.code != http.StatusOK || postID == 0 {
		t.Fatalf("published post = %d id=%d: %s", postResponse.code, postID, postResponse.body)
	}
	indexNowCheckPendingOption(t, h, "post", postID)

	productResponse, productID := indexNowPublishProduct(t, h, h.token(admin), "IndexNow Pending Product", "indexnow-pending-product", 0)
	if productResponse.code < 200 || productResponse.code >= 300 || productID == 0 {
		t.Fatalf("published product = %d id=%d: %s", productResponse.code, productID, productResponse.body)
	}
	indexNowCheckPendingOption(t, h, "product", productID)
	var jobs int64
	if err := h.db.Model(&struct{}{}).Table("jobs").Where("kind = ?", indexNowJobKind).Count(&jobs).Error; err != nil {
		t.Fatalf("count IndexNow jobs without runtime: %v", err)
	}
	if jobs != 0 {
		t.Errorf("IndexNow job count without a running queue = %d, want 0 until durable intents dispatch", jobs)
	}
}

func indexNowInstallWriteFailureTrigger(t *testing.T, h *accessHarness, suffix string) {
	t.Helper()
	functionName := "jelly_test_fail_indexnow_pending_" + suffix
	triggerName := "jelly_test_fail_indexnow_pending_" + suffix
	function := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.key LIKE 'system_indexnow_pending:%%' THEN
				RAISE EXCEPTION 'injected IndexNow intent persistence failure';
			END IF;
			RETURN NEW;
		END; $$`, functionName)
	if err := h.db.Exec(function).Error; err != nil {
		t.Fatalf("create IndexNow intent failure function: %v", err)
	}
	if err := h.db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT OR UPDATE ON options FOR EACH ROW EXECUTE FUNCTION %s()`, triggerName, functionName)).Error; err != nil {
		t.Fatalf("create IndexNow intent failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_ = h.db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON options`, triggerName)).Error
		_ = h.db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, functionName)).Error
	})
}

func TestIndexNowRegressionIntentWriteFailureRollsBackPublishedContentAndRelations(t *testing.T) {
	t.Setenv("APP_URL", "https://indexnow.example.test")
	h := newAccessHarness(t)
	admin := h.user("admin", "indexnow-rollback-admin")
	indexNowSetEnabled(t, h, admin.ID)
	var postCategory core.Category
	if err := h.db.Create(&core.Category{Name: "IndexNow rollback post category", Slug: "indexnow-rollback-post-category"}).Error; err != nil {
		t.Fatalf("create post category: %v", err)
	}
	if err := h.db.Where("slug = ?", "indexnow-rollback-post-category").Take(&postCategory).Error; err != nil {
		t.Fatalf("read post category: %v", err)
	}
	var productCategory uint
	if err := h.db.Raw(`INSERT INTO product_categories (name, slug) VALUES ('IndexNow rollback product category', 'indexnow-rollback-product-category') RETURNING id`).Scan(&productCategory).Error; err != nil || productCategory == 0 {
		t.Fatalf("create product category: id=%d err=%v", productCategory, err)
	}
	indexNowInstallWriteFailureTrigger(t, h, "rollback")
	token := h.token(admin)

	postResponse, postID := indexNowPublishPost(t, h, token, "IndexNow Rollback Post", "indexnow-rollback-post", postCategory.ID)
	if postResponse.code < 400 {
		t.Errorf("publish post with failed durable intent = %d, want failure", postResponse.code)
	}
	var postRows, postRelations int64
	if err := h.db.Model(&core.Post{}).Where("slug = ?", "indexnow-rollback-post").Count(&postRows).Error; err != nil {
		t.Fatalf("count post after failed intent: %v", err)
	}
	if err := h.db.Table("post_categories").Where("post_id = ? AND category_id = ?", postID, postCategory.ID).Count(&postRelations).Error; err != nil {
		t.Fatalf("count post-category relation after failed intent: %v", err)
	}
	if postRows != 0 || postRelations != 0 {
		t.Errorf("failed post intent left content/relations = %d/%d, want 0/0", postRows, postRelations)
	}

	productResponse, productID := indexNowPublishProduct(t, h, token, "IndexNow Rollback Product", "indexnow-rollback-product", productCategory)
	if productResponse.code < 400 {
		t.Errorf("publish product with failed durable intent = %d, want failure", productResponse.code)
	}
	var productRows, productRelations int64
	if err := h.db.Model(&core.Product{}).Where("slug = ?", "indexnow-rollback-product").Count(&productRows).Error; err != nil {
		t.Fatalf("count product after failed intent: %v", err)
	}
	if err := h.db.Table("product_category_links").Where("product_id = ? AND category_id = ?", productID, productCategory).Count(&productRelations).Error; err != nil {
		t.Fatalf("count product-category relation after failed intent: %v", err)
	}
	if productRows != 0 || productRelations != 0 {
		t.Errorf("failed product intent left content/relations = %d/%d, want 0/0", productRows, productRelations)
	}
}

func TestIndexNowRegressionChangedContentGenerationsCreateSeparatePausedJobs(t *testing.T) {
	t.Setenv("APP_URL", "https://indexnow.example.test")
	h := newAccessHarness(t)
	admin := h.user("admin", "indexnow-generation-admin")
	indexNowSetEnabled(t, h, admin.ID)
	if err := settings.Set(context.Background(), admin.ID, map[string]string{"queue_paused": "true"}); err != nil {
		t.Fatalf("pause system queue workers: %v", err)
	}
	SetSystemDatabase(h.db)
	stop := StartSystemServices(context.Background())
	t.Cleanup(func() {
		stop()
		SetSystemDatabase(nil)
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		systemRuntimeState.RLock()
		runtime := systemRuntimeState.active
		ready := runtime != nil && runtime.db == h.db && runtime.ready.Load()
		systemRuntimeState.RUnlock()
		if ready {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	systemRuntimeState.RLock()
	runtime := systemRuntimeState.active
	ready := runtime != nil && runtime.db == h.db && runtime.ready.Load()
	systemRuntimeState.RUnlock()
	if !ready {
		t.Fatal("system runtime queue did not become ready")
	}
	token := h.token(admin)
	postResponse, postID := indexNowPublishPost(t, h, token, "IndexNow Generation Post", "indexnow-generation-post", 0)
	if postResponse.code != http.StatusOK || postID == 0 {
		t.Fatalf("create published post = %d id=%d: %s", postResponse.code, postID, postResponse.body)
	}
	updateBody, _ := json.Marshal(map[string]any{"source": "updated published body", "format": "md"})
	updated := h.request(http.MethodPut, "/api/admin/posts/"+strconv.FormatUint(uint64(postID), 10), string(updateBody), token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	h.mustNoPanic(updated, "update published IndexNow post")
	if updated.code != http.StatusOK {
		t.Fatalf("update published post = %d: %s", updated.code, updated.body)
	}
	var jobs int64
	if err := h.db.Model(&struct{}{}).Table("jobs").Where("kind = ?", indexNowJobKind).Count(&jobs).Error; err != nil {
		t.Fatalf("count paused IndexNow jobs: %v", err)
	}
	if jobs != 2 {
		t.Errorf("queued IndexNow generations for same URL within five minutes = %d, want 2", jobs)
	}
	var dedupeKeys []string
	if err := h.db.Table("jobs").Where("kind = ?", indexNowJobKind).Order("created_at, id").Pluck("dedupe_key", &dedupeKeys).Error; err != nil {
		t.Fatalf("read queued IndexNow generations: %v", err)
	}
	if len(dedupeKeys) == 2 && dedupeKeys[0] == dedupeKeys[1] {
		t.Errorf("content saves reused IndexNow generation dedupe key %q", dedupeKeys[0])
	}
}

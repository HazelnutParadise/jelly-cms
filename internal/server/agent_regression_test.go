package server

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These tests cover interaction boundaries that are not exercised by the ticket's
// original acceptance cases. They intentionally assert only HTTP-visible behavior
// except where the invariant is that a rejected write leaves persisted rows alone.

func TestAgentRegressionConcurrentGraceRotationsKeepAtMostTwoUsableKeys(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, created := agtSetup(t, h, "Concurrent rotation", "posts.read")
	const racers = 8
	results := make([]agtResult, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = agtAdmin(h, adminToken, http.MethodPost,
				fmt.Sprintf("/api/admin/agents/%d/rotate", created.Agent.ID),
				`{"mode":"grace","grace_minutes":30}`)
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for i, r := range results {
		if r.code == http.StatusOK {
			wins++
		} else if r.code != http.StatusConflict {
			t.Errorf("valid concurrent rotation %d returned %d, want success or a concurrency conflict: %s", i, r.code, r.body)
		}
	}
	if wins == 0 {
		t.Fatalf("no concurrent rotation succeeded: %+v", results)
	}
	keys := agtDetail(t, h, adminToken, created.Agent.ID).Keys
	usable, active, grace := 0, 0, 0
	statusByKeyID := make(map[uint]string, len(keys))
	for _, key := range keys {
		statusByKeyID[key.ID] = key.Status
		switch key.Status {
		case "active":
			active++
			usable++
		case "grace":
			grace++
			usable++
		}
	}
	if usable > 2 {
		t.Errorf("agent has %d usable keys after concurrent rotations, want at most active+grace (keys=%+v)", usable, keys)
	}
	if wins >= 2 && (active != 1 || grace != 1 || usable != 2) {
		t.Errorf("after %d successful rotations, key states have active=%d grace=%d usable=%d; want exactly one active and one grace (keys=%+v)", wins, active, grace, usable, keys)
	}
	// Later successful rotations may revoke earlier grace keys. Their responses
	// were valid when issued, but their credentials must now match persisted state.
	for i, r := range results {
		if r.code != http.StatusOK {
			continue
		}
		var rotated agtRotated
		agtDecode(t, r, &rotated, fmt.Sprintf("rotation %d", i))
		sum := sha256.Sum256([]byte(rotated.APIKey))
		var persisted struct {
			ID      uint
			KeyHash string
			Prefix  string
		}
		if err := h.db.Raw("SELECT id, key_hash, prefix FROM agent_keys WHERE id = ? AND agent_id = ?", rotated.Key.ID, created.Agent.ID).Scan(&persisted).Error; err != nil {
			t.Fatalf("read persisted key for rotation %d: %v", i, err)
		}
		if persisted.ID != rotated.Key.ID || persisted.KeyHash != hex.EncodeToString(sum[:]) || persisted.Prefix != rotated.APIKey[:12] || rotated.Key.Prefix != persisted.Prefix {
			t.Errorf("rotation %d response key identity does not match its api_key: response id=%d prefix=%q; persisted=%+v; api_key prefix=%q", i, rotated.Key.ID, rotated.Key.Prefix, persisted, rotated.APIKey[:12])
		}
		status, exists := statusByKeyID[rotated.Key.ID]
		if !exists {
			t.Errorf("successful rotation %d returned key id %d absent from persisted detail", i, rotated.Key.ID)
			continue
		}
		me := agtDo(h, http.MethodGet, "/api/agent/v1/me", "", rotated.APIKey, "")
		switch status {
		case "active", "grace":
			if me.code != http.StatusOK {
				t.Errorf("successful rotation %d key %d is persisted %q but /me returned %d: %s", i, rotated.Key.ID, status, me.code, me.body)
			}
		case "revoked":
			agtExpectError(t, me, http.StatusUnauthorized, "key_revoked", fmt.Sprintf("superseded rotation %d key %d", i, rotated.Key.ID))
		default:
			t.Errorf("successful rotation %d key %d has unexpected persisted status %q", i, rotated.Key.ID, status)
		}
	}
}

func TestAgentRegressionRotationResponseKeepsItsOwnKeyIdentity(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, created := agtSetup(t, h, "Deterministic rotation identity", "posts.read")
	entered := make(chan struct{})
	release := make(chan struct{})
	var claimed atomic.Bool
	const callbackName = "agent_regression_pause_first_rotation_view_key_read"
	if err := h.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "agent_keys" {
			return
		}
		if _, inTransaction := tx.Statement.ConnPool.(*sql.Tx); inTransaction {
			return
		}
		if claimed.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}); err != nil {
		t.Fatalf("register deterministic rotation callback: %v", err)
	}
	t.Cleanup(func() { _ = h.db.Callback().Query().Remove(callbackName) })

	firstDone := make(chan agtResult, 1)
	go func() {
		firstDone <- agtAdmin(h, adminToken, http.MethodPost,
			fmt.Sprintf("/api/admin/agents/%d/rotate", created.Agent.ID), `{"mode":"grace","grace_minutes":30}`)
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("first rotation did not reach its post-commit key-view query")
	}
	second := agtAdmin(h, adminToken, http.MethodPost,
		fmt.Sprintf("/api/admin/agents/%d/rotate", created.Agent.ID), `{"mode":"grace","grace_minutes":30}`)
	close(release)
	first := <-firstDone
	for i, result := range []agtResult{first, second} {
		if result.code != http.StatusOK {
			t.Fatalf("rotation %d returned %d: %s", i, result.code, result.body)
		}
		var rotated agtRotated
		agtDecode(t, result, &rotated, fmt.Sprintf("deterministic rotation %d", i))
		sum := sha256.Sum256([]byte(rotated.APIKey))
		var persisted core.AgentKey
		if err := h.db.First(&persisted, rotated.Key.ID).Error; err != nil {
			t.Fatalf("read response key %d: %v", rotated.Key.ID, err)
		}
		if persisted.KeyHash != hex.EncodeToString(sum[:]) || persisted.Prefix != rotated.APIKey[:12] || rotated.Key.Prefix != rotated.APIKey[:12] {
			t.Errorf("rotation %d response mixed api_key with another key identity: response id=%d prefix=%q api_key_prefix=%q persisted id=%d prefix=%q", i, rotated.Key.ID, rotated.Key.Prefix, rotated.APIKey[:12], persisted.ID, persisted.Prefix)
		}
	}
}

func TestAgentRegressionRotationEmptyChunkedBodyDefaultsToImmediate(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, created := agtSetup(t, h, "Empty chunked rotation body", "posts.read")
	for _, tc := range []struct {
		label, body string
	}{
		{"empty", ""},
		{"whitespace", "  \n\t"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost,
				fmt.Sprintf("/api/admin/agents/%d/rotate", created.Agent.ID),
				io.NopCloser(strings.NewReader(tc.body)))
			request.ContentLength = -1
			request.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
			request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			request.Header.Set(echo.HeaderAuthorization, "Bearer "+adminToken)
			recorder := httptest.NewRecorder()
			h.echo.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Errorf("rotate with %s chunked body returned %d, want 200 immediate rotation; body=%q", tc.label, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestAgentRegressionOwnerAvailabilityAndPersonalTokenCannotManage(t *testing.T) {
	h := newAccessHarness(t)
	owner, _, created := agtSetup(t, h, "Owner state", "posts.read")
	for _, status := range []string{"invited", "disabled"} {
		if err := h.db.Model(&core.User{}).Where("id = ?", owner.ID).Update("status", status).Error; err != nil {
			t.Fatalf("set owner status %q: %v", status, err)
		}
		r := agtMe(h, created.APIKey)
		env := agtExpectError(t, r, http.StatusUnauthorized, "owner_unavailable", "agent request while owner is "+status)
		if env.Code != "owner_unavailable" {
			t.Errorf("owner status %q error=%q, want owner_unavailable", status, env.Code)
		}
		if err := h.db.Model(&core.User{}).Where("id = ?", owner.ID).Update("status", "active").Error; err != nil {
			t.Fatalf("restore active owner: %v", err)
		}
	}
	// Create a real jly_ personal token via the account API. Unknown bearer strings
	// would only prove authentication failure, not the agent-management boundary.
	personalOwner := tokTestUser(t, h, "agent-regression-personal-admin", "admin")
	personalJWT := tokTestLogin(t, h, personalOwner.Username)
	createdToken := tokTestCall(t, h, http.MethodPost, "/api/account/tokens", `{"name":"agent regression","scopes":["admin"]}`, personalJWT)
	if createdToken.code != http.StatusCreated {
		t.Fatalf("create personal token returned %d: %s", createdToken.code, createdToken.body)
	}
	var tokenBody struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(createdToken.body), &tokenBody); err != nil || !strings.HasPrefix(tokenBody.Token, "jly_") {
		t.Fatalf("invalid personal token response: %s (err=%v)", createdToken.body, err)
	}
	personalToken := tokenBody.Token
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/admin/agents", agtCreateBody("personal token attempt", "posts.read")},
		{http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/rotate", created.Agent.ID), `{"mode":"immediate"}`},
		{http.MethodDelete, fmt.Sprintf("/api/admin/agents/%d", created.Agent.ID), ""},
	} {
		r := agtAdmin(h, personalToken, tc.method, tc.path, tc.body)
		if r.code != http.StatusUnauthorized && r.code != http.StatusForbidden {
			t.Errorf("personal jly token %s %s returned %d, want denied; body=%q", tc.method, tc.path, r.code, r.body)
		}
	}
}

func TestAgentRegressionCrossTypeWritesAndDeletesAreNonMutating(t *testing.T) {
	h := newAccessHarness(t)
	admin, _, agent := agtSetup(t, h, "Cross type", "posts.write", "pages.write")
	article := core.Post{Title: "post snapshot", Slug: "agent-regression-post", Content: "unchanged", Type: "post", Status: "draft", AuthorID: admin.ID}
	page := core.Post{Title: "page snapshot", Slug: "agent-regression-page", Content: "unchanged", Type: "page", Status: "draft", AuthorID: admin.ID}
	for _, p := range []*core.Post{&article, &page} {
		if err := h.db.Create(p).Error; err != nil {
			t.Fatalf("seed %s: %v", p.Slug, err)
		}
	}
	for _, tc := range []struct {
		method, route string
		id            uint
		body          string
	}{
		{http.MethodPut, "/posts", page.ID, `{"title":"must not become post","content":"mutated","base_updated_at":"2026-01-01T00:00:00Z"}`},
		{http.MethodDelete, "/posts", page.ID, ""},
		{http.MethodPut, "/pages", article.ID, `{"title":"must not become page","content":"mutated","base_updated_at":"2026-01-01T00:00:00Z"}`},
		{http.MethodDelete, "/pages", article.ID, ""},
	} {
		r := agtDo(h, tc.method, fmt.Sprintf("/api/agent/v1%s/%d", tc.route, tc.id), tc.body, agent.APIKey, "")
		if r.code != http.StatusNotFound {
			t.Errorf("%s %s/%d returned %d, want 404; body=%q", tc.method, tc.route, tc.id, r.code, r.body)
		}
	}
	var gotPost, gotPage core.Post
	if err := h.db.First(&gotPost, article.ID).Error; err != nil {
		t.Fatalf("read post after rejected cross-type operations: %v", err)
	}
	if err := h.db.First(&gotPage, page.ID).Error; err != nil {
		t.Fatalf("read page after rejected cross-type operations: %v", err)
	}
	if gotPost.Title != article.Title || gotPost.Content != article.Content || gotPage.Title != page.Title || gotPage.Content != page.Content {
		t.Errorf("cross-type operation mutated content: post=%+v page=%+v", gotPost, gotPage)
	}
}

func TestAgentRegressionProductCASHasSingleWinnerAndNoPartialCatalogWrites(t *testing.T) {
	h := newAccessHarness(t)
	_, _, agent := agtSetup(t, h, "Product CAS", "products.read", "products.write")
	product := core.Product{Title: "CAS baseline", Slug: "agent-regression-cas-product", Price: 12000, Stock: 5, Status: "active"}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	variant := core.ProductVariant{ProductID: product.ID, Name: "Baseline", SKU: "REG-CAS-BASE", Price: 12000, Stock: 5, Status: "active"}
	if err := h.db.Create(&variant).Error; err != nil {
		t.Fatalf("seed product variant: %v", err)
	}
	categories := make([]core.ProductCategory, 6)
	for i := range categories {
		categories[i] = core.ProductCategory{Name: fmt.Sprintf("CAS category %d", i), Slug: fmt.Sprintf("agent-regression-cas-%d", i)}
		if err := h.db.Create(&categories[i]).Error; err != nil {
			t.Fatalf("seed product category %d: %v", i, err)
		}
	}
	path := fmt.Sprintf("/api/agent/v1/products/%d", product.ID)
	base := agtUpdatedAt(t, agtDo(h, http.MethodGet, path, "", agent.APIKey, ""), "GET product before concurrent update")
	const racers = 6
	results := make([]agtResult, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			body := fmt.Sprintf(`{"title":"CAS winner %d","status":"active","base_updated_at":%q,"variants":[{"id":%d,"name":"variant winner %d","sku":"REG-CAS-%d","price":"%d","stock":%d,"status":"active"}],"categories":[%d]}`,
				i, base, variant.ID, i, i, 120+i, i+2, categories[i].ID)
			results[i] = agtDo(h, http.MethodPut, path, body, agent.APIKey, "")
		}(i)
	}
	close(start)
	wg.Wait()
	winner := -1
	conflicts := 0
	for i, r := range results {
		switch r.code {
		case http.StatusOK:
			if winner != -1 {
				t.Errorf("product CAS had multiple winners: %d and %d", winner, i)
			}
			winner = i
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("product CAS racer %d returned %d, want 200 or 409; body=%q", i, r.code, r.body)
		}
	}
	winners := 0
	if winner >= 0 {
		winners = 1
	}
	if winner < 0 || conflicts != racers-1 {
		t.Fatalf("product CAS winners=%d conflicts=%d, want exactly one winner and %d conflicts", winners, conflicts, racers-1)
	}
	var stored core.Product
	if err := h.db.First(&stored, product.ID).Error; err != nil {
		t.Fatalf("read product after races: %v", err)
	}
	var storedVariants []core.ProductVariant
	if err := h.db.Where("product_id = ?", product.ID).Find(&storedVariants).Error; err != nil {
		t.Fatalf("read variants after races: %v", err)
	}
	var categoryIDs []uint
	if err := h.db.Raw("SELECT category_id FROM product_category_links WHERE product_id = ?", product.ID).Scan(&categoryIDs).Error; err != nil {
		t.Fatalf("read category links after races: %v", err)
	}
	if len(storedVariants) != 1 || storedVariants[0].ID != variant.ID || storedVariants[0].Name != fmt.Sprintf("variant winner %d", winner) || storedVariants[0].SKU != fmt.Sprintf("REG-CAS-%d", winner) || storedVariants[0].Stock != winner+2 {
		t.Errorf("variant state is not the winning request as one unit: winner=%d stored=%+v", winner, storedVariants)
	}
	if stored.Title != fmt.Sprintf("CAS winner %d", winner) {
		t.Errorf("product title=%q, want winning request %d", stored.Title, winner)
	}
	if stored.Stock != winner+2 {
		t.Errorf("product stock=%d, want stock from winning request %d", stored.Stock, winner+2)
	}
	if len(categoryIDs) != 1 || categoryIDs[0] != categories[winner].ID {
		t.Errorf("category links=%v, want only winner category %d", categoryIDs, categories[winner].ID)
	}
	// The response timestamp must round-trip as the next optimistic base.
	winningResponse := results[winner]
	baseAfter := agtUpdatedAt(t, winningResponse, "winning product update response")
	next := agtDo(h, http.MethodPut, path, fmt.Sprintf(`{"title":"CAS timestamp reusable","base_updated_at":%q}`, baseAfter), agent.APIKey, "")
	if next.code != http.StatusOK {
		t.Errorf("returned product updated_at was not reusable as base_updated_at: status=%d body=%q", next.code, next.body)
	}
}

func TestAgentRegressionKeyIDLogFilterPreservesTotalCursorAndIsolation(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, a := agtSetup(t, h, "Key filter A", "posts.read", "logs.read")
	b := agtCreate(t, h, adminToken, "Key filter B", "posts.read", "logs.read")
	// Produce events with two key IDs for A, plus events belonging to a separate agent.
	rotated := agtRotate(t, h, adminToken, a.Agent.ID, `{"mode":"grace","grace_minutes":30}`)
	for i := 0; i < 3; i++ {
		agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", a.APIKey, "")
	}
	for i := 0; i < 2; i++ {
		agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", rotated.APIKey, "")
	}
	for i := 0; i < 2; i++ {
		agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", b.APIKey, "")
	}
	keyID := a.Key.ID
	fetch := func(cursor string) agtEventPage {
		t.Helper()
		q := url.Values{"key_id": {fmt.Sprint(keyID)}, "module": {"posts"}, "limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		r := agtDo(h, http.MethodGet, "/api/agent/v1/logs?"+q.Encode(), "", a.APIKey, "")
		if r.code != http.StatusOK {
			t.Fatalf("key_id log filter returned %d: %s", r.code, r.body)
		}
		var p agtEventPage
		agtDecode(t, r, &p, "key_id filtered logs")
		return p
	}
	first := fetch("")
	if first.Total != 3 || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first filtered page=%+v, want total=3, 2 items and cursor", first)
	}
	second := fetch(*first.NextCursor)
	if second.Total != 3 || len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("second filtered page=%+v, want total=3, 1 item and no cursor", second)
	}
	for _, event := range append(first.Items, second.Items...) {
		if event.AgentID != a.Agent.ID || event.KeyID == nil || *event.KeyID != keyID {
			t.Errorf("key_id filtered event escaped agent/key scope: %+v", event)
		}
	}
}

func TestAgentRegressionUnknownRouteAndWrongMethodKeepErrorEnvelopeAndAudit(t *testing.T) {
	h := newAccessHarness(t)
	_, _, agent := agtSetup(t, h, "Matched failure audit", "posts.read")
	var unknownRoute agtResult
	for _, tc := range []struct {
		method, path string
		code         int
		expected     string
	}{
		{http.MethodGet, "/api/agent/v1/posts/987654321", http.StatusNotFound, "not_found"},
		{http.MethodGet, "/api/agent/v1/not-a-route/extra", http.StatusNotFound, "not_found"},
		{http.MethodPost, "/api/agent/v1/me", http.StatusMethodNotAllowed, "method_not_allowed"},
		{http.MethodPost, "/api/agent/v1", http.StatusMethodNotAllowed, "method_not_allowed"},
	} {
		r := agtDo(h, tc.method, tc.path, "", agent.APIKey, "")
		agtExpectError(t, r, tc.code, tc.expected, tc.method+" "+tc.path)
		if tc.path == "/api/agent/v1/not-a-route/extra" {
			unknownRoute = r
		}
	}
	var unknownEvent core.AgentEvent
	if err := h.db.Where("request_id = ? AND actor_type = 'agent'", unknownRoute.header.Get(echo.HeaderXRequestID)).First(&unknownEvent).Error; err != nil {
		t.Fatalf("read unknown-route audit: %v", err)
	}
	var unknownSummary map[string]any
	if err := json.Unmarshal(unknownEvent.Summary, &unknownSummary); err != nil {
		t.Fatalf("decode unknown-route summary: %v", err)
	}
	params, _ := unknownSummary["params"].(map[string]any)
	if len(params) != 0 {
		t.Errorf("unknown route recorded wildcard params=%v, want empty object", params)
	}
	var events []agtEvent
	if err := h.db.Raw(`SELECT id, agent_id, key_id, key_prefix, method, route, status, request_id AS correlation_id FROM agent_events WHERE agent_id = ? ORDER BY id`, agent.Agent.ID).Scan(&events).Error; err != nil {
		t.Fatalf("read matched-key audit events: %v", err)
	}
	if len(events) < 4 {
		t.Fatalf("got %d audit events for matched-key failures, want all four", len(events))
	}
	statuses := map[int]bool{}
	for _, event := range events {
		statuses[event.Status] = true
		if event.AgentID != agent.Agent.ID || event.KeyID == nil || *event.KeyID != agent.Key.ID || event.KeyPrefix != agent.Key.Prefix {
			t.Errorf("matched-key failure audit envelope lost agent/key identity: %+v", event)
		}
	}
	if !statuses[http.StatusNotFound] || !statuses[http.StatusMethodNotAllowed] {
		t.Errorf("audit statuses=%v, want 404 and 405 including POST on discovery root", statuses)
	}
}

func TestAgentRegressionSettingsURLValidationMatchesAdminAndDoesNotWrite(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, agent := agtSetup(t, h, "Settings URL validation", "settings.write")
	before := core.Option{Key: "permalink_post", Value: "/post/{slug}"}
	if err := h.db.Save(&before).Error; err != nil {
		t.Fatalf("seed permalink option: %v", err)
	}
	bad := `{"permalink_post":"/archive/{unknown}"}`
	for _, tc := range []struct{ label, method, path, bearer string }{
		{"agent", http.MethodPut, "/api/agent/v1/settings", agent.APIKey},
		{"admin", http.MethodPost, "/api/admin/settings", adminToken},
	} {
		var r agtResult
		if tc.label == "agent" {
			r = agtDo(h, tc.method, tc.path, bad, tc.bearer, "")
		} else {
			r = agtAdmin(h, tc.bearer, tc.method, tc.path, bad)
		}
		if r.code != http.StatusBadRequest {
			t.Errorf("%s invalid permalink write returned %d, want 400; body=%q", tc.label, r.code, r.body)
		}
		var after core.Option
		if err := h.db.Where("key = ?", before.Key).First(&after).Error; err != nil {
			t.Fatalf("read permalink option after %s rejection: %v", tc.label, err)
		}
		if after.Value != before.Value {
			t.Errorf("%s rejected URL write changed permalink from %q to %q", tc.label, before.Value, after.Value)
		}
	}
	// A prefix collision is checked against the stored settings as well as each
	// incoming key, so a valid-looking individual prefix cannot break route identity.
	if err := h.db.Save(&core.Option{Key: "permalink_category_prefix", Value: "/topics-old"}).Error; err != nil {
		t.Fatalf("seed category prefix: %v", err)
	}
	if err := h.db.Save(&core.Option{Key: "permalink_tag_prefix", Value: "/tags-old"}).Error; err != nil {
		t.Fatalf("seed tag prefix: %v", err)
	}
	collision := `{"permalink_category_prefix":"/topics","permalink_tag_prefix":"/topics"}`
	r := agtDo(h, http.MethodPut, "/api/agent/v1/settings", collision, agent.APIKey, "")
	if r.code != http.StatusBadRequest {
		t.Errorf("agent prefix collision returned %d, want 400; body=%q", r.code, r.body)
	}
	var categoryPrefix, tagPrefix core.Option
	if err := h.db.Where("key = ?", "permalink_category_prefix").First(&categoryPrefix).Error; err != nil {
		t.Fatalf("read category prefix after rejected collision: %v", err)
	}
	if err := h.db.Where("key = ?", "permalink_tag_prefix").First(&tagPrefix).Error; err != nil {
		t.Fatalf("read tag prefix after rejected collision: %v", err)
	}
	if categoryPrefix.Value != "/topics-old" || tagPrefix.Value != "/tags-old" {
		t.Errorf("rejected prefix collision changed stored values: category=%q tag=%q", categoryPrefix.Value, tagPrefix.Value)
	}
}

func TestAgentRegressionAdminEventsFilterMultipleActionsAcrossHistory(t *testing.T) {
	h := newAccessHarness(t)
	owner, adminToken, a := agtSetup(t, h, "Action history A", "posts.read")
	b := agtCreate(t, h, adminToken, "Action history B", "posts.read")
	ownerID := owner.ID
	seedEvent := func(agentID uint, name, action string, at time.Time) {
		t.Helper()
		event := core.AgentEvent{
			At: at, ActorType: "user", ActorUserID: &ownerID,
			AgentID: agentID, AgentName: name, Action: action, Module: "agents",
			Method: http.MethodPost, Route: "/api/admin/agents/:id", Status: http.StatusOK,
			Summary: json.RawMessage(`{"fixture":"action-filter-regression"}`),
		}
		if err := h.db.Create(&event).Error; err != nil {
			t.Fatalf("seed %s event for agent %d: %v", action, agentID, err)
		}
	}
	baseTime := time.Now().UTC().Add(-time.Hour)
	olderActions := []string{"key.rotate", "key.revoke", "key.rotate", "key.revoke", "key.rotate"}
	for i, action := range olderActions {
		seedEvent(a.Agent.ID, a.Agent.Name, action, baseTime.Add(time.Duration(i)*time.Second))
	}
	// More than the default 50 newest rows are irrelevant agent.update events.
	// Correct filtering must find the older rotation history before applying limit.
	for i := 0; i < 55; i++ {
		seedEvent(a.Agent.ID, a.Agent.Name, "agent.update", baseTime.Add(time.Hour+time.Duration(i)*time.Second))
	}
	seedEvent(b.Agent.ID, b.Agent.Name, "key.rotate", baseTime.Add(2*time.Hour))

	query := url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "limit": {"2"}}
	query.Add("action", "key.rotate")
	query.Add("action", "key.revoke")
	query.Add("action", "") // Empty values are ignored while repeated actions remain meaningful.
	first, _ := agtEvents(t, h, adminToken, query)
	if first.Total != 5 || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first multi-action history page=%+v; want total=5, two matching rows, and cursor", first)
	}
	seen := make(map[int64]bool)
	for _, event := range first.Items {
		seen[event.ID] = true
		if event.AgentID != a.Agent.ID || (event.Action != "key.rotate" && event.Action != "key.revoke") {
			t.Errorf("multi-action history returned unrelated event: %+v", event)
		}
	}
	cursor := *first.NextCursor
	for pageNo := 1; pageNo < 3; pageNo++ {
		pageQuery := url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "limit": {"2"}, "cursor": {cursor}}
		pageQuery.Add("action", "key.rotate")
		pageQuery.Add("action", "key.revoke")
		pageQuery.Add("action", "")
		page, _ := agtEvents(t, h, adminToken, pageQuery)
		wantItems := 2
		if pageNo == 2 {
			wantItems = 1
		}
		if page.Total != 5 || len(page.Items) != wantItems {
			t.Errorf("multi-action page %d has total=%d items=%d, want total=5 items=%d", pageNo+1, page.Total, len(page.Items), wantItems)
		}
		for _, event := range page.Items {
			if seen[event.ID] {
				t.Errorf("event %d repeated across action-filtered pages", event.ID)
			}
			seen[event.ID] = true
			if event.AgentID != a.Agent.ID || (event.Action != "key.rotate" && event.Action != "key.revoke") {
				t.Errorf("multi-action history returned unrelated event: %+v", event)
			}
		}
		if pageNo < 2 {
			if page.NextCursor == nil {
				t.Fatalf("multi-action page %d has no cursor before history is exhausted", pageNo+1)
			}
			cursor = *page.NextCursor
		} else if page.NextCursor != nil {
			t.Errorf("last multi-action page unexpectedly has cursor %q", *page.NextCursor)
		}
	}
	if len(seen) != 5 {
		t.Errorf("multi-action pagination returned %d unique events, want 5", len(seen))
	}

	singleQuery := url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "action": {"key.rotate"}, "limit": {"100"}}
	single, _ := agtEvents(t, h, adminToken, singleQuery)
	if single.Total != 3 || len(single.Items) != 3 {
		t.Errorf("single action filter total/items=%d/%d, want 3/3", single.Total, len(single.Items))
	}
	duplicateQuery := url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "limit": {"100"}}
	duplicateQuery.Add("action", "key.rotate")
	duplicateQuery.Add("action", "key.rotate")
	duplicateQuery.Add("action", "")
	duplicate, _ := agtEvents(t, h, adminToken, duplicateQuery)
	if duplicate.Total != 3 || len(duplicate.Items) != 3 {
		t.Errorf("duplicate action values inflated results: total/items=%d/%d, want 3/3", duplicate.Total, len(duplicate.Items))
	}
	unknownQuery := url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "action": {"key.future"}, "limit": {"100"}}
	unknown, _ := agtEvents(t, h, adminToken, unknownQuery)
	if unknown.Total != 0 || len(unknown.Items) != 0 {
		t.Errorf("unknown nonempty action filter returned total/items=%d/%d, want 0/0", unknown.Total, len(unknown.Items))
	}
	tooLong := url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}}
	tooLong.Add("action", strings.Repeat("x", 33))
	invalid := agtAdmin(h, adminToken, http.MethodGet, "/api/admin/agents/events?"+tooLong.Encode(), "")
	agtExpectError(t, agtResult{code: invalid.code, body: invalid.body, header: invalid.header, panicked: invalid.panicked}, http.StatusBadRequest, "logs_filter_invalid", "agent event action filter over 32 characters")

	var allCount int64
	if err := h.db.Model(&core.AgentEvent{}).Where("agent_id = ?", a.Agent.ID).Count(&allCount).Error; err != nil {
		t.Fatalf("count all agent events: %v", err)
	}
	emptyQuery := url.Values{"agent_id": {fmt.Sprint(a.Agent.ID)}, "action": {""}, "limit": {"100"}}
	empty, _ := agtEvents(t, h, adminToken, emptyQuery)
	if int64(empty.Total) != allCount {
		t.Errorf("empty action value was not ignored: total=%d, agent has %d events", empty.Total, allCount)
	}
}

// agentRegressionInstallTrigger confines deliberate persistence faults to the fresh
// database owned by newAccessHarness. The trigger is removed before that database closes.
func agentRegressionInstallTrigger(t *testing.T, h *accessHarness, name, table, operation, condition string) {
	t.Helper()
	if operation != "INSERT" && operation != "UPDATE" {
		t.Fatalf("unsupported isolated trigger operation %q", operation)
	}
	function := name + "_fn"
	functionSQL := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF %s THEN RAISE EXCEPTION 'agent audit fault fixture'; END IF;
  RETURN NEW;
END; $$`, function, condition)
	if err := h.db.Exec(functionSQL).Error; err != nil {
		t.Fatalf("create isolated fault function %s: %v", function, err)
	}
	if err := h.db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION %s()", name, operation, table, function)).Error; err != nil {
		t.Fatalf("create isolated fault trigger %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := h.db.Exec(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s", name, table)).Error; err != nil {
			t.Errorf("drop isolated fault trigger %s: %v", name, err)
		}
		if err := h.db.Exec(fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", function)).Error; err != nil {
			t.Errorf("drop isolated fault function %s: %v", function, err)
		}
	})
}

func TestAgentRegressionAuditEventInsertFailurePreventsBusinessWrite(t *testing.T) {
	h := newAccessHarness(t)
	_, _, writer := agtSetup(t, h, "Audit event insert fault", "posts.read", "posts.write")
	agentRegressionInstallTrigger(t, h, "agent_regression_reject_agent_event", "agent_events", "INSERT", "NEW.actor_type = 'agent'")
	const title = "audit-event-fault-post-must-not-exist"
	r := agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"audit-event-fault-post-must-not-exist","content":"fault fixture","status":"draft"}`, writer.APIKey, "")
	if r.code != http.StatusInternalServerError {
		t.Errorf("POST with agent_events insert fault returned %d, want 500; body=%q", r.code, r.body)
	}
	var posts int64
	if err := h.db.Model(&core.Post{}).Where("title = ?", title).Count(&posts).Error; err != nil {
		t.Fatalf("count business posts after audit insert fault: %v", err)
	}
	if posts != 0 {
		t.Errorf("failed audit insert left %d business post(s), want no partial write", posts)
	}
	requestID := r.header.Get(echo.HeaderXRequestID)
	var eventRows, auditRows int64
	if err := h.db.Model(&core.AgentEvent{}).Where("request_id = ? AND actor_type = 'agent'", requestID).Count(&eventRows).Error; err != nil {
		t.Fatalf("count agent event rows after failed insert: %v", err)
	}
	if err := h.db.Table("system_logs").Where("request_id = ? AND source = 'audit'", requestID).Count(&auditRows).Error; err != nil {
		t.Fatalf("count system audit rows after failed insert: %v", err)
	}
	if eventRows != 0 || auditRows != 0 {
		t.Errorf("failed audit insert left partial rows for %q: agent_events=%d system_logs=%d", requestID, eventRows, auditRows)
	}
}

func TestAgentRegressionAuditSystemLogInsertFailurePreventsBusinessWrite(t *testing.T) {
	h := newAccessHarness(t)
	_, _, writer := agtSetup(t, h, "Audit syslog insert fault", "posts.read", "posts.write")
	agentRegressionInstallTrigger(t, h, "agent_regression_reject_request_syslog", "system_logs", "INSERT", "NEW.source = 'audit' AND NEW.fields->>'action' = 'request'")
	const title = "audit-syslog-fault-post-must-not-exist"
	r := agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"audit-syslog-fault-post-must-not-exist","content":"fault fixture","status":"draft"}`, writer.APIKey, "")
	if r.code != http.StatusInternalServerError {
		t.Errorf("POST with audit system_logs insert fault returned %d, want 500; body=%q", r.code, r.body)
	}
	var posts, eventRows, auditRows int64
	if err := h.db.Model(&core.Post{}).Where("title = ?", title).Count(&posts).Error; err != nil {
		t.Fatalf("count business posts after audit system log fault: %v", err)
	}
	if err := h.db.Model(&core.AgentEvent{}).Where("request_id = ? AND actor_type = 'agent'", r.header.Get(echo.HeaderXRequestID)).Count(&eventRows).Error; err != nil {
		t.Fatalf("count agent events after system log fault: %v", err)
	}
	if err := h.db.Table("system_logs").Where("request_id = ? AND source = 'audit' AND fields->>'action' = 'request'", r.header.Get(echo.HeaderXRequestID)).Count(&auditRows).Error; err != nil {
		t.Fatalf("count system audit rows after system log fault: %v", err)
	}
	if posts != 0 || eventRows != 0 || auditRows != 0 {
		t.Errorf("audit system log failure left partial state: posts=%d agent_events=%d system_logs=%d", posts, eventRows, auditRows)
	}
}

func TestAgentRegressionAuditFinalizeFailureRetainsPendingRecord(t *testing.T) {
	h := newAccessHarness(t)
	_, _, writer := agtSetup(t, h, "Audit finalize fault", "posts.read", "posts.write")
	agentRegressionInstallTrigger(t, h, "agent_regression_reject_event_finalize", "agent_events", "UPDATE", "OLD.actor_type = 'agent' AND OLD.status = 0")
	const title = "audit-finalize-fault-post-succeeds"
	r := agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"audit-finalize-fault-post-succeeds","content":"fault fixture","status":"draft"}`, writer.APIKey, "")
	if r.code < 200 || r.code >= 300 {
		t.Fatalf("POST with audit finalize fault returned %d, want successful business write; body=%q", r.code, r.body)
	}
	var posts int64
	if err := h.db.Model(&core.Post{}).Where("title = ?", title).Count(&posts).Error; err != nil {
		t.Fatalf("count business posts after finalize fault: %v", err)
	}
	if posts != 1 {
		t.Errorf("successful POST left %d posts, want 1", posts)
	}
	requestID := r.header.Get(echo.HeaderXRequestID)
	var events []core.AgentEvent
	if err := h.db.Where("request_id = ? AND actor_type = 'agent' AND action = 'request'", requestID).Find(&events).Error; err != nil {
		t.Fatalf("read pending agent request event: %v", err)
	}
	if len(events) != 1 || events[0].Status != 0 {
		t.Errorf("finalize failure should preserve one status-0 request event; got %+v", events)
	}
	var logs []struct {
		Fields []byte `gorm:"column:fields"`
	}
	if err := h.db.Table("system_logs").Select("fields::text AS fields").Where("request_id = ? AND source = 'audit' AND message = 'request'", requestID).Scan(&logs).Error; err != nil {
		t.Fatalf("read pending system audit log: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("finalize failure should preserve one system audit row; got %+v", logs)
	}
	var fields map[string]any
	if err := json.Unmarshal(logs[0].Fields, &fields); err != nil {
		t.Fatalf("decode pending system audit fields %q: %v", logs[0].Fields, err)
	}
	if status, ok := fields["status"].(float64); !ok || status != 0 {
		t.Errorf("finalize failure should preserve status 0 in system audit fields; got %v", fields["status"])
	}
}

func TestAgentRegressionSettingsMixedTypesRejectAndSecretMasksMatchAdmin(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, reader := agtSetup(t, h, "Settings regression", "settings.read", "settings.write")
	before := core.Option{Key: "site_title", Value: "before"}
	if err := h.db.Save(&before).Error; err != nil {
		t.Fatalf("seed site title: %v", err)
	}
	if err := h.db.Save(&core.Option{Key: "permalink_post", Value: "/post/{slug}"}).Error; err != nil {
		t.Fatalf("seed post permalink: %v", err)
	}
	r := agtDo(h, http.MethodPut, "/api/agent/v1/settings", `{"site_title":"after","maintenance_enabled":true,"permalink_post":"/archive/{unknown}"}`, reader.APIKey, "")
	if r.code != http.StatusBadRequest {
		t.Errorf("mixed-type settings write with invalid permalink returned %d, want 400; body=%q", r.code, r.body)
	}
	var title, permalink core.Option
	if err := h.db.Where("key = ?", "site_title").First(&title).Error; err != nil {
		t.Fatalf("read title after rejected settings write: %v", err)
	}
	if err := h.db.Where("key = ?", "permalink_post").First(&permalink).Error; err != nil {
		t.Fatalf("read permalink after rejected settings write: %v", err)
	}
	if title.Value != before.Value || permalink.Value != "/post/{slug}" {
		t.Errorf("rejected mixed settings write partially changed options: title=%q permalink=%q", title.Value, permalink.Value)
	}

	for _, secret := range []string{"x", "xxxx", "xxxxx", "xxxxxxx", "xxxxxxxx", "秘密abc"} {
		if err := h.db.Save(&core.Option{Key: "oauth_google_client_secret", Value: secret}).Error; err != nil {
			t.Fatalf("seed secret %q: %v", secret, err)
		}
		readAgent := agtDo(h, http.MethodGet, "/api/agent/v1/settings", "", reader.APIKey, "")
		readAdmin := agtAdmin(h, adminToken, http.MethodGet, "/api/admin/settings", "")
		if readAgent.code != http.StatusOK || readAdmin.code != http.StatusOK {
			t.Fatalf("read settings for secret %q returned agent=%d admin=%d", secret, readAgent.code, readAdmin.code)
		}
		var agentOptions, adminOptions []core.Option
		agtDecode(t, readAgent, &agentOptions, "agent secret settings")
		agtDecode(t, readAdmin, &adminOptions, "admin secret settings")
		value := func(options []core.Option) string {
			for _, o := range options {
				if o.Key == "oauth_google_client_secret" {
					return o.Value
				}
			}
			return "<missing>"
		}
		if got, want := value(agentOptions), value(adminOptions); got != want {
			t.Errorf("secret %q displays differently between agent (%q) and admin (%q)", secret, got, want)
		}
	}
}

func TestAgentRegressionConcurrentDuplicateNamesReturnConflict(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, first := agtSetup(t, h, "Initial rename A", "posts.read")
	second := agtCreate(t, h, adminToken, "Initial rename B", "posts.read")

	const racers = 2
	createResults := make([]agtResult, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range createResults {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			createResults[i] = agtAdmin(h, adminToken, http.MethodPost, "/api/admin/agents", agtCreateBody(strings.ToUpper("same concurrent name"), "posts.read"))
		}(i)
	}
	close(start)
	wg.Wait()
	assertSingleConflict := func(label string, results []agtResult, success int) {
		t.Helper()
		wins, conflicts := 0, 0
		for i, result := range results {
			switch result.code {
			case success:
				wins++
			case http.StatusConflict:
				conflicts++
				agtExpectError(t, result, http.StatusConflict, "agent_name_taken", fmt.Sprintf("%s racer %d", label, i))
			default:
				t.Errorf("%s racer %d returned %d, want %d or 409; body=%q", label, i, result.code, success, result.body)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Errorf("%s wins=%d conflicts=%d, want one each; results=%+v", label, wins, conflicts, results)
		}
	}
	assertSingleConflict("concurrent create", createResults, http.StatusCreated)

	renameResults := make([]agtResult, racers)
	start = make(chan struct{})
	ids := []uint{first.Agent.ID, second.Agent.ID}
	for i := range renameResults {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			renameResults[i] = agtAdmin(h, adminToken, http.MethodPatch, fmt.Sprintf("/api/admin/agents/%d", ids[i]), `{"name":"Shared concurrent rename"}`)
		}(i)
	}
	close(start)
	wg.Wait()
	assertSingleConflict("concurrent rename", renameResults, http.StatusOK)
}

func TestAgentRegressionConcurrentAgentPatchesDoNotResurrectPermissions(t *testing.T) {
	h := newAccessHarness(t)
	_, adminToken, created := agtSetup(t, h, "Patch lock sequencing", "posts.read", "media.read")
	locker := h.db.Begin()
	if locker.Error != nil {
		t.Fatalf("begin patch lock transaction: %v", locker.Error)
	}
	var locked core.Agent
	if err := locker.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, created.Agent.ID).Error; err != nil {
		_ = locker.Rollback().Error
		t.Fatalf("lock agent row: %v", err)
	}

	permissionDone := make(chan agtResult, 1)
	go func() {
		permissionDone <- agtAdmin(h, adminToken, http.MethodPatch, fmt.Sprintf("/api/admin/agents/%d", created.Agent.ID), `{"permissions":["media.read"]}`)
	}()
	waitForAgentRegressionLockWaiters(t, h.db, 1)
	nameDone := make(chan agtResult, 1)
	go func() {
		nameDone <- agtAdmin(h, adminToken, http.MethodPatch, fmt.Sprintf("/api/admin/agents/%d", created.Agent.ID), `{"name":"Patch lock winner"}`)
	}()
	waitForAgentRegressionLockWaiters(t, h.db, 2)
	if err := locker.Commit().Error; err != nil {
		t.Fatalf("release patch lock: %v", err)
	}
	permissionResult, nameResult := <-permissionDone, <-nameDone
	if permissionResult.code != http.StatusOK || nameResult.code != http.StatusOK {
		t.Fatalf("concurrent patches returned permissions=%d (%s), rename=%d (%s); want 200 each", permissionResult.code, permissionResult.body, nameResult.code, nameResult.body)
	}
	detail := agtDetail(t, h, adminToken, created.Agent.ID)
	if detail.Name != "Patch lock winner" || len(detail.Permissions) != 1 || detail.Permissions[0] != "media.read" {
		t.Errorf("concurrent rename restored stale permissions: name=%q permissions=%v, want Patch lock winner and [media.read]", detail.Name, detail.Permissions)
	}
}

func TestAgentRegressionConcurrentPostTypeSwitchCannotDeleteWrongContent(t *testing.T) {
	h := newAccessHarness(t)
	admin, _, agent := agtSetup(t, h, "Delete type lock sequencing", "posts.write", "pages.write")
	post := core.Post{Title: "post type race", Slug: "agent-regression-delete-type-post", Content: "keep", Type: "post", Status: "draft", AuthorID: admin.ID}
	page := core.Post{Title: "page type race", Slug: "agent-regression-delete-type-page", Content: "keep", Type: "page", Status: "draft", AuthorID: admin.ID}
	for _, item := range []*core.Post{&post, &page} {
		if err := h.db.Create(item).Error; err != nil {
			t.Fatalf("seed %s: %v", item.Slug, err)
		}
	}
	tests := []struct {
		label, route, afterType string
		item                    *core.Post
	}{
		{"post delete switched to page", "posts", "page", &post},
		{"page delete switched to post", "pages", "post", &page},
	}
	for _, tc := range tests {
		locker := h.db.Begin()
		if locker.Error != nil {
			t.Fatalf("begin %s lock: %v", tc.label, locker.Error)
		}
		var locked core.Post
		if err := locker.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, tc.item.ID).Error; err != nil {
			_ = locker.Rollback().Error
			t.Fatalf("lock %s row: %v", tc.label, err)
		}
		deleteDone := make(chan agtResult, 1)
		go func(path string, id uint) {
			deleteDone <- agtDo(h, http.MethodDelete, fmt.Sprintf("/api/agent/v1/%s/%d", path, id), "", agent.APIKey, "")
		}(tc.route, tc.item.ID)
		waitForAgentRegressionLockWaiters(t, h.db, 1)
		if err := locker.Model(&core.Post{}).Where("id = ?", tc.item.ID).Update("type", tc.afterType).Error; err != nil {
			_ = locker.Rollback().Error
			t.Fatalf("switch %s type to %s: %v", tc.label, tc.afterType, err)
		}
		if err := locker.Commit().Error; err != nil {
			t.Fatalf("release %s lock: %v", tc.label, err)
		}
		result := <-deleteDone
		agtExpectError(t, result, http.StatusNotFound, "not_found", tc.label)
		var stored core.Post
		if err := h.db.Unscoped().First(&stored, tc.item.ID).Error; err != nil {
			t.Fatalf("read %s after rejected delete: %v", tc.label, err)
		}
		if stored.Type != tc.afterType || stored.DeletedAt.Valid {
			t.Errorf("%s deleted or corrupted row after type switch: type=%q deleted=%v", tc.label, stored.Type, stored.DeletedAt.Valid)
		}
	}
}

func waitForAgentRegressionLockWaiters(t *testing.T, db *gorm.DB, want int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := db.Raw(`SELECT COUNT(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND state = 'active' AND wait_event_type = 'Lock'`).Scan(&count).Error; err != nil {
			t.Fatalf("read PostgreSQL lock waiters: %v", err)
		}
		if count >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d PostgreSQL lock waiter(s)", want)
}

func TestAgentRegressionChunkedBodyAuditCountsBytesAndOversizeIsAudited(t *testing.T) {
	h := newAccessHarness(t)
	owner, _, agent := agtSetup(t, h, "Body accounting", "posts.read", "posts.write", "products.write")
	body := `{"title":"chunked-audit-body","content":"x","status":"draft"}`
	request := httptest.NewRequest(http.MethodPost, "/api/agent/v1/posts", io.NopCloser(bytes.NewBufferString(body)))
	request.ContentLength = -1
	request.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	request.Header.Set(echo.HeaderAuthorization, "Bearer "+agent.APIKey)
	recorder := httptest.NewRecorder()
	h.echo.ServeHTTP(recorder, request)
	var response struct {
		ID uint `json:"id"`
	}
	if recorder.Code < 200 || recorder.Code >= 300 {
		t.Fatalf("chunked POST returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.ID == 0 {
		t.Fatalf("chunked POST response=%q err=%v", recorder.Body.String(), err)
	}
	var summary map[string]any
	var event core.AgentEvent
	if err := h.db.Where("request_id = ? AND actor_type = 'agent'", recorder.Header().Get(echo.HeaderXRequestID)).First(&event).Error; err != nil {
		t.Fatalf("read chunked request event: %v", err)
	}
	if err := json.Unmarshal(event.Summary, &summary); err != nil {
		t.Fatalf("decode chunked request summary %q: %v", event.Summary, err)
	}
	if got, _ := summary["body_bytes"].(float64); got != float64(len(body)) {
		t.Errorf("chunked request body_bytes=%v, want %d", summary["body_bytes"], len(body))
	}

	large := strings.Repeat("x", 2*1024*1024+1)
	over := agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"`+large+`"}`, agent.APIKey, "")
	agtExpectError(t, over, http.StatusRequestEntityTooLarge, "payload_too_large", "matched-key oversized request")
	var rows []core.AgentEvent
	if err := h.db.Where("agent_id = ? AND request_id = ? AND actor_type = 'agent'", agent.Agent.ID, over.header.Get(echo.HeaderXRequestID)).Find(&rows).Error; err != nil {
		t.Fatalf("read oversized request audit: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != http.StatusRequestEntityTooLarge || rows[0].KeyID == nil || *rows[0].KeyID != agent.Key.ID {
		t.Errorf("oversized request audit rows=%+v, want one matched-key 413 event", rows)
	}

	post := core.Post{Title: "chunked oversize post baseline", Slug: "agent-regression-chunked-oversize-post", Content: "keep", Type: "post", Status: "draft", AuthorID: owner.ID}
	if err := h.db.Create(&post).Error; err != nil {
		t.Fatalf("seed post for chunked oversize PUT: %v", err)
	}
	product := core.Product{Title: "chunked oversize product baseline", Slug: "agent-regression-chunked-oversize-product", Price: 100, Stock: 1, Status: "active"}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("seed product for chunked oversize PUT: %v", err)
	}
	largeContent := strings.Repeat("x", 2*1024*1024+1)
	chunkedOversize := []struct {
		label, method, path, body string
	}{
		{"POST posts", http.MethodPost, "/api/agent/v1/posts", `{"title":"chunked oversized post","content":"` + largeContent + `","status":"draft"}`},
		{"PUT post", http.MethodPut, fmt.Sprintf("/api/agent/v1/posts/%d", post.ID), fmt.Sprintf(`{"base_updated_at":%q,"content":%q}`, post.UpdatedAt.Format(time.RFC3339Nano), largeContent)},
		{"PUT product", http.MethodPut, fmt.Sprintf("/api/agent/v1/products/%d", product.ID), fmt.Sprintf(`{"base_updated_at":%q,"title":%q}`, product.UpdatedAt.Format(time.RFC3339Nano), largeContent)},
	}
	for _, tc := range chunkedOversize {
		request := httptest.NewRequest(tc.method, tc.path, io.NopCloser(strings.NewReader(tc.body)))
		request.ContentLength = -1
		request.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
		request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		request.Header.Set(echo.HeaderAuthorization, "Bearer "+agent.APIKey)
		recorder := httptest.NewRecorder()
		h.echo.ServeHTTP(recorder, request)
		result := agtResult{code: recorder.Code, body: recorder.Body.String(), header: recorder.Header().Clone()}
		agtExpectError(t, result, http.StatusRequestEntityTooLarge, "payload_too_large", tc.label+" chunked oversized request")
		var audit core.AgentEvent
		if err := h.db.Where("agent_id = ? AND request_id = ? AND actor_type = 'agent'", agent.Agent.ID, result.header.Get(echo.HeaderXRequestID)).First(&audit).Error; err != nil {
			t.Errorf("%s: read chunked oversized request audit: %v", tc.label, err)
			continue
		}
		if audit.Status != http.StatusRequestEntityTooLarge || audit.KeyID == nil || *audit.KeyID != agent.Key.ID {
			t.Errorf("%s chunked oversized audit=%+v, want matched-key status 413", tc.label, audit)
		}
	}
	var afterPost core.Post
	if err := h.db.First(&afterPost, post.ID).Error; err != nil {
		t.Fatalf("read post after chunked oversize requests: %v", err)
	}
	var afterProduct core.Product
	if err := h.db.First(&afterProduct, product.ID).Error; err != nil {
		t.Fatalf("read product after chunked oversize requests: %v", err)
	}
	if afterPost.Title != post.Title || afterPost.Content != post.Content || afterProduct.Title != product.Title {
		t.Errorf("chunked oversized requests partially wrote content: post=%+v product=%+v", afterPost, afterProduct)
	}
	var oversizedPosts int64
	if err := h.db.Model(&core.Post{}).Where("title = ?", "chunked oversized post").Count(&oversizedPosts).Error; err != nil {
		t.Fatalf("count post from chunked oversized POST: %v", err)
	}
	if oversizedPosts != 0 {
		t.Errorf("chunked oversized POST created %d post(s), want none", oversizedPosts)
	}
	var posts int64
	if err := h.db.Model(&core.Post{}).Where("title = ?", "chunked-audit-body").Count(&posts).Error; err != nil {
		t.Fatalf("count chunked business post: %v", err)
	}
	if posts != 1 {
		t.Errorf("chunked request persisted %d posts, want one; oversized body must not write", posts)
	}

	query := url.Values{}
	for i := 0; i < 90; i++ {
		query.Set(fmt.Sprintf("query_key_%03d_with_long_name", i), "sensitive-query-value")
	}
	largeSummary := agtDo(h, http.MethodGet, "/api/agent/v1/me?"+query.Encode(), "", agent.APIKey, "")
	if largeSummary.code != http.StatusOK {
		t.Errorf("GET /me with many query keys returned %d: %s", largeSummary.code, largeSummary.body)
	}
	var summaryEvent core.AgentEvent
	if err := h.db.Where("request_id = ? AND actor_type = 'agent'", largeSummary.header.Get(echo.HeaderXRequestID)).First(&summaryEvent).Error; err != nil {
		t.Fatalf("read bounded summary event: %v", err)
	}
	var bounded map[string]any
	if err := json.Unmarshal(summaryEvent.Summary, &bounded); err != nil {
		t.Fatalf("decode bounded summary %q: %v", summaryEvent.Summary, err)
	}
	if len(summaryEvent.Summary) > 2048 || bounded["truncated"] != true {
		t.Errorf("large-query summary size=%d truncated=%v, want <=2048 and true", len(summaryEvent.Summary), bounded["truncated"])
	}
	if strings.Contains(string(summaryEvent.Summary), "sensitive-query-value") {
		t.Error("request summary contains query parameter values")
	}
}

func TestAgentRegressionDiscoveryAndOpenAPISchemasMatchHandlers(t *testing.T) {
	h := newAccessHarness(t)
	indexResult := agtDo(h, http.MethodGet, "/api/agent/v1", "", "", "")
	var index map[string]any
	if indexResult.code != http.StatusOK {
		t.Fatalf("discovery returned %d: %s", indexResult.code, indexResult.body)
	}
	agtDecode(t, indexResult, &index, "agent discovery")
	for _, field := range []string{"version", "skill_path", "concurrency"} {
		if _, ok := index[field]; !ok {
			t.Errorf("discovery response is missing %q", field)
		}
	}
	endpoints, _ := index["endpoints"].([]any)
	foundGuard := false
	for _, raw := range endpoints {
		endpoint, _ := raw.(map[string]any)
		if endpoint["path"] == "/api/agent/v1/posts/{id}" && endpoint["method"] == "PUT" && endpoint["version_guard"] == true {
			foundGuard = true
		}
	}
	if !foundGuard {
		t.Error("discovery endpoints omit guarded PUT /api/agent/v1/posts/{id}")
	}

	openapi := agtDo(h, http.MethodGet, "/api/agent/v1/openapi.json", "", "", "")
	var doc map[string]any
	if openapi.code != http.StatusOK {
		t.Fatalf("openapi returned %d: %s", openapi.code, openapi.body)
	}
	agtDecode(t, openapi, &doc, "agent OpenAPI")
	paths, _ := doc["paths"].(map[string]any)
	operation := func(path, method string) map[string]any {
		t.Helper()
		item, _ := paths[path].(map[string]any)
		op, _ := item[method].(map[string]any)
		return op
	}
	responseSchemaStatus := func(path, method, status string) map[string]any {
		t.Helper()
		op := operation(path, method)
		responses, _ := op["responses"].(map[string]any)
		okResp, _ := responses[status].(map[string]any)
		content, _ := okResp["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		schema, _ := media["schema"].(map[string]any)
		return schema
	}
	responseSchema := func(path, method string) map[string]any {
		t.Helper()
		return responseSchemaStatus(path, method, "200")
	}
	if got := responseSchema("/api/agent/v1/posts", "post")["type"]; got != "object" {
		t.Errorf("POST /posts success schema type=%v, want object", got)
	}
	if got := responseSchema("/api/agent/v1/pages", "post")["type"]; got != "object" {
		t.Errorf("POST /pages success schema type=%v, want object", got)
	}
	for _, collection := range []struct{ path, method string }{
		{"/api/agent/v1/posts", "get"}, {"/api/agent/v1/pages", "get"},
	} {
		listSchema := responseSchema(collection.path, collection.method)
		listItems, _ := listSchema["items"].(map[string]any)
		itemProps, _ := listItems["properties"].(map[string]any)
		for _, field := range []string{"id", "title"} {
			if itemProps[field] == nil {
				t.Errorf("%s list item schema omits %q: %v", collection.path, field, listItems)
			}
		}
	}
	for _, collection := range []string{"posts", "pages"} {
		path := "/api/agent/v1/" + collection
		post := operation(path, "post")
		requestBody, _ := post["requestBody"].(map[string]any)
		requestContent, _ := requestBody["content"].(map[string]any)
		requestMedia, _ := requestContent["application/json"].(map[string]any)
		requestSchema, _ := requestMedia["schema"].(map[string]any)
		requestProps, _ := requestSchema["properties"].(map[string]any)
		for _, field := range []string{"title", "content"} {
			if requestProps[field] == nil {
				t.Errorf("POST %s request body omits %q: %v", path, field, requestSchema)
			}
		}
		responseProps, _ := responseSchema(path, "post")["properties"].(map[string]any)
		for _, field := range []string{"id", "title"} {
			if responseProps[field] == nil {
				t.Errorf("POST %s success object omits %q: %v", path, field, responseProps)
			}
		}
	}
	themes := responseSchema("/api/agent/v1/themes", "get")
	items, _ := themes["items"].(map[string]any)
	isArray := themes["type"] == "array"
	if types, ok := themes["type"].([]any); ok {
		for _, typ := range types {
			isArray = isArray || typ == "array"
		}
	}
	if !isArray || items["type"] != "string" {
		t.Errorf("GET /themes success schema=%v, want array or nullable array of strings", themes)
	}
	activation := responseSchema("/api/agent/v1/themes/activate", "post")
	properties, _ := activation["properties"].(map[string]any)
	if activation["type"] != "object" || properties["message"] == nil {
		t.Errorf("POST /themes/activate success schema=%v, want object with message", activation)
	}
	mediaFile := responseSchemaStatus("/api/agent/v1/media/files", "post", "201")
	mediaProps, _ := mediaFile["properties"].(map[string]any)
	if mediaFile["type"] != "object" || mediaFile["items"] != nil || mediaProps["id"] == nil || mediaProps["url"] == nil {
		t.Errorf("POST /media/files 201 schema=%v, want a single object with id and url", mediaFile)
	}
	put := operation("/api/agent/v1/posts/{id}", "put")
	body, _ := put["requestBody"].(map[string]any)
	bodyContent, _ := body["content"].(map[string]any)
	jsonMedia, _ := bodyContent["application/json"].(map[string]any)
	requestSchema, _ := jsonMedia["schema"].(map[string]any)
	requestProps, _ := requestSchema["properties"].(map[string]any)
	for _, field := range []string{"base_updated_at", "title", "slug", "content", "source", "format", "status", "excerpt"} {
		if _, ok := requestProps[field]; !ok {
			t.Errorf("PUT /posts/{id} request schema omits accepted field %q", field)
		}
	}
}

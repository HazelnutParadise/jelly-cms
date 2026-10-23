package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// Acceptance tests for the notification center (docs/tickets/06-accounts.md,
// D5: 通知、S19 to S21、A-series 待審與安全事件). Fixtures and login helpers are
// shared with the token tests (tokTest*).

// ntfListBody is the S19 response shape.
type ntfListBody struct {
	Items []struct {
		ID       uint       `json:"id"`
		Kind     string     `json:"kind"`
		Category string     `json:"category"`
		GroupKey string     `json:"group_key"`
		Count    int        `json:"count"`
		ReadAt   *time.Time `json:"read_at"`
	} `json:"items"`
	UnreadCount int            `json:"unread_count"`
	Counts      map[string]int `json:"counts"`
}

// ntfSeed inserts a notification row directly. ExpiresAt follows the ticket's
// rule of 90 days from creation.
func ntfSeed(t *testing.T, h *accessHarness, userID uint, kind, category, groupKey string, read bool, createdAt time.Time) core.Notification {
	t.Helper()
	n := core.Notification{
		UserID: userID, Kind: kind, Category: category, TitleKey: "notify." + kind,
		GroupKey: groupKey, Count: 1, CreatedAt: createdAt, ExpiresAt: createdAt.Add(90 * 24 * time.Hour),
	}
	if read {
		at := createdAt
		n.ReadAt = &at
	}
	if err := h.db.Create(&n).Error; err != nil {
		t.Fatalf("seed notification %s: %v", kind, err)
	}
	return n
}

// ntfList calls GET /api/account/notifications with an optional query string.
func ntfList(t *testing.T, h *accessHarness, jwt, query string) (accessResult, ntfListBody) {
	t.Helper()
	path := "/api/account/notifications"
	if query != "" {
		path += "?" + query
	}
	res := tokTestCall(t, h, http.MethodGet, path, "", jwt)
	var body ntfListBody
	if res.code == http.StatusOK {
		if err := json.Unmarshal([]byte(res.body), &body); err != nil {
			t.Errorf("decode GET %s: %v body=%q", path, err, res.body)
		}
	} else {
		t.Errorf("GET %s returned %d, want 200; body=%q", path, res.code, res.body)
	}
	return res, body
}

// ntfRows returns the notification rows of one user, optionally of one kind,
// read straight from the table.
func ntfRows(t *testing.T, h *accessHarness, userID uint, kind string) []core.Notification {
	t.Helper()
	var rows []core.Notification
	q := h.db.Where("user_id = ?", userID)
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	if err := q.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load notifications for user %d: %v", userID, err)
	}
	return rows
}

// ntfCount runs a single-value COUNT query.
func ntfCount(t *testing.T, h *accessHarness, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := h.db.Raw(query, args...).Row().Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// ntfString runs a single-value text query; COALESCE in the query keeps it to one row.
func ntfString(t *testing.T, h *accessHarness, query string, args ...any) string {
	t.Helper()
	var s string
	if err := h.db.Raw(query, args...).Row().Scan(&s); err != nil {
		t.Fatalf("read %q: %v", query, err)
	}
	return s
}

// ntfGuestComment posts a guest comment from a fresh address and expects 201.
func ntfGuestComment(t *testing.T, h *accessHarness, postID uint, name string) {
	t.Helper()
	body := fmt.Sprintf(`{"post_id":%d,"name":%q,"email":"guest@example.test","body":"hello from %s","website":""}`, postID, name, name)
	res := tokTestCall(t, h, http.MethodPost, "/api/comments", body, "")
	if res.code != http.StatusCreated {
		t.Fatalf("guest POST /api/comments returned %d, want 201; body=%q", res.code, res.body)
	}
}

// ntfPublishedPost seeds a published, public post owned by author.
func ntfPublishedPost(t *testing.T, h *accessHarness, author core.User, slug string) core.Post {
	t.Helper()
	published := time.Now().Add(-time.Hour)
	p := core.Post{Title: "Post " + slug, Slug: slug, Content: "content " + slug, Type: "post", Status: "published", Visibility: "public", AuthorID: author.ID, PublishedAt: &published}
	if err := h.db.Create(&p).Error; err != nil {
		t.Fatalf("seed post %s: %v", slug, err)
	}
	return p
}

// ntfPrefs returns a PUT body for notification-preferences.
func ntfPrefs(events string, webhook string, quiet string) string {
	return fmt.Sprintf(`{"events":%s,"webhook_url":%q,"quiet":%s}`, events, webhook, quiet)
}

// --- S19: own rows only, category counts -------------------------------------

func TestNotificationsListOwnOnlyAndCategoryCounts(t *testing.T) {
	h := newAccessHarness(t)
	a := tokTestUser(t, h, "ntf-list-a", "author")
	b := tokTestUser(t, h, "ntf-list-b", "author")
	now := time.Now()

	ownIDs := map[uint]bool{}
	for _, n := range []core.Notification{
		ntfSeed(t, h, a.ID, "comment.pending", "comments", "comment.pending:101", false, now.Add(-3*time.Minute)),
		ntfSeed(t, h, a.ID, "comment.reply", "comments", "comment.reply:102", false, now.Add(-2*time.Minute)),
		ntfSeed(t, h, a.ID, "order.new", "orders", "order.new:103", false, now.Add(-time.Minute)),
		ntfSeed(t, h, a.ID, "system.update_available", "system", "system.update_available:104", false, now.Add(-time.Minute)),
		ntfSeed(t, h, a.ID, "security.new_device", "security", "security.new_device:105", false, now),
	} {
		ownIDs[n.ID] = true
	}
	for i := 0; i < 3; i++ {
		ntfSeed(t, h, b.ID, "comment.pending", "comments", fmt.Sprintf("comment.pending:2%d", i), false, now)
	}

	jwtA := tokTestLogin(t, h, a.Username)
	_, all := ntfList(t, h, jwtA, "")
	if len(all.Items) != len(ownIDs) {
		t.Errorf("list returned %d items for user A, want %d", len(all.Items), len(ownIDs))
	}
	for _, item := range all.Items {
		if !ownIDs[item.ID] {
			t.Errorf("list leaked notification %d of another user", item.ID)
		}
	}
	want := map[string]int{"all": 5, "unread": 5, "comments": 2, "orders": 1, "system": 1, "security": 1}
	for key, value := range want {
		if all.Counts[key] != value {
			t.Errorf("counts[%s] = %d, want %d; counts=%v", key, all.Counts[key], value, all.Counts)
		}
	}
	if all.UnreadCount != 5 {
		t.Errorf("unread_count = %d, want 5", all.UnreadCount)
	}

	_, comments := ntfList(t, h, jwtA, "category=comments")
	if len(comments.Items) != 2 {
		t.Errorf("category=comments returned %d items, want 2", len(comments.Items))
	}
	for _, item := range comments.Items {
		if item.Category != "comments" {
			t.Errorf("category=comments returned a %q item", item.Category)
		}
	}
}

// --- S20: IDOR returns 404 and leaves the row untouched ----------------------

func TestNotificationsMarkReadIDOR404(t *testing.T) {
	h := newAccessHarness(t)
	a := tokTestUser(t, h, "ntf-idor-a", "author")
	b := tokTestUser(t, h, "ntf-idor-b", "author")
	jwtA := tokTestLogin(t, h, a.Username)
	jwtB := tokTestLogin(t, h, b.Username)
	now := time.Now()
	n := ntfSeed(t, h, a.ID, "comment.pending", "comments", "comment.pending:201", false, now)
	ntfSeed(t, h, a.ID, "order.new", "orders", "order.new:202", false, now)

	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/notifications/%d/read", n.ID), "", jwtB); r.code != http.StatusNotFound {
		t.Errorf("user B marking user A's notification read returned %d, want 404; body=%q", r.code, r.body)
	}
	if rows := ntfRows(t, h, a.ID, ""); rows[0].ReadAt != nil {
		t.Errorf("user B's request changed read_at of user A's notification")
	}

	if r := tokTestCall(t, h, http.MethodPost, fmt.Sprintf("/api/account/notifications/%d/read", n.ID), "", jwtA); r.code != http.StatusOK {
		t.Errorf("owner marking own notification read returned %d, want 200; body=%q", r.code, r.body)
	}
	if rows := ntfRows(t, h, a.ID, "comment.pending"); len(rows) != 1 || rows[0].ReadAt == nil {
		t.Errorf("owner read did not set read_at: %+v", rows)
	}

	if r := tokTestCall(t, h, http.MethodPost, "/api/account/notifications/read-all", "", jwtB); r.code != http.StatusOK {
		t.Errorf("user B read-all returned %d, want 200; body=%q", r.code, r.body)
	}
	if rows := ntfRows(t, h, a.ID, "order.new"); len(rows) != 1 || rows[0].ReadAt != nil {
		t.Errorf("user B read-all changed user A's notification: %+v", rows)
	}
	if r := tokTestCall(t, h, http.MethodPost, "/api/account/notifications/read-all?category=orders", "", jwtA); r.code != http.StatusOK {
		t.Errorf("owner read-all?category=orders returned %d, want 200; body=%q", r.code, r.body)
	}
	if rows := ntfRows(t, h, a.ID, "order.new"); len(rows) != 1 || rows[0].ReadAt == nil {
		t.Errorf("owner read-all?category=orders did not mark the order read: %+v", rows)
	}
}

// --- bell count in the admin layout -----------------------------------------

var ntfBellCount = regexp.MustCompile(`class="bell-count"[^>]*>\s*([^<]+?)\s*<`)

func TestNotificationsBellCountMatchesUnread(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "ntf-bell-admin", "admin")
	jwt := tokTestLogin(t, h, admin.Username)
	now := time.Now()
	for i := 0; i < 3; i++ {
		ntfSeed(t, h, admin.ID, "comment.pending", "comments", fmt.Sprintf("comment.pending:30%d", i), false, now)
	}
	ntfSeed(t, h, admin.ID, "system.update_available", "system", "system.update_available:309", true, now)

	page := tokTestReq(t, h, tokTestRemote(), http.MethodGet, "/admin", "", jwt, map[string]string{echo.HeaderAccept: "text/html"})
	if page.code != http.StatusOK {
		t.Fatalf("GET /admin returned %d, want 200; body=%q", page.code, page.body)
	}
	m := ntfBellCount.FindStringSubmatch(page.body)
	if m == nil {
		t.Fatalf("admin layout rendered no .bell-count for 3 unread notifications")
	}
	if m[1] != "3" {
		t.Errorf("bell count = %q, want 3 (the unread count)", m[1])
	}

	for _, row := range ntfRows(t, h, admin.ID, "") {
		if row.ReadAt == nil {
			h.db.Model(&core.Notification{}).Where("id = ?", row.ID).Update("read_at", time.Now())
		}
	}
	page = tokTestReq(t, h, tokTestRemote(), http.MethodGet, "/admin", "", jwt, map[string]string{echo.HeaderAccept: "text/html"})
	if page.code != http.StatusOK {
		t.Fatalf("GET /admin after read-all returned %d, want 200", page.code)
	}
	if ntfBellCount.MatchString(page.body) {
		t.Errorf("bell still shows a count with zero unread notifications")
	}
}

// --- comment.pending fan-out --------------------------------------------------

func TestNotificationsCommentPendingNotifiesAdminsAndEditorsNotAuthors(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "ntf-cp-admin", "admin")
	editor := tokTestUser(t, h, "ntf-cp-editor", "editor")
	author := tokTestUser(t, h, "ntf-cp-author", "author")
	post := ntfPublishedPost(t, h, author, "ntf-cp-post")

	ntfGuestComment(t, h, post.ID, "Guest")

	for _, recipient := range []core.User{admin, editor} {
		rows := ntfRows(t, h, recipient.ID, "comment.pending")
		if len(rows) != 1 {
			t.Errorf("%s has %d comment.pending notifications, want 1", recipient.Username, len(rows))
			continue
		}
		if rows[0].Category != "comments" {
			t.Errorf("%s comment.pending category = %q, want comments", recipient.Username, rows[0].Category)
		}
		if !strings.Contains(string(rows[0].Actions), "/admin/comments") {
			t.Errorf("%s comment.pending actions = %s, want an action to /admin/comments", recipient.Username, rows[0].Actions)
		}
	}
	if n := len(ntfRows(t, h, author.ID, "")); n != 0 {
		t.Errorf("post author got %d notifications for a guest comment, want none", n)
	}

	jwt := tokTestLogin(t, h, admin.Username)
	_, body := ntfList(t, h, jwt, "category=comments")
	if len(body.Items) != 1 || body.Items[0].Kind != "comment.pending" {
		t.Errorf("admin comments list = %+v, want one comment.pending item", body.Items)
	}
}

// --- coalescing while unread -------------------------------------------------

func TestNotificationsCoalesceSameGroupKeyWhileUnread(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "ntf-coalesce-admin", "admin")
	author := tokTestUser(t, h, "ntf-coalesce-author", "author")
	post := ntfPublishedPost(t, h, author, "ntf-coalesce-post")
	jwt := tokTestLogin(t, h, admin.Username)
	groupKey := fmt.Sprintf("comment.pending:%d", post.ID)

	ntfGuestComment(t, h, post.ID, "First")
	ntfGuestComment(t, h, post.ID, "Second")
	rows := ntfRows(t, h, admin.ID, "comment.pending")
	if len(rows) != 1 {
		t.Fatalf("two pending comments produced %d rows, want 1 coalesced row", len(rows))
	}
	if rows[0].Count != 2 || rows[0].GroupKey != groupKey || rows[0].ReadAt != nil {
		t.Errorf("coalesced row = count %d group %q read_at %v, want count 2 group %q unread", rows[0].Count, rows[0].GroupKey, rows[0].ReadAt, groupKey)
	}

	if r := tokTestCall(t, h, http.MethodPost, "/api/account/notifications/read-all", "", jwt); r.code != http.StatusOK {
		t.Fatalf("read-all returned %d, want 200; body=%q", r.code, r.body)
	}
	ntfGuestComment(t, h, post.ID, "Third")
	rows = ntfRows(t, h, admin.ID, "comment.pending")
	if len(rows) != 2 {
		t.Fatalf("after read-all a third comment left %d rows, want 2 (read row plus new unread row)", len(rows))
	}
	if rows[1].Count != 1 || rows[1].ReadAt != nil {
		t.Errorf("new row after read-all = count %d read_at %v, want count 1 unread", rows[1].Count, rows[1].ReadAt)
	}
}

// --- per-channel preferences -------------------------------------------------

func TestNotificationsPreferencesPerChannel(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "ntf-pref-admin", "admin")
	author := tokTestUser(t, h, "ntf-pref-author", "author")
	post := ntfPublishedPost(t, h, author, "ntf-pref-post")
	jwt := tokTestLogin(t, h, admin.Username)

	body := ntfPrefs(`{"comment.pending":{"in_app":false,"email":true,"webhook":false}}`, "", `{"enabled":false,"start":"","end":"","tz":""}`)
	if r := tokTestCall(t, h, http.MethodPut, "/api/account/notification-preferences", body, jwt); r.code != http.StatusOK {
		t.Fatalf("PUT notification-preferences returned %d, want 200; body=%q", r.code, r.body)
	}
	get := tokTestCall(t, h, http.MethodGet, "/api/account/notification-preferences", "", jwt)
	var prefs struct {
		Events map[string]struct {
			InApp bool `json:"in_app"`
			Email bool `json:"email"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(get.body), &prefs); err != nil || get.code != http.StatusOK {
		t.Errorf("GET notification-preferences = %d %q, want 200 JSON", get.code, get.body)
	} else if p := prefs.Events["comment.pending"]; p.InApp || !p.Email {
		t.Errorf("saved comment.pending prefs = %+v, want in_app false and email true", p)
	}

	ntfGuestComment(t, h, post.ID, "Pref")

	_, list := ntfList(t, h, jwt, "")
	for _, item := range list.Items {
		if item.Kind == "comment.pending" {
			t.Errorf("in_app false still shows a comment.pending notification in the center")
		}
	}
	if list.UnreadCount != 0 {
		t.Errorf("unread_count = %d with in_app false, want 0", list.UnreadCount)
	}
	queued := ntfCount(t, h, `SELECT count(*) FROM notification_deliveries d
		JOIN notifications n ON n.id = d.notification_id
		WHERE n.user_id = ? AND d.channel = 'email' AND d.status IN ('pending', 'held')`, admin.ID)
	if queued != 1 {
		t.Errorf("email delivery rows for admin = %d, want 1 pending or held", queued)
	}
}

// --- quiet hours -------------------------------------------------------------

func TestNotificationsQuietHoursHoldDeliveries(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "ntf-quiet-admin", "admin")
	author := tokTestUser(t, h, "ntf-quiet-author", "author")
	post := ntfPublishedPost(t, h, author, "ntf-quiet-post")
	jwt := tokTestLogin(t, h, admin.Username)

	now := time.Now().UTC()
	start := now.Add(-time.Hour).Format("15:04")
	end := now.Add(time.Hour).Format("15:04")
	body := ntfPrefs(`{"comment.pending":{"in_app":true,"email":true,"webhook":false}}`, "", fmt.Sprintf(`{"enabled":true,"start":%q,"end":%q,"tz":"UTC"}`, start, end))
	if r := tokTestCall(t, h, http.MethodPut, "/api/account/notification-preferences", body, jwt); r.code != http.StatusOK {
		t.Fatalf("PUT quiet hours returned %d, want 200; body=%q", r.code, r.body)
	}

	ntfGuestComment(t, h, post.ID, "Quiet")

	_, list := ntfList(t, h, jwt, "category=comments")
	if len(list.Items) != 1 {
		t.Errorf("in-app notification during quiet hours: %d items, want 1", len(list.Items))
	}
	status := ntfString(t, h, `SELECT COALESCE((SELECT d.status FROM notification_deliveries d
		JOIN notifications n ON n.id = d.notification_id
		WHERE n.user_id = ? AND d.channel = 'email' ORDER BY d.id DESC LIMIT 1), '')`, admin.ID)
	if status != "held" {
		t.Errorf("email delivery status during quiet hours = %q, want held", status)
	}
}

// --- S21: webhook URL validation ---------------------------------------------

func TestNotificationsWebhookURLRejectsPrivateAndHTTP(t *testing.T) {
	h := newAccessHarness(t)
	admin := tokTestUser(t, h, "ntf-hook-admin", "admin")
	jwt := tokTestLogin(t, h, admin.Username)
	quiet := `{"enabled":false,"start":"","end":"","tz":""}`

	for _, u := range []string{"http://hooks.example.com/x", "https://127.0.0.1/x", "https://10.0.0.5/x", "https://localhost/x"} {
		r := tokTestCall(t, h, http.MethodPut, "/api/account/notification-preferences", ntfPrefs(`{}`, u, quiet), jwt)
		if r.code != http.StatusBadRequest {
			t.Errorf("webhook_url %q returned %d, want 400; body=%q", u, r.code, r.body)
		}
	}
	if stored := ntfString(t, h, "SELECT COALESCE((SELECT webhook_url FROM notification_settings WHERE user_id = ?), '')", admin.ID); stored != "" {
		t.Errorf("rejected webhook URL was stored: %q", stored)
	}

	r := tokTestCall(t, h, http.MethodPut, "/api/account/notification-preferences", ntfPrefs(`{}`, "https://hooks.example.com/x", quiet), jwt)
	if r.code != http.StatusOK {
		t.Errorf("webhook_url https://hooks.example.com/x returned %d, want 200; body=%q", r.code, r.body)
	}
}

// --- security kind on a new device ------------------------------------------

func TestNotificationsNewDeviceLoginEmitsSecurityKind(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "ntf-device-user", "author")
	uaLinux := tokTestUserAgent
	uaPhone := "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"

	tokTestLoginAs(t, h, u.Username, uaLinux)
	countSecurity := func() int64 {
		return ntfCount(t, h, "SELECT count(*) FROM notifications WHERE user_id = ? AND kind = 'security.new_device'", u.ID)
	}
	before := countSecurity()

	tokTestLoginAs(t, h, u.Username, uaPhone)
	after := countSecurity()
	if after <= before {
		t.Fatalf("login from a new device added %d security.new_device rows, want at least 1", after-before)
	}
	rows := ntfRows(t, h, u.ID, "security.new_device")
	if len(rows) == 0 || rows[len(rows)-1].Category != "security" {
		t.Errorf("security.new_device row = %+v, want category security", rows)
	}

	tokTestLoginAs(t, h, u.Username, uaLinux)
	if again := countSecurity(); again != after {
		t.Errorf("login from an already-seen device added a security notification (%d -> %d)", after, again)
	}
}

// --- 90-day retention --------------------------------------------------------

func TestNotificationsExpireAfterNinetyDays(t *testing.T) {
	h := newAccessHarness(t)
	u := tokTestUser(t, h, "ntf-expire-user", "author")
	jwt := tokTestLogin(t, h, u.Username)
	now := time.Now()
	old := ntfSeed(t, h, u.ID, "system.update_available", "system", "system.update_available:old", false, now.Add(-91*24*time.Hour))
	fresh := ntfSeed(t, h, u.ID, "system.update_available", "system", "system.update_available:fresh", false, now)

	_, list := ntfList(t, h, jwt, "")
	seenOld, seenFresh := false, false
	for _, item := range list.Items {
		if item.ID == old.ID {
			seenOld = true
		}
		if item.ID == fresh.ID {
			seenFresh = true
		}
	}
	if seenOld {
		t.Errorf("notification created 91 days ago is still listed")
	}
	if !seenFresh {
		t.Errorf("notification created now is not listed")
	}
	if list.UnreadCount != 1 {
		t.Errorf("unread_count = %d, want 1 (the expired row must not count)", list.UnreadCount)
	}
}

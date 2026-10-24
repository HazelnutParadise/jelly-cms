package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

// Administrator user management acceptance (docs/tickets/06-accounts.md, A1 to
// A10). Fixtures and request helpers live in account_profile_integration_test.go.

type usersAdminListBody struct {
	Items  []map[string]any `json:"items"`
	Total  int64            `json:"total"`
	Counts struct {
		All    int64            `json:"all"`
		Role   map[string]int64 `json:"role"`
		Status map[string]int64 `json:"status"`
	} `json:"counts"`
}

type usersAdminInviteBody struct {
	ID        uint      `json:"id"`
	InviteURL string    `json:"invite_url"`
	ExpiresAt time.Time `json:"expires_at"`
	Emailed   bool      `json:"emailed"`
}

func usersAdminGetList(t *testing.T, h *accessHarness, token, query string) (accessResult, usersAdminListBody) {
	t.Helper()
	res := acctJSON(t, h, http.MethodGet, "/api/admin/users?"+query, "", token)
	h.mustNoPanic(res, "GET /api/admin/users?"+query)
	var body usersAdminListBody
	if res.code == http.StatusOK {
		acctDecode(t, res, "users list", &body)
	}
	return res, body
}

func usersAdminUsernames(items []map[string]any) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		name, _ := item["username"].(string)
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func usersAdminInvite(t *testing.T, h *accessHarness, token, email, role string) (usersAdminInviteBody, accessResult) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"email": email, "role": role, "note": "acceptance"})
	if err != nil {
		t.Fatalf("encode invite body: %v", err)
	}
	res := acctJSON(t, h, http.MethodPost, "/api/admin/users/invite", string(body), token)
	h.mustNoPanic(res, "POST /api/admin/users/invite")
	var out usersAdminInviteBody
	if res.code == http.StatusCreated {
		acctDecode(t, res, "invite response", &out)
	}
	return out, res
}

func usersAdminAccept(t *testing.T, h *accessHarness, token, password, displayName string) accessResult {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token, "password": password, "display_name": displayName})
	if err != nil {
		t.Fatalf("encode accept body: %v", err)
	}
	res := acctJSON(t, h, http.MethodPost, "/api/invites/accept", string(body), "")
	h.mustNoPanic(res, "POST /api/invites/accept")
	return res
}

func usersAdminPatch(t *testing.T, h *accessHarness, token string, id uint, body string) accessResult {
	t.Helper()
	res := acctJSON(t, h, http.MethodPatch, fmt.Sprintf("/api/admin/users/%d", id), body, token)
	h.mustNoPanic(res, "PATCH /api/admin/users/:id")
	return res
}

func usersAdminBulk(t *testing.T, h *accessHarness, token, body string) (accessResult, map[string]any) {
	t.Helper()
	res := acctJSON(t, h, http.MethodPost, "/api/admin/users/bulk", body, token)
	h.mustNoPanic(res, "POST /api/admin/users/bulk")
	var out map[string]any
	if res.code == http.StatusOK {
		acctDecode(t, res, "bulk response", &out)
	}
	return res, out
}

func usersAdminStatusOf(t *testing.T, h *accessHarness, id uint) string {
	t.Helper()
	return acctString(t, h, "SELECT status FROM users WHERE id = ?", id)
}

func usersAdminRoleOf(t *testing.T, h *accessHarness, id uint) string {
	t.Helper()
	return acctString(t, h, "SELECT role FROM users WHERE id = ?", id)
}

func usersAdminExists(t *testing.T, h *accessHarness, id uint) bool {
	t.Helper()
	return acctCount(t, h, "SELECT COUNT(*) FROM users WHERE id = ?", id) == 1
}

func TestUsersAdminListRoleGate(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-gate-admin", Role: "admin"})
	editor := acctCreateUser(t, h, acctUserSpec{Username: "ua-gate-editor", Role: "editor"})
	author := acctCreateUser(t, h, acctUserSpec{Username: "ua-gate-author", Role: "author"})

	anon := acctJSON(t, h, http.MethodGet, "/api/admin/users", "", "")
	h.mustNoPanic(anon, "anonymous users list")
	acctWant(t, anon, "anonymous GET /api/admin/users", http.StatusUnauthorized)
	if !strings.Contains(strings.ToLower(anon.header.Get(echo.HeaderContentType)), "application/json") {
		t.Errorf("anonymous users list content type = %q, want JSON", anon.header.Get(echo.HeaderContentType))
	}
	for _, actor := range []struct {
		name string
		user core.User
		want int
	}{
		{"author", author, http.StatusForbidden},
		{"editor", editor, http.StatusForbidden},
		{"admin", admin, http.StatusOK},
	} {
		res := acctJSON(t, h, http.MethodGet, "/api/admin/users", "", h.token(actor.user))
		h.mustNoPanic(res, actor.name+" users list")
		acctWant(t, res, actor.name+" GET /api/admin/users", actor.want)
	}
}

func TestUsersAdminSearchFilterCountsAndPaging(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-search-admin", Role: "admin", DisplayName: "Root Admin"})
	acctCreateUser(t, h, acctUserSpec{Username: "ua-search-editor", Role: "editor", DisplayName: "Edith Editor"})
	acctCreateUser(t, h, acctUserSpec{Username: "ua-search-writer", Role: "author", DisplayName: "Needle Writer"})
	acctCreateUser(t, h, acctUserSpec{Username: "ua-search-disabled", Role: "author", Status: "disabled"})
	acctCreateUser(t, h, acctUserSpec{Username: "ua-search-customer", Role: "customer"})
	acctCreateUser(t, h, acctUserSpec{Username: "ua-search-invited", Role: "author", Status: "invited"})
	token := h.token(admin)

	cases := []struct {
		query string
		want  []string
	}{
		{"q=needle", []string{"ua-search-writer"}},
		{"q=NEEDLE", []string{"ua-search-writer"}},
		{"q=edith", []string{"ua-search-editor"}},
		{"q=ua-search-customer", []string{"ua-search-customer"}},
		{"q=ua-search-editor%40example.test", []string{"ua-search-editor"}},
		{"role=author", []string{"ua-search-disabled", "ua-search-invited", "ua-search-writer"}},
		{"role=editor", []string{"ua-search-editor"}},
		{"status=disabled", []string{"ua-search-disabled"}},
		{"status=invited", []string{"ua-search-invited"}},
		{"role=author&status=active", []string{"ua-search-writer"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			res, body := usersAdminGetList(t, h, token, tc.query)
			if res.code != http.StatusOK {
				t.Fatalf("GET /api/admin/users?%s returned %d; body=%q", tc.query, res.code, res.body)
			}
			got := usersAdminUsernames(body.Items)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("GET /api/admin/users?%s usernames = %v, want %v", tc.query, got, want)
			}
			if body.Total != int64(len(want)) {
				t.Errorf("GET /api/admin/users?%s total = %d, want %d", tc.query, body.Total, len(want))
			}
		})
	}

	_, all := usersAdminGetList(t, h, token, "")
	if all.Counts.All != 6 || all.Total != 6 {
		t.Errorf("unfiltered counts.all = %d total = %d, want 6 and 6", all.Counts.All, all.Total)
	}
	for role, want := range map[string]int64{"admin": 1, "editor": 1, "author": 3, "customer": 1} {
		if got := all.Counts.Role[role]; got != want {
			t.Errorf("counts.role[%s] = %d, want %d", role, got, want)
		}
	}
	for status, want := range map[string]int64{"active": 4, "disabled": 1, "invited": 1} {
		if got := all.Counts.Status[status]; got != want {
			t.Errorf("counts.status[%s] = %d, want %d", status, got, want)
		}
	}

	bulk := make([]core.User, 0, 105)
	for i := 0; i < 105; i++ {
		bulk = append(bulk, core.User{
			Username: fmt.Sprintf("ua-bulk-%03d", i), Email: fmt.Sprintf("ua-bulk-%03d@example.test", i),
			Role: "author", Status: "active", Provider: "local",
		})
	}
	if err := h.db.Create(&bulk).Error; err != nil {
		t.Fatalf("seed paging users: %v", err)
	}

	res, capped := usersAdminGetList(t, h, token, "per_page=500")
	if res.code == http.StatusOK && (len(capped.Items) != 100 || capped.Total != 111) {
		t.Errorf("per_page=500 returned %d items and total %d, want the cap of 100 items and total 111", len(capped.Items), capped.Total)
	}
	_, second := usersAdminGetList(t, h, token, "per_page=100&page=2")
	if len(second.Items) != 11 {
		t.Errorf("per_page=100&page=2 returned %d items, want 11", len(second.Items))
	}

	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		_, got := usersAdminGetList(t, h, token, fmt.Sprintf("q=ua-bulk&per_page=50&page=%d", page))
		if got.Total != 105 {
			t.Errorf("q=ua-bulk total = %d, want 105", got.Total)
		}
		for _, name := range usersAdminUsernames(got.Items) {
			if seen[name] {
				t.Errorf("username %q appeared on more than one page", name)
			}
			seen[name] = true
		}
	}
	if len(seen) != 105 {
		t.Errorf("paging through q=ua-bulk visited %d distinct users, want 105", len(seen))
	}
}

func TestUsersInviteCreatesInvitedUserAndSevenDayLink(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-invite-admin", Role: "admin"})
	token := h.token(admin)

	before := time.Now()
	invite, res := usersAdminInvite(t, h, token, "invitee.one@example.test", "editor")
	h.mustNoPanic(res, "invite create")
	if !acctWant(t, res, "POST /api/admin/users/invite", http.StatusCreated) {
		return
	}
	if !strings.Contains(invite.InviteURL, "/invite/") {
		t.Fatalf("invite_url = %q, want a /invite/<token> link", invite.InviteURL)
	}
	if invite.Emailed {
		t.Error("emailed = true with no SMTP configured, want false")
	}
	if drift := invite.ExpiresAt.Sub(before.Add(7 * 24 * time.Hour)); drift < -5*time.Minute || drift > 5*time.Minute {
		t.Errorf("expires_at = %v, want about 7 days from now (drift %v)", invite.ExpiresAt, drift)
	}

	var invited core.User
	if err := h.db.First(&invited, invite.ID).Error; err != nil {
		t.Fatalf("read invited user %d: %v", invite.ID, err)
	}
	if invited.Status != "invited" {
		t.Errorf("status = %q, want invited", invited.Status)
	}
	if invited.Password != "" {
		t.Error("invited user has a password before the invitation is accepted")
	}
	if invited.Role != "editor" || invited.Email != "invitee.one@example.test" {
		t.Errorf("invited user role/email = %q/%q, want editor/invitee.one@example.test", invited.Role, invited.Email)
	}
	if !strings.Contains(strings.ToLower(invited.Username), "invitee") {
		t.Errorf("username = %q, want one derived from the email local part", invited.Username)
	}

	token1 := acctTokenFromURL(t, invite.InviteURL, "/invite/")
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND kind = 'invite' AND token_hash = ?", invited.ID, acctSHA256Hex(token1)); n != 1 {
		t.Errorf("user_invites row with SHA-256 of the link token = %d, want 1", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE token_hash = ?", token1); n != 0 {
		t.Error("raw invite token is stored in user_invites; only its hash may be stored")
	}

	second, res2 := usersAdminInvite(t, h, token, "invitee.one@other.test", "author")
	h.mustNoPanic(res2, "second invite with the same local part")
	if acctWant(t, res2, "second invite with the same local part", http.StatusCreated) {
		var other core.User
		if err := h.db.First(&other, second.ID).Error; err != nil {
			t.Fatalf("read second invited user: %v", err)
		}
		if other.Username == invited.Username || other.Username == "" {
			t.Errorf("second username %q collides with %q", other.Username, invited.Username)
		}
	}
}

func TestUsersInviteDuplicateEmailAndUnknownRoleRejected(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-dup-admin", Role: "admin"})
	token := h.token(admin)
	existing, res := usersAdminInvite(t, h, token, "dup-first@example.test", "author")
	if !acctWant(t, res, "first invite", http.StatusCreated) {
		return
	}
	before := acctCount(t, h, "SELECT COUNT(*) FROM users")

	_, dupActive := usersAdminInvite(t, h, token, admin.Email, "author")
	acctWant(t, dupActive, "invite with an active user's email", http.StatusConflict)

	_, dupInvited := usersAdminInvite(t, h, token, "dup-first@example.test", "author")
	acctWant(t, dupInvited, "invite with an already invited email", http.StatusConflict)

	_, unknown := usersAdminInvite(t, h, token, "dup-unknown-role@example.test", "superuser")
	acctWant(t, unknown, "invite with an unknown role", http.StatusBadRequest)

	if n := acctCount(t, h, "SELECT COUNT(*) FROM users WHERE email = ?", "dup-unknown-role@example.test"); n != 0 {
		t.Error("rejected invite with an unknown role still created a user")
	}
	if after := acctCount(t, h, "SELECT COUNT(*) FROM users"); after != before {
		t.Errorf("rejected invites changed the user count from %d to %d", before, after)
	}
	if existing.ID == 0 {
		t.Error("first invite did not return an id")
	}
}

func TestUsersInviteResendRotatesAndRevokeDeletes(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-resend-admin", Role: "admin"})
	token := h.token(admin)
	created, res := usersAdminInvite(t, h, token, "resend-me@example.test", "author")
	if !acctWant(t, res, "invite create", http.StatusCreated) {
		return
	}
	oldToken := acctTokenFromURL(t, created.InviteURL, "/invite/")
	oldHash := acctSHA256Hex(oldToken)

	resend := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/invite/resend", created.ID), "", token)
	h.mustNoPanic(resend, "invite resend")
	if !acctWantOK(t, resend, "POST /api/admin/users/:id/invite/resend") {
		return
	}
	var rotated usersAdminInviteBody
	acctDecode(t, resend, "resend response", &rotated)
	newToken := acctTokenFromURL(t, rotated.InviteURL, "/invite/")
	if newToken == oldToken {
		t.Error("resend returned the same invitation token")
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL", created.ID); n != 1 {
		t.Errorf("live invite rows after resend = %d, want 1", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND token_hash = ?", created.ID, acctSHA256Hex(newToken)); n != 1 {
		t.Error("resend did not store the hash of the new token")
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND token_hash = ?", created.ID, oldHash); n != 0 {
		t.Error("resend left the old token hash in place")
	}

	stale := usersAdminAccept(t, h, oldToken, "stale-password-1", "")
	if stale.code != http.StatusNotFound && stale.code != http.StatusGone {
		t.Errorf("accept with the replaced token returned %d, want 404 or 410", stale.code)
	}

	revoke := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/invite/revoke", created.ID), "", token)
	h.mustNoPanic(revoke, "invite revoke")
	if acctWantOK(t, revoke, "POST /api/admin/users/:id/invite/revoke") {
		if usersAdminExists(t, h, created.ID) {
			t.Error("revoking an invitation left the invited user row in place")
		}
	}
	dead := usersAdminAccept(t, h, newToken, "revoked-password-1", "")
	if dead.code != http.StatusNotFound && dead.code != http.StatusGone {
		t.Errorf("accept after revoke returned %d, want 404 or 410", dead.code)
	}
}

func TestUsersExpiredInvitationCanBeResentWithoutLiveKindConflict(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-expired-resend-admin", Role: "admin"})
	token := h.token(admin)
	created, res := usersAdminInvite(t, h, token, "expired-resend@example.test", "author")
	if !acctWant(t, res, "invite create", http.StatusCreated) {
		return
	}
	oldToken := acctTokenFromURL(t, created.InviteURL, "/invite/")
	if err := h.db.Exec("UPDATE user_invites SET expires_at = ? WHERE user_id = ? AND kind = 'invite'", time.Now().Add(-time.Hour), created.ID).Error; err != nil {
		t.Fatalf("expire invitation: %v", err)
	}

	resent := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/invite/resend", created.ID), "", token)
	h.mustNoPanic(resent, "resend expired invitation")
	if !acctWantOK(t, resent, "resend expired invitation") {
		return
	}
	var rotated usersAdminInviteBody
	acctDecode(t, resent, "resent expired invitation", &rotated)
	newToken := acctTokenFromURL(t, rotated.InviteURL, "/invite/")
	if newToken == oldToken {
		t.Error("resending an expired invitation returned the expired token")
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL", created.ID); n != 1 {
		t.Errorf("live invite rows after resending expired link = %d, want 1", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND token_hash = ? AND expires_at > now()", created.ID, acctSHA256Hex(newToken)); n != 1 {
		t.Errorf("new non-expired invite row for replacement token = %d, want 1", n)
	}
	stale := usersAdminAccept(t, h, oldToken, "stale-expired-pass-1", "")
	if stale.code != http.StatusGone && stale.code != http.StatusNotFound {
		t.Errorf("accept with the expired replaced token returned %d, want 410 or 404", stale.code)
	}
}

func TestUsersInviteAcceptSetsPasswordOnceAndRejectsReuseOrExpiredToken(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-accept-admin", Role: "admin"})
	token := h.token(admin)

	invite, res := usersAdminInvite(t, h, token, "accept.me@example.test", "author")
	if !acctWant(t, res, "invite create", http.StatusCreated) {
		return
	}
	inviteToken := acctTokenFromURL(t, invite.InviteURL, "/invite/")

	page := acctCall(t, h, acctRemote(), http.MethodGet, "/invite/"+inviteToken, "", "", map[string]string{echo.HeaderAccept: "text/html"})
	h.mustNoPanic(page, "GET /invite/:token")
	acctWant(t, page, "GET /invite/:token before acceptance", http.StatusOK)

	accepted := usersAdminAccept(t, h, inviteToken, "accepted-password-1", "Accepted Person")
	if !acctWant(t, accepted, "POST /api/invites/accept", http.StatusOK) {
		return
	}
	var user core.User
	if err := h.db.First(&user, invite.ID).Error; err != nil {
		t.Fatalf("read accepted user: %v", err)
	}
	if user.Status != "active" {
		t.Errorf("status after accept = %q, want active", user.Status)
	}
	if user.EmailVerifiedAt == nil {
		t.Error("email_verified_at is not set after accepting the invitation")
	}
	if user.DisplayName != "Accepted Person" {
		t.Errorf("display_name after accept = %q, want Accepted Person", user.DisplayName)
	}
	if login, _ := acctLogin(t, h, user.Username, "accepted-password-1", "", acctRemote()); login == "" {
		t.Error("accepted user cannot log in with the password set on accept")
	}

	reuse := usersAdminAccept(t, h, inviteToken, "reused-password-2", "")
	acctWant(t, reuse, "second use of the invitation token", http.StatusGone)
	reusePage := acctCall(t, h, acctRemote(), http.MethodGet, "/invite/"+inviteToken, "", "", map[string]string{echo.HeaderAccept: "text/html"})
	acctWant(t, reusePage, "GET /invite/:token after use", http.StatusGone)
	if r := acctJSON(t, h, http.MethodPost, "/auth/login", fmt.Sprintf(`{"username":%q,"password":"reused-password-2"}`, user.Username), ""); r.code != http.StatusUnauthorized {
		t.Errorf("password from the reused accept request logged in (status %d), want 401", r.code)
	}

	expiring, res2 := usersAdminInvite(t, h, token, "expired.me@example.test", "author")
	if !acctWant(t, res2, "second invite create", http.StatusCreated) {
		return
	}
	expiredToken := acctTokenFromURL(t, expiring.InviteURL, "/invite/")
	if err := h.db.Exec("UPDATE user_invites SET expires_at = ? WHERE user_id = ? AND kind = 'invite'", time.Now().Add(-time.Hour), expiring.ID).Error; err != nil {
		t.Fatalf("expire invitation: %v", err)
	}
	expired := usersAdminAccept(t, h, expiredToken, "expired-password-3", "")
	acctWant(t, expired, "accept with an expired invitation", http.StatusGone)
	if status := usersAdminStatusOf(t, h, expiring.ID); status != "invited" {
		t.Errorf("expired accept changed status to %q, want invited", status)
	}
}

func TestUsersPatchRoleUsesWhitelistAndIgnoresInjectedFields(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "ua-patch-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "ua-patch-target", Role: "author", Password: acctPassword})
	token := h.token(admin)
	var before core.User
	if err := h.db.First(&before, target.ID).Error; err != nil {
		t.Fatalf("read target: %v", err)
	}

	res := usersAdminPatch(t, h, token, target.ID, `{"role":"editor","status":"active","display_name":"Patched Name","id":987654,"password":"injected-password","created_at":"2001-01-01T00:00:00Z","email":"hijack@example.test"}`)
	if !acctWant(t, res, "PATCH with injected fields", http.StatusOK) {
		return
	}
	var after core.User
	if err := h.db.First(&after, target.ID).Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if after.Role != "editor" {
		t.Errorf("role = %q, want editor", after.Role)
	}
	if after.DisplayName != "Patched Name" {
		t.Errorf("display_name = %q, want Patched Name", after.DisplayName)
	}
	if after.ID != before.ID {
		t.Errorf("id changed to %d", after.ID)
	}
	if after.Password != before.Password {
		t.Error("password changed through PATCH /api/admin/users/:id")
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("created_at changed to %v", after.CreatedAt)
	}
	if after.Email != before.Email {
		t.Errorf("email changed to %q through PATCH", after.Email)
	}
	if usersAdminExists(t, h, 987654) {
		t.Error("injected id created a user row")
	}

	bad := usersAdminPatch(t, h, token, target.ID, `{"role":"root"}`)
	acctWant(t, bad, "PATCH with an unknown role", http.StatusBadRequest)
	if got := usersAdminRoleOf(t, h, target.ID); got != "editor" {
		t.Errorf("unknown role changed role to %q", got)
	}
	missing := usersAdminPatch(t, h, token, 987654321, `{"role":"editor"}`)
	acctWant(t, missing, "PATCH a missing user", http.StatusNotFound)
}

func TestUsersCannotDemoteDisableOrDeleteLastAdmin(t *testing.T) {
	h := newAccessHarness(t)
	actor := acctCreateUser(t, h, acctUserSpec{Username: "last-admin-actor", Role: "admin"})
	second := acctCreateUser(t, h, acctUserSpec{Username: "last-admin-second", Role: "admin"})
	third := acctCreateUser(t, h, acctUserSpec{Username: "last-admin-third", Role: "admin"})
	token := h.token(actor)

	// While other admins remain, the actor can demote, disable and delete them.
	acctWantOK(t, usersAdminPatch(t, h, token, second.ID, `{"role":"editor"}`), "demote the second admin with two other admins active")
	acctWantOK(t, usersAdminPatch(t, h, token, third.ID, `{"status":"disabled"}`), "disable the third admin with two other admins active")
	del := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", third.ID), "", token)
	h.mustNoPanic(del, "delete the third admin")
	acctWantOK(t, del, "DELETE the third admin with the actor still an active admin")

	// Only the actor is an active admin now. An active admin acting on the last
	// active admin is necessarily acting on itself, so the guard shows up as the
	// self-protection 409 and as a bulk skip with a reason.
	acctWant(t, usersAdminPatch(t, h, token, actor.ID, `{"role":"editor"}`), "demote the last active admin (self)", http.StatusConflict)
	acctWant(t, usersAdminPatch(t, h, token, actor.ID, `{"status":"disabled"}`), "disable the last active admin (self)", http.StatusConflict)
	self := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", actor.ID), "", token)
	h.mustNoPanic(self, "delete the last active admin (self)")
	acctWant(t, self, "DELETE the last active admin (self)", http.StatusConflict)

	res, body := usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%d],"action":"disable"}`, actor.ID))
	if acctWant(t, res, "bulk disable of the last active admin", http.StatusOK) {
		skipped, _ := body["skipped"].([]any)
		if len(skipped) != 1 {
			t.Errorf("bulk skipped = %v, want the last admin with a reason", skipped)
		}
	}
	if got := usersAdminStatusOf(t, h, actor.ID); got != "active" {
		t.Errorf("actor status after refused changes = %q, want active", got)
	}
	if got := usersAdminRoleOf(t, h, actor.ID); got != "admin" {
		t.Errorf("actor role after refused changes = %q, want admin", got)
	}
	if got := usersAdminRoleOf(t, h, second.ID); got != "editor" {
		t.Errorf("second admin role = %q, want editor after the earlier demotion", got)
	}
}

func TestUsersCannotChangeOwnRoleOrDeleteSelf(t *testing.T) {
	h := newAccessHarness(t)
	actor := acctCreateUser(t, h, acctUserSpec{Username: "self-admin", Role: "admin"})
	acctCreateUser(t, h, acctUserSpec{Username: "self-admin-other", Role: "admin"})
	token := h.token(actor)

	acctWant(t, usersAdminPatch(t, h, token, actor.ID, `{"role":"author"}`), "PATCH own role", http.StatusConflict)
	acctWant(t, usersAdminPatch(t, h, token, actor.ID, `{"status":"disabled"}`), "PATCH own status", http.StatusConflict)
	del := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", actor.ID), "", token)
	h.mustNoPanic(del, "DELETE self")
	acctWant(t, del, "DELETE own account", http.StatusConflict)

	if got := usersAdminRoleOf(t, h, actor.ID); got != "admin" {
		t.Errorf("own role after refused self-change = %q, want admin", got)
	}
	if got := usersAdminStatusOf(t, h, actor.ID); got != "active" {
		t.Errorf("own status after refused self-change = %q, want active", got)
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/admin/users", "", token); r.code != http.StatusOK {
		t.Errorf("admin lost access after refused self-changes: %d", r.code)
	}
}

func TestUsersDisabledUserOldJWTIsRejected(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "disable-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "disable-target", Role: "admin"})
	adminToken := h.token(admin)
	targetToken := h.token(target)

	if r := acctJSON(t, h, http.MethodGet, "/api/admin/posts", "", targetToken); r.code != http.StatusOK {
		t.Fatalf("target admin JWT before disable returned %d, want 200", r.code)
	}
	acctWantOK(t, usersAdminPatch(t, h, adminToken, target.ID, `{"status":"disabled"}`), "PATCH status disabled")

	if r := acctJSON(t, h, http.MethodGet, "/api/admin/posts", "", targetToken); r.code != http.StatusUnauthorized {
		t.Errorf("old JWT of a disabled admin returned %d on /api/admin/posts, want 401", r.code)
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", targetToken); r.code != http.StatusUnauthorized {
		t.Errorf("old JWT of a disabled admin returned %d on /api/auth/me, want 401", r.code)
	}
}

func TestUsersDisabledLocalLoginRefused(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "login-block-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "login-block-target", Role: "author", Password: acctPassword})
	adminToken := h.token(admin)
	acctWantOK(t, usersAdminPatch(t, h, adminToken, target.ID, `{"status":"disabled"}`), "PATCH status disabled")

	body := fmt.Sprintf(`{"username":%q,"password":%q}`, target.Username, acctPassword)
	res := acctCall(t, h, acctRemote(), http.MethodPost, "/auth/login", body, "", map[string]string{"Content-Type": "application/json"})
	h.mustNoPanic(res, "disabled login")
	acctWant(t, res, "POST /auth/login for a disabled account with the correct password", http.StatusForbidden)
	if hasCookie(res.header, "jwt_token") {
		t.Error("disabled account login set jwt_token")
	}
	if strings.Contains(res.body, `"token"`) {
		t.Error("disabled account login returned a token")
	}

	acctWantOK(t, usersAdminPatch(t, h, adminToken, target.ID, `{"status":"active"}`), "PATCH status active")
	if token, _ := acctLogin(t, h, target.Username, acctPassword, "", acctRemote()); token == "" {
		t.Error("re-enabled account cannot log in")
	}
}

func TestUsersDeleteRequiresReassignmentAndMovesPostsComments(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "delete-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "delete-target", Role: "author"})
	heir := acctCreateUser(t, h, acctUserSpec{Username: "delete-heir", Role: "editor"})
	disabled := acctCreateUser(t, h, acctUserSpec{Username: "delete-disabled", Role: "editor", Status: "disabled"})
	token := h.token(admin)

	targetID := target.ID
	var postIDs []uint
	for i, typ := range []string{"post", "post", "page"} {
		p := core.Post{
			Title: fmt.Sprintf("Delete fixture %d", i), Slug: fmt.Sprintf("delete-fixture-%d", i), Content: "body",
			Type: typ, Status: "published", Visibility: "public", AuthorID: target.ID,
		}
		if err := h.db.Create(&p).Error; err != nil {
			t.Fatalf("seed %s: %v", typ, err)
		}
		postIDs = append(postIDs, p.ID)
	}
	comment := core.Comment{
		PostID: postIDs[0], AuthorID: &targetID, Name: "Delete Target", Email: target.Email,
		Body: "comment", BodyHTML: "<p>comment</p>", Status: "approved",
	}
	if err := h.db.Create(&comment).Error; err != nil {
		t.Fatalf("seed comment: %v", err)
	}

	blocked := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", target.ID), "", token)
	h.mustNoPanic(blocked, "delete without reassignment")
	if acctWant(t, blocked, "DELETE with content and no reassign_to", http.StatusConflict) {
		var counts map[string]any
		acctDecode(t, blocked, "delete conflict", &counts)
		if counts["posts"] != float64(2) || counts["pages"] != float64(1) || counts["comments"] != float64(1) {
			t.Errorf("delete conflict counts = posts %v pages %v comments %v, want 2, 1, 1", counts["posts"], counts["pages"], counts["comments"])
		}
	}
	if !usersAdminExists(t, h, target.ID) {
		t.Fatal("refused delete removed the user")
	}

	toDisabled := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d?reassign_to=%d", target.ID, disabled.ID), "", token)
	h.mustNoPanic(toDisabled, "delete with a disabled reassign target")
	if toDisabled.code < 400 || toDisabled.code >= 500 {
		t.Errorf("reassign to a disabled account returned %d, want a 4xx refusal; body=%q", toDisabled.code, toDisabled.body)
	}
	if !usersAdminExists(t, h, target.ID) {
		t.Fatal("refused reassignment removed the user")
	}

	moved := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d?reassign_to=%d", target.ID, heir.ID), "", token)
	h.mustNoPanic(moved, "delete with reassignment")
	acctWantOK(t, moved, "DELETE with reassign_to an active editor")

	if usersAdminExists(t, h, target.ID) {
		t.Error("deleted user row still exists")
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM posts WHERE author_id = ?", target.ID); n != 0 {
		t.Errorf("posts still owned by the deleted user: %d", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM posts WHERE author_id = ?", heir.ID); n != 3 {
		t.Errorf("posts owned by the heir after reassignment = %d, want 3 (2 posts and 1 page)", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM comments WHERE author_id = ?", heir.ID); n != 1 {
		t.Errorf("comments owned by the heir after reassignment = %d, want 1", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM comments WHERE author_id = ?", target.ID); n != 0 {
		t.Errorf("comments still owned by the deleted user: %d", n)
	}
}

func TestUsersDeleteReassignPreservesHTTPPostRevisions(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "revision-delete-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "revision-delete-target", Role: "author"})
	heir := acctCreateUser(t, h, acctUserSpec{Username: "revision-delete-heir", Role: "editor"})
	otherAuthor := acctCreateUser(t, h, acctUserSpec{Username: "revision-delete-other-author", Role: "author"})
	adminToken := h.token(admin)
	targetToken := h.token(target)
	otherToken := h.token(otherAuthor)

	postID := revTestCreate(t, h, targetToken, map[string]any{
		"title": "Revision owner before deletion", "content": "original revision body", "status": "draft",
	})
	revTestPut(t, h, targetToken, postID, map[string]any{
		"title": "Revision owner after edit", "content": "updated revision body", "status": "draft",
	})
	var before []core.Revision
	if err := h.db.Where("post_id = ?", postID).Order("id ASC").Find(&before).Error; err != nil {
		t.Fatalf("read generated HTTP revisions: %v", err)
	}
	if len(before) < 2 {
		t.Fatalf("HTTP create and edit produced %d revisions, want at least 2", len(before))
	}
	otherPostID := revTestCreate(t, h, otherToken, map[string]any{
		"title": "Unrelated version owner", "content": "unrelated original", "status": "draft",
	})
	revTestPut(t, h, otherToken, otherPostID, map[string]any{
		"title": "Unrelated version owner edited", "content": "unrelated updated", "status": "draft",
	})
	var otherBefore []core.Revision
	if err := h.db.Where("post_id = ?", otherPostID).Order("id ASC").Find(&otherBefore).Error; err != nil {
		t.Fatalf("read unrelated revisions before owner deletion: %v", err)
	}
	var revisionFK string
	if err := h.db.Raw(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'fk_revisions_user' AND conrelid = 'revisions'::regclass`).Scan(&revisionFK).Error; err != nil {
		t.Fatalf("inspect revision actor foreign key: %v", err)
	}
	if revisionFK == "" {
		t.Fatal("expected the database to retain fk_revisions_user on revision actors")
	}
	t.Logf("revision actor constraint: %s", revisionFK)

	withoutReassign := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", target.ID), "", adminToken)
	h.mustNoPanic(withoutReassign, "delete revision owner without reassignment")
	if !acctWant(t, withoutReassign, "DELETE revision owner without reassign_to", http.StatusConflict) {
		t.Fatalf("delete without reassignment should stop before touching revisions: %q", withoutReassign.body)
	}

	moved := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d?reassign_to=%d", target.ID, heir.ID), "", adminToken)
	h.mustNoPanic(moved, "delete revision owner with reassignment")
	if !acctWantOK(t, moved, "DELETE revision owner with reassign_to") {
		t.Fatalf("delete with reassignment failed; revision actor FK is %s", revisionFK)
	}
	if usersAdminExists(t, h, target.ID) {
		t.Fatal("deleted revision owner row still exists")
	}
	var post core.Post
	if err := h.db.First(&post, postID).Error; err != nil || post.AuthorID != heir.ID {
		t.Fatalf("reassigned post author=%d err=%v, want heir %d", post.AuthorID, err, heir.ID)
	}
	var after []core.Revision
	if err := h.db.Where("post_id = ?", postID).Order("id ASC").Find(&after).Error; err != nil {
		t.Fatalf("read revisions after author deletion: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("revision count after author deletion=%d, want %d", len(after), len(before))
	}
	if nullActors := acctCount(t, h, "SELECT COUNT(*) FROM revisions WHERE post_id = ? AND user_id IS NULL", postID); nullActors != int64(len(before)) {
		t.Errorf("revision rows with SQL NULL user_id=%d, want %d", nullActors, len(before))
	}
	for i := range before {
		if after[i].UserID != 0 {
			t.Errorf("revision %d actor user_id=%d after deletion, want SQL NULL", after[i].ID, after[i].UserID)
		}
		if before[i].ID != after[i].ID || before[i].PostID != after[i].PostID || before[i].Kind != after[i].Kind ||
			before[i].Title != after[i].Title || before[i].Content != after[i].Content || before[i].Excerpt != after[i].Excerpt ||
			string(before[i].Meta) != string(after[i].Meta) || before[i].Added != after[i].Added || before[i].Removed != after[i].Removed ||
			!before[i].CreatedAt.Equal(after[i].CreatedAt) {
			t.Errorf("revision %d changed across author deletion: before=%+v after=%+v", i, before[i], after[i])
		}
	}
	listed := revTestList(t, h, h.token(heir), postID)
	if len(listed) != len(before) {
		t.Fatalf("revision API returned %d rows after reassignment, want %d", len(listed), len(before))
	}
	for _, original := range before {
		detail, code := revTestGet(h, h.token(heir), postID, original.ID)
		if code != http.StatusOK || detail.Title != original.Title || detail.Content != original.Content || detail.User.ID != 0 {
			t.Errorf("GET revision %d after deleting its actor: status=%d detail=%+v", original.ID, code, detail)
		}
	}
	diff := h.request(http.MethodGet, fmt.Sprintf("/api/admin/posts/%d/revisions/diff?from=%d&to=%d", postID, before[0].ID, before[1].ID), "", h.token(heir), echo.MIMEApplicationJSON, "")
	h.mustNoPanic(diff, "diff revisions after actor deletion")
	if diff.code != http.StatusOK {
		t.Errorf("revision diff after actor deletion returned %d: %q", diff.code, diff.body)
	} else {
		var changes revTestDiff
		if err := json.Unmarshal([]byte(diff.body), &changes); err != nil {
			t.Errorf("decode revision diff after actor deletion: %v; body=%q", err, diff.body)
		} else {
			var removedOriginal, addedUpdated bool
			for _, op := range changes.Content {
				removedOriginal = removedOriginal || op.Op == "-" && op.Text == "original revision body"
				addedUpdated = addedUpdated || op.Op == "+" && op.Text == "updated revision body"
			}
			if !removedOriginal || !addedUpdated {
				t.Errorf("revision diff after actor deletion=%+v, want original body removed and updated body added", changes.Content)
			}
		}
	}
	restore := h.request(http.MethodPost, fmt.Sprintf("/api/admin/posts/%d/revisions/%d/restore", postID, before[0].ID), "", h.token(heir), echo.MIMEApplicationJSON, "")
	h.mustNoPanic(restore, "restore revision after actor deletion")
	if restore.code != http.StatusOK {
		t.Errorf("restore after actor deletion returned %d: %q", restore.code, restore.body)
	}
	var restored struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(restore.body), &restored); err != nil || restored.Title != before[0].Title || restored.Content != before[0].Content {
		t.Errorf("restore after actor deletion returned title=%q content=%q err=%v", restored.Title, restored.Content, err)
	}
	if restoredList := revTestList(t, h, h.token(heir), postID); len(restoredList) != len(before)+1 || restoredList[0].Kind != "restore" {
		t.Errorf("revision history after restore=%+v, want prior history plus newest restore", restoredList)
	}
	var otherAfter []core.Revision
	if err := h.db.Where("post_id = ?", otherPostID).Order("id ASC").Find(&otherAfter).Error; err != nil {
		t.Fatalf("read unrelated revisions after owner deletion: %v", err)
	}
	if len(otherAfter) != len(otherBefore) {
		t.Fatalf("unrelated revision count changed from %d to %d", len(otherBefore), len(otherAfter))
	}
	for i := range otherBefore {
		if otherAfter[i].UserID != otherAuthor.ID || otherAfter[i].ID != otherBefore[i].ID || otherAfter[i].Content != otherBefore[i].Content {
			t.Errorf("unrelated revision changed: before=%+v after=%+v", otherBefore[i], otherAfter[i])
		}
	}
}

func TestUsersDeleteRevisionOnlyContributorPreservesRevision(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "revision-only-admin", Role: "admin"})
	owner := acctCreateUser(t, h, acctUserSpec{Username: "revision-only-owner", Role: "author"})
	contributor := acctCreateUser(t, h, acctUserSpec{Username: "revision-only-contributor", Role: "editor"})
	adminToken := h.token(admin)
	ownerToken := h.token(owner)
	contributorToken := h.token(contributor)
	postID := revTestCreate(t, h, ownerToken, map[string]any{
		"title": "Owned by someone else", "content": "owner original", "status": "draft",
	})
	revTestPut(t, h, contributorToken, postID, map[string]any{
		"title": "Edited by contributor", "content": "contributor revision body", "status": "draft",
	})
	var before []core.Revision
	if err := h.db.Where("post_id = ? AND user_id = ?", postID, contributor.ID).Order("id ASC").Find(&before).Error; err != nil {
		t.Fatalf("read contributor revisions: %v", err)
	}
	if len(before) != 1 || before[0].Content != "contributor revision body" {
		t.Fatalf("contributor revision fixture=%+v, want one saved edit", before)
	}
	if posts := acctCount(t, h, "SELECT COUNT(*) FROM posts WHERE author_id = ?", contributor.ID); posts != 0 {
		t.Fatalf("revision-only contributor unexpectedly owns %d posts", posts)
	}
	if comments := acctCount(t, h, "SELECT COUNT(*) FROM comments WHERE author_id = ?", contributor.ID); comments != 0 {
		t.Fatalf("revision-only contributor unexpectedly owns %d comments", comments)
	}

	deleted := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", contributor.ID), "", adminToken)
	h.mustNoPanic(deleted, "delete revision-only contributor")
	if !acctWantOK(t, deleted, "DELETE revision-only contributor") {
		t.Fatalf("deleting contributor with only revision actor rows returned %d: %q", deleted.code, deleted.body)
	}
	if usersAdminExists(t, h, contributor.ID) {
		t.Fatal("deleted revision-only contributor row still exists")
	}
	var after []core.Revision
	if err := h.db.Where("post_id = ?", postID).Order("id ASC").Find(&after).Error; err != nil {
		t.Fatalf("read revisions after contributor deletion: %v", err)
	}
	if len(after) != 2 || after[1].ID != before[0].ID || after[1].Title != before[0].Title || after[1].Content != before[0].Content {
		t.Errorf("deleting revision-only contributor did not preserve the full revision history: before=%+v after=%+v", before, after)
	}
	if len(after) == 2 && after[1].UserID != 0 {
		t.Errorf("revision-only contributor actor user_id=%d after deletion, want SQL NULL", after[1].UserID)
	}
	listed := revTestList(t, h, h.token(owner), postID)
	if len(listed) != 2 {
		t.Errorf("owner sees %d revisions after contributor deletion, want 2", len(listed))
	}
}

func TestUsersDeleteCustomerWithOrdersReturnsConflictAndBulkSkips(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "order-delete-admin", Role: "admin"})
	customer := acctCreateUser(t, h, acctUserSpec{Username: "order-delete-customer", Role: "customer"})
	removable := acctCreateUser(t, h, acctUserSpec{Username: "order-delete-removable", Role: "author"})
	adminToken := h.token(admin)
	product := core.Product{Title: "Order retention fixture", Slug: "order-retention-fixture", Price: 2599, SKU: "order-retention-fixture", Stock: 2, Status: "active"}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("create order product: %v", err)
	}
	order := core.Order{UserID: customer.ID, TotalAmount: 2599, Status: "paid", PaymentMethod: "fixture", ShippingAddress: "1 Test Street"}
	if err := h.db.Create(&order).Error; err != nil {
		t.Fatalf("create retained customer order: %v", err)
	}
	item := core.OrderItem{OrderID: order.ID, ProductID: product.ID, Quantity: 1, Price: 2599}
	if err := h.db.Create(&item).Error; err != nil {
		t.Fatalf("create retained order item: %v", err)
	}

	deleted := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", customer.ID), "", adminToken)
	h.mustNoPanic(deleted, "delete customer with retained order")
	if deleted.code != http.StatusConflict {
		t.Fatalf("deleting a customer with an order returned %d, want 409: %q", deleted.code, deleted.body)
	}
	if !strings.Contains(strings.ToLower(deleted.body), "disable") && !strings.Contains(deleted.body, "停用") {
		t.Errorf("order conflict did not advise disabling the account: %q", deleted.body)
	}
	if !usersAdminExists(t, h, customer.ID) {
		t.Fatal("order owner was deleted despite retained order")
	}

	res, body := usersAdminBulk(t, h, adminToken, fmt.Sprintf(`{"ids":[%d,%d],"action":"delete"}`, customer.ID, removable.ID))
	if !acctWant(t, res, "bulk delete with a customer order", http.StatusOK) {
		return
	}
	if body["updated"] != float64(1) {
		t.Errorf("bulk delete updated=%v, want 1 successful deletion", body["updated"])
	}
	skipped, _ := body["skipped"].([]any)
	orderOwnerSkipped := false
	for _, entry := range skipped {
		item, _ := entry.(map[string]any)
		if item["id"] == float64(customer.ID) {
			reason, _ := item["reason"].(string)
			orderOwnerSkipped = strings.Contains(strings.ToLower(reason), "order") && (strings.Contains(strings.ToLower(reason), "disable") || strings.Contains(reason, "停用"))
		}
	}
	if !orderOwnerSkipped {
		t.Errorf("bulk delete did not skip order owner with a disable hint: %v", skipped)
	}
	if !usersAdminExists(t, h, customer.ID) || usersAdminExists(t, h, removable.ID) {
		t.Errorf("bulk delete customer exists=%t, no-order user exists=%t; want true, false", usersAdminExists(t, h, customer.ID), usersAdminExists(t, h, removable.ID))
	}
	var afterOrder core.Order
	if err := h.db.First(&afterOrder, order.ID).Error; err != nil || afterOrder.UserID != customer.ID || afterOrder.TotalAmount != order.TotalAmount || afterOrder.Status != order.Status || afterOrder.PaymentMethod != order.PaymentMethod || afterOrder.ShippingAddress != order.ShippingAddress {
		t.Errorf("retained order changed: before=%+v after=%+v err=%v", order, afterOrder, err)
	}
	var afterItem core.OrderItem
	if err := h.db.First(&afterItem, item.ID).Error; err != nil || afterItem.OrderID != item.OrderID || afterItem.ProductID != item.ProductID || afterItem.Quantity != item.Quantity || afterItem.Price != item.Price {
		t.Errorf("retained order item changed: before=%+v after=%+v err=%v", item, afterItem, err)
	}
}

func TestUsersDeleteReassignmentRollsBackWhenFinalDeleteFails(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "delete-rollback-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "delete-rollback-target", Role: "author", Password: acctPassword})
	heir := acctCreateUser(t, h, acctUserSpec{Username: "delete-rollback-heir", Role: "editor"})
	adminToken := h.token(admin)
	targetToken := h.token(target)
	targetLoginToken, _ := acctLogin(t, h, target.Username, acctPassword, "", acctRemote())
	postID := revTestCreate(t, h, targetToken, map[string]any{
		"title": "Rollback snapshot", "content": "rollback original content", "status": "draft",
	})
	revTestPut(t, h, targetToken, postID, map[string]any{
		"title": "Rollback changed", "content": "rollback changed content", "status": "draft",
	})
	var before []core.Revision
	if err := h.db.Where("post_id = ?", postID).Order("id ASC").Find(&before).Error; err != nil || len(before) != 2 {
		t.Fatalf("read rollback revision fixture: count=%d err=%v", len(before), err)
	}
	activeSessionsBefore := acctCount(t, h, "SELECT COUNT(*) FROM login_sessions WHERE user_id = ? AND revoked_at IS NULL", target.ID)
	if activeSessionsBefore == 0 {
		t.Fatal("target login did not create an active session fixture")
	}
	targetID := target.ID
	comment := core.Comment{PostID: postID, AuthorID: &targetID, Name: "Rollback target", Email: target.Email, Body: "comment before delete", BodyHTML: "<p>comment before delete</p>", Status: "approved"}
	if err := h.db.Create(&comment).Error; err != nil {
		t.Fatalf("create rollback comment fixture: %v", err)
	}
	if err := oauthAccountInsertIdentity(h.db, target.ID, "google", "delete-rollback-google"); err != nil {
		t.Fatalf("create rollback OAuth identity fixture: %v", err)
	}
	auditBefore := acctCount(t, h, "SELECT COUNT(*) FROM oauth_identity_audit_log WHERE user_id = ?", target.ID)
	if err := h.db.Exec(`CREATE TABLE account_delete_final_blocker (user_id bigint NOT NULL REFERENCES users(id))`).Error; err != nil {
		t.Fatalf("create test-only final delete blocker: %v", err)
	}
	if err := h.db.Exec("INSERT INTO account_delete_final_blocker (user_id) VALUES (?)", target.ID).Error; err != nil {
		t.Fatalf("insert final delete blocker: %v", err)
	}

	failed := acctJSON(t, h, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d?reassign_to=%d", target.ID, heir.ID), "", adminToken)
	h.mustNoPanic(failed, "delete with injected final-delete failure")
	if failed.code != http.StatusInternalServerError {
		t.Fatalf("injected final user delete failure returned %d, want 500: %q", failed.code, failed.body)
	}
	if !usersAdminExists(t, h, target.ID) {
		t.Fatal("failed final delete removed the target user")
	}
	var post core.Post
	if err := h.db.First(&post, postID).Error; err != nil || post.AuthorID != target.ID {
		t.Errorf("post assignment after transaction rollback author=%d err=%v, want original author %d", post.AuthorID, err, target.ID)
	}
	var commentAfter core.Comment
	if err := h.db.First(&commentAfter, comment.ID).Error; err != nil || commentAfter.AuthorID == nil || *commentAfter.AuthorID != target.ID {
		t.Errorf("comment author after transaction rollback=%v err=%v, want target %d", commentAfter.AuthorID, err, target.ID)
	}
	var after []core.Revision
	if err := h.db.Where("post_id = ?", postID).Order("id ASC").Find(&after).Error; err != nil || len(after) != len(before) {
		t.Fatalf("revision history after rollback has %d rows, want %d; err=%v", len(after), len(before), err)
	}
	for i := range before {
		if after[i].UserID != target.ID || after[i].ID != before[i].ID || after[i].Title != before[i].Title || after[i].Content != before[i].Content {
			t.Errorf("revision row %d was not rolled back: before=%+v after=%+v", i, before[i], after[i])
		}
	}
	if active, history := oauthAccountIdentityCounts(t, h.db, target.ID, "google"); active != 1 || history != 1 {
		t.Errorf("OAuth identity after transaction rollback active/history=%d/%d, want 1/1", active, history)
	}
	if auditAfter := acctCount(t, h, "SELECT COUNT(*) FROM oauth_identity_audit_log WHERE user_id = ?", target.ID); auditAfter != auditBefore {
		t.Errorf("OAuth audit rows after transaction rollback=%d, want unchanged %d", auditAfter, auditBefore)
	}
	if activeSessions := acctCount(t, h, "SELECT COUNT(*) FROM login_sessions WHERE user_id = ? AND revoked_at IS NULL", target.ID); activeSessions != activeSessionsBefore {
		t.Errorf("active login sessions after transaction rollback=%d, want unchanged %d", activeSessions, activeSessionsBefore)
	}
	if profile := acctJSON(t, h, http.MethodGet, "/api/account/profile", "", targetLoginToken); profile.code != http.StatusOK {
		t.Errorf("original target JWT profile after transaction rollback returned %d: %q", profile.code, profile.body)
	}
}

func TestUsersResetPasswordLinkSingleUseAndRevokesSessions(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "reset-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "reset-target", Role: "author", Password: acctPassword})
	adminToken := h.token(admin)
	oldToken, _ := acctLogin(t, h, target.Username, acctPassword, "", acctRemote())
	legacyToken := h.token(target) // GenerateJWT creates a valid pre-session-row style credential.
	recoveryCodes := accountsSecurityEnableTOTP(t, h, target)
	staleMFAToken := accountsSecurityMFAToken(t, h, target.Username, acctPassword)
	parsed, _, err := new(jwt.Parser).ParseUnverified(legacyToken, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse legacy reset fixture JWT: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("legacy reset fixture claims type = %T, want jwt.MapClaims", parsed.Claims)
	}
	legacyJTI, ok := claims["jti"].(string)
	if !ok || legacyJTI == "" {
		t.Fatalf("legacy reset fixture JWT has no string jti: %#v", claims["jti"])
	}
	legacyJTIHash := sha256.Sum256([]byte(legacyJTI))
	legacyRevocationID := hex.EncodeToString(legacyJTIHash[:])
	if n := acctCount(t, h, "SELECT COUNT(*) FROM login_sessions WHERE id = ?", legacyRevocationID); n != 0 {
		t.Fatalf("legacy reset fixture jti already has %d session rows, want none before it is used", n)
	}

	reset := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/reset-password", target.ID), "", adminToken)
	h.mustNoPanic(reset, "reset-password")
	if !acctWantOK(t, reset, "POST /api/admin/users/:id/reset-password") {
		return
	}
	var link struct {
		ResetURL string `json:"reset_url"`
	}
	acctDecode(t, reset, "reset response", &link)
	if !strings.Contains(link.ResetURL, "/password-reset/") {
		t.Fatalf("reset_url = %q, want a /password-reset/<token> link", link.ResetURL)
	}
	resetToken := acctTokenFromURL(t, link.ResetURL, "/password-reset/")

	page := acctCall(t, h, acctRemote(), http.MethodGet, "/password-reset/"+resetToken, "", "", map[string]string{echo.HeaderAccept: "text/html"})
	h.mustNoPanic(page, "GET /password-reset/:token")
	acctWant(t, page, "GET /password-reset/:token", http.StatusOK)

	body := fmt.Sprintf(`{"token":%q,"password":"reset-password-new-1"}`, resetToken)
	done := acctJSON(t, h, http.MethodPost, "/api/password-reset", body, "")
	h.mustNoPanic(done, "POST /api/password-reset")
	acctWant(t, done, "POST /api/password-reset", http.StatusOK)

	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", oldToken); r.code != http.StatusUnauthorized {
		t.Errorf("JWT from before the reset returned %d, want 401", r.code)
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", legacyToken); r.code != http.StatusUnauthorized {
		t.Errorf("legacy JWT without a session row from before the reset returned %d, want 401", r.code)
	}
	if r := totpTestRecoveryStep(t, h, staleMFAToken, recoveryCodes[0]); r.code != http.StatusUnauthorized {
		t.Errorf("MFA token issued before admin password reset accepted a valid recovery code with status %d, want 401; body=%q", r.code, r.body)
	}
	if r := acctCall(t, h, acctRemote(), http.MethodPost, "/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, target.Username, acctPassword), "", map[string]string{"Content-Type": "application/json"}); r.code != http.StatusUnauthorized {
		t.Errorf("old password after reset returned %d, want 401", r.code)
	}
	if token := accountsSecurityMFAToken(t, h, target.Username, "reset-password-new-1"); token == "" {
		t.Error("new password after reset cannot start MFA login")
	}

	again := acctJSON(t, h, http.MethodPost, "/api/password-reset", body, "")
	acctWant(t, again, "second use of the reset link", http.StatusGone)

	second := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/reset-password", target.ID), "", adminToken)
	h.mustNoPanic(second, "second reset-password")
	var expiring struct {
		ResetURL string `json:"reset_url"`
	}
	acctDecode(t, second, "second reset response", &expiring)
	expiringToken := acctTokenFromURL(t, expiring.ResetURL, "/password-reset/")
	if err := h.db.Exec("UPDATE user_invites SET expires_at = ? WHERE user_id = ? AND kind = 'reset'", time.Now().Add(-time.Hour), target.ID).Error; err != nil {
		t.Fatalf("expire reset link: %v", err)
	}
	expired := acctJSON(t, h, http.MethodPost, "/api/password-reset", fmt.Sprintf(`{"token":%q,"password":"reset-password-late-2"}`, expiringToken), "")
	acctWant(t, expired, "reset with an expired link", http.StatusGone)
}

func TestUsersResetAndRevokeOthersAdvanceMFACredentialVersionImmediately(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "reset-mfa-version-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "reset-mfa-version-target", Role: "author", Password: acctPassword})
	recoveryCodes := accountsSecurityEnableTOTP(t, h, target)
	if len(recoveryCodes) < 4 {
		t.Fatalf("TOTP enrollment returned %d recovery codes, need four independent codes", len(recoveryCodes))
	}
	oldPasswordChallenge := accountsSecurityMFAToken(t, h, target.Username, acctPassword)
	adminToken := h.token(admin)

	profileIssueAndConsumeAdminReset(t, h, target, adminToken, "mfa-version-reset-password-66")
	resetCompletedAt := time.Now().UTC()
	resetCutoff := acctNullTime(t, h, "SELECT password_changed_at FROM users WHERE id = ?", target.ID)
	if !resetCutoff.Valid {
		t.Fatal("password reset did not set password_changed_at")
	}
	if !resetCutoff.Time.After(resetCompletedAt) {
		t.Fatalf("password_changed_at cutoff %s is not future relative to reset completion %s", resetCutoff.Time, resetCompletedAt)
	}

	// The pre-reset challenge must stay invalid even when submitted with an
	// otherwise-valid recovery code.
	if stale := totpTestRecoveryStep(t, h, oldPasswordChallenge, recoveryCodes[0]); stale.code != http.StatusUnauthorized {
		t.Fatalf("pre-reset MFA challenge returned %d, want 401; body=%q", stale.code, stale.body)
	}

	// This challenge is issued immediately after reset while the future cutoff
	// can still be ahead of JWT iat's one-second precision.
	postResetChallenge := accountsSecurityMFAToken(t, h, target.Username, "mfa-version-reset-password-66")
	if elapsed := time.Since(resetCompletedAt); elapsed >= time.Second {
		t.Fatalf("post-reset MFA challenge took %s, want issuance within one second", elapsed)
	}
	assertUsersAdminMFATokenIssuedBeforeCutoff(t, postResetChallenge, resetCutoff.Time, "post-reset challenge")
	postResetLogin := totpTestRecoveryStep(t, h, postResetChallenge, recoveryCodes[1])
	if postResetLogin.code != http.StatusOK {
		t.Fatalf("immediate post-reset MFA challenge returned %d, want 200; body=%q", postResetLogin.code, postResetLogin.body)
	}
	currentToken := totpTestLoginToken(t, postResetLogin)

	preRevokeChallenge := accountsSecurityMFAToken(t, h, target.Username, "mfa-version-reset-password-66")
	revoke := acctJSON(t, h, http.MethodPost, "/api/account/sessions/revoke-others", "", currentToken)
	if !acctWant(t, revoke, "revoke other sessions immediately after issuing MFA challenge", http.StatusOK) {
		return
	}
	revokeCompletedAt := time.Now().UTC()
	revokeCutoff := acctNullTime(t, h, "SELECT password_changed_at FROM users WHERE id = ?", target.ID)
	if !revokeCutoff.Valid || !revokeCutoff.Time.After(resetCutoff.Time) {
		t.Fatalf("revoke-others cutoff %v did not strictly advance past reset cutoff %s", revokeCutoff, resetCutoff.Time)
	}
	if stale := totpTestRecoveryStep(t, h, preRevokeChallenge, recoveryCodes[2]); stale.code != http.StatusUnauthorized {
		t.Fatalf("pre-revoke MFA challenge returned %d, want 401; body=%q", stale.code, stale.body)
	}

	postRevokeChallenge := accountsSecurityMFAToken(t, h, target.Username, "mfa-version-reset-password-66")
	if elapsed := time.Since(revokeCompletedAt); elapsed >= time.Second {
		t.Fatalf("post-revoke MFA challenge took %s, want issuance within one second", elapsed)
	}
	assertUsersAdminMFATokenIssuedBeforeCutoff(t, postRevokeChallenge, revokeCutoff.Time, "post-revoke challenge")
	postRevokeLogin := totpTestRecoveryStep(t, h, postRevokeChallenge, recoveryCodes[3])
	if postRevokeLogin.code != http.StatusOK {
		t.Errorf("immediate post-revoke MFA challenge returned %d, want 200; body=%q", postRevokeLogin.code, postRevokeLogin.body)
	}
}

func assertUsersAdminMFATokenIssuedBeforeCutoff(t *testing.T, token string, cutoff time.Time, label string) {
	t.Helper()
	parsed, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse %s claims: %v", label, err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("%s claims type = %T, want jwt.MapClaims", label, parsed.Claims)
	}
	issuedAt, ok := claims["iat"].(float64)
	if !ok || issuedAt <= 0 {
		t.Fatalf("%s iat = %#v, want positive NumericDate", label, claims["iat"])
	}
	if time.Unix(int64(issuedAt), 0).After(cutoff) {
		t.Fatalf("%s iat %s is later than future cutoff %s", label, time.Unix(int64(issuedAt), 0), cutoff)
	}
}

func TestUsersExpiredResetLinkCanBeReissuedWithoutLiveKindConflict(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "reset-expired-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "reset-expired-target", Role: "author", Password: acctPassword})
	adminToken := h.token(admin)

	first := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/reset-password", target.ID), "", adminToken)
	h.mustNoPanic(first, "create initial reset link")
	if !acctWantOK(t, first, "create initial reset link") {
		return
	}
	var firstLink struct {
		ResetURL string `json:"reset_url"`
	}
	acctDecode(t, first, "initial reset link", &firstLink)
	oldToken := acctTokenFromURL(t, firstLink.ResetURL, "/password-reset/")
	if err := h.db.Exec("UPDATE user_invites SET expires_at = ? WHERE user_id = ? AND kind = 'reset'", time.Now().Add(-time.Hour), target.ID).Error; err != nil {
		t.Fatalf("expire reset link: %v", err)
	}

	second := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/reset-password", target.ID), "", adminToken)
	h.mustNoPanic(second, "reissue expired reset link")
	if !acctWantOK(t, second, "reissue expired reset link") {
		return
	}
	var nextLink struct {
		ResetURL string `json:"reset_url"`
	}
	acctDecode(t, second, "reissued reset link", &nextLink)
	newToken := acctTokenFromURL(t, nextLink.ResetURL, "/password-reset/")
	if newToken == oldToken {
		t.Error("reissuing an expired reset link returned the expired token")
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND kind = 'reset' AND accepted_at IS NULL AND revoked_at IS NULL", target.ID); n != 1 {
		t.Errorf("live reset rows after reissue = %d, want 1", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND token_hash = ? AND expires_at > now()", target.ID, acctSHA256Hex(newToken)); n != 1 {
		t.Errorf("new non-expired reset row for replacement token = %d, want 1", n)
	}
	stale := acctJSON(t, h, http.MethodPost, "/api/password-reset", fmt.Sprintf(`{"token":%q,"password":"stale-reset-password-1"}`, oldToken), "")
	if stale.code != http.StatusGone && stale.code != http.StatusNotFound {
		t.Errorf("old expired reset link returned %d, want 410 or 404", stale.code)
	}
}

func TestUsersInviteAcceptAndPasswordResetRejectCrossSitePlainTextJSON(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "csrf-users-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "csrf-reset-target", Role: "author", Password: acctPassword})
	adminToken := h.token(admin)

	invite, inviteRes := usersAdminInvite(t, h, adminToken, "csrf-invite-target@example.test", "author")
	if !acctWant(t, inviteRes, "create invite for CSRF test", http.StatusCreated) {
		return
	}
	inviteToken := acctTokenFromURL(t, invite.InviteURL, "/invite/")
	resetRes := acctJSON(t, h, http.MethodPost, fmt.Sprintf("/api/admin/users/%d/reset-password", target.ID), "", adminToken)
	h.mustNoPanic(resetRes, "create reset link for CSRF test")
	if !acctWantOK(t, resetRes, "create reset link for CSRF test") {
		return
	}
	var resetLink struct {
		ResetURL string `json:"reset_url"`
	}
	acctDecode(t, resetRes, "reset link for CSRF test", &resetLink)
	resetToken := acctTokenFromURL(t, resetLink.ResetURL, "/password-reset/")

	cases := []struct {
		name, path, body string
	}{
		{"invite acceptance", "/api/invites/accept", fmt.Sprintf(`{"token":%q,"username":"csrf-invite-accepted","password":"csrf-invite-password-1"}`, inviteToken)},
		{"password reset", "/api/password-reset", fmt.Sprintf(`{"token":%q,"password":"csrf-reset-password-2"}`, resetToken)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := acctCall(t, h, acctRemote(), http.MethodPost, tc.path, tc.body, "", map[string]string{
				"Origin":       "https://attacker.example",
				"Content-Type": "text/plain",
			})
			h.mustNoPanic(res, "cross-site text/plain "+tc.name)
			if res.code != http.StatusForbidden {
				t.Fatalf("cross-site text/plain %s returned %d, want 403; body=%q", tc.name, res.code, res.body)
			}
		})
	}

	if status := usersAdminStatusOf(t, h, invite.ID); status != "invited" {
		t.Errorf("cross-site invite acceptance changed account status to %q, want invited", status)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND kind = 'invite' AND token_hash = ? AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()", invite.ID, acctSHA256Hex(inviteToken)); n != 1 {
		t.Errorf("cross-site invite acceptance consumed or changed the valid token; rows=%d, want 1", n)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM user_invites WHERE user_id = ? AND kind = 'reset' AND token_hash = ? AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()", target.ID, acctSHA256Hex(resetToken)); n != 1 {
		t.Errorf("cross-site password reset consumed or changed the valid token; rows=%d, want 1", n)
	}
	if token, _ := acctLogin(t, h, target.Username, acctPassword, "", acctRemote()); token == "" {
		t.Error("cross-site password reset changed the target password")
	}

	accepted := usersAdminAccept(t, h, inviteToken, "csrf-invite-password-1", "")
	acctWant(t, accepted, "same invite token after rejected cross-site request", http.StatusOK)
	reset := acctJSON(t, h, http.MethodPost, "/api/password-reset", fmt.Sprintf(`{"token":%q,"password":"csrf-reset-password-2"}`, resetToken), "")
	acctWant(t, reset, "same reset token after rejected cross-site request", http.StatusOK)
}

func TestUsersBulkDisableEnableSetRoleDelete(t *testing.T) {
	h := newAccessHarness(t)
	actor := acctCreateUser(t, h, acctUserSpec{Username: "bulk-admin", Role: "admin"})
	coAdmin := acctCreateUser(t, h, acctUserSpec{Username: "bulk-coadmin", Role: "admin"})
	b1 := acctCreateUser(t, h, acctUserSpec{Username: "bulk-one", Role: "author"})
	b2 := acctCreateUser(t, h, acctUserSpec{Username: "bulk-two", Role: "author"})
	b3 := acctCreateUser(t, h, acctUserSpec{Username: "bulk-three", Role: "author"})
	token := h.token(actor)

	res, body := usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%d,%d,987654],"action":"disable"}`, b1.ID, b2.ID))
	if acctWant(t, res, "bulk disable", http.StatusOK) {
		if body["updated"] != float64(2) {
			t.Errorf("bulk disable updated = %v, want 2", body["updated"])
		}
		skipped, _ := body["skipped"].([]any)
		if len(skipped) != 1 {
			t.Errorf("bulk disable skipped = %v, want the missing id with a reason", skipped)
		}
	}
	if usersAdminStatusOf(t, h, b1.ID) != "disabled" || usersAdminStatusOf(t, h, b2.ID) != "disabled" {
		t.Error("bulk disable did not disable both users")
	}

	res, body = usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%d],"action":"enable"}`, b1.ID))
	if acctWant(t, res, "bulk enable", http.StatusOK) && body["updated"] != float64(1) {
		t.Errorf("bulk enable updated = %v, want 1", body["updated"])
	}
	if got := usersAdminStatusOf(t, h, b1.ID); got != "active" {
		t.Errorf("bulk enable left status %q", got)
	}
	if got := usersAdminStatusOf(t, h, b2.ID); got != "disabled" {
		t.Errorf("bulk enable touched an unlisted user: %q", got)
	}

	res, body = usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%d,%d],"action":"set_role","role":"editor"}`, b3.ID, b1.ID))
	if acctWant(t, res, "bulk set_role", http.StatusOK) && body["updated"] != float64(2) {
		t.Errorf("bulk set_role updated = %v, want 2", body["updated"])
	}
	if usersAdminRoleOf(t, h, b3.ID) != "editor" || usersAdminRoleOf(t, h, b1.ID) != "editor" {
		t.Error("bulk set_role did not change both roles")
	}
	acctWant(t, usersAdminPatch(t, h, token, actor.ID, `{"role":"admin"}`), "PATCH own role again", http.StatusConflict)

	res, body = usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%d,%d],"action":"disable"}`, actor.ID, coAdmin.ID))
	if acctWant(t, res, "bulk disable including self", http.StatusOK) {
		skipped, _ := body["skipped"].([]any)
		selfSkipped := false
		for _, entry := range skipped {
			item, _ := entry.(map[string]any)
			if item["id"] == float64(actor.ID) {
				reason, _ := item["reason"].(string)
				selfSkipped = reason != ""
			}
		}
		if !selfSkipped {
			t.Errorf("bulk disable did not skip the actor with a reason: %v", skipped)
		}
	}
	if usersAdminStatusOf(t, h, actor.ID) != "active" {
		t.Error("bulk disable disabled the acting admin")
	}

	res, body = usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%d],"action":"delete"}`, b2.ID))
	if acctWant(t, res, "bulk delete", http.StatusOK) && body["updated"] != float64(1) {
		t.Errorf("bulk delete updated = %v, want 1", body["updated"])
	}
	if usersAdminExists(t, h, b2.ID) {
		t.Error("bulk delete left the user row")
	}

	tooMany := make([]string, 0, 101)
	for i := 0; i < 101; i++ {
		tooMany = append(tooMany, fmt.Sprint(900000+i))
	}
	res, _ = usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%s],"action":"disable"}`, strings.Join(tooMany, ",")))
	acctWant(t, res, "bulk with 101 ids", http.StatusBadRequest)
	res, _ = usersAdminBulk(t, h, token, fmt.Sprintf(`{"ids":[%d],"action":"promote"}`, b1.ID))
	acctWant(t, res, "bulk with an unknown action", http.StatusBadRequest)
}

func TestUsersCreatedWithoutRoleBecomesAuthor(t *testing.T) {
	h := newAccessHarness(t)
	u := core.User{Username: "no-role-user", Email: "no-role-user@example.test", Provider: "local"}
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create user without role: %v", err)
	}
	var reloaded core.User
	if err := h.db.First(&reloaded, u.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.Role != "author" {
		t.Errorf("role of a user created without a role = %q, want author", reloaded.Role)
	}
	if reloaded.Status != "active" {
		t.Errorf("status of a user created without a status = %q, want active", reloaded.Status)
	}
}

func TestUsersNavAndPaletteVisibleToAdminOnly(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "nav-admin", Role: "admin"})
	editor := acctCreateUser(t, h, acctUserSpec{Username: "nav-editor", Role: "editor"})

	adminPalette := acctJSON(t, h, http.MethodGet, "/api/admin/palette", "", h.token(admin))
	h.mustNoPanic(adminPalette, "admin palette")
	if acctWant(t, adminPalette, "admin GET /api/admin/palette", http.StatusOK) && !strings.Contains(adminPalette.body, "/admin/users") {
		t.Errorf("admin palette has no entry for the users page or invite command: %s", adminPalette.body)
	}
	editorPalette := acctJSON(t, h, http.MethodGet, "/api/admin/palette", "", h.token(editor))
	h.mustNoPanic(editorPalette, "editor palette")
	if acctWant(t, editorPalette, "editor GET /api/admin/palette", http.StatusOK) && strings.Contains(editorPalette.body, "/admin/users") {
		t.Errorf("editor palette lists the admin-only users page: %s", editorPalette.body)
	}

	adminPage := h.request(http.MethodGet, "/admin", "", h.token(admin), "text/html", "")
	h.mustNoPanic(adminPage, "admin dashboard")
	if acctWant(t, adminPage, "admin GET /admin", http.StatusOK) && !strings.Contains(adminPage.body, `/admin/users`) {
		t.Error("admin rail has no users entry linking to /admin/users")
	}
	editorPage := h.request(http.MethodGet, "/admin", "", h.token(editor), "text/html", "")
	h.mustNoPanic(editorPage, "editor dashboard")
	if acctWant(t, editorPage, "editor GET /admin", http.StatusOK) && strings.Contains(editorPage.body, `/admin/users`) {
		t.Error("editor rail lists the admin-only users page")
	}
}

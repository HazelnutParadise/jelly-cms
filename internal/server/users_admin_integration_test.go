package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
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

func TestUsersResetPasswordLinkSingleUseAndRevokesSessions(t *testing.T) {
	h := newAccessHarness(t)
	admin := acctCreateUser(t, h, acctUserSpec{Username: "reset-admin", Role: "admin"})
	target := acctCreateUser(t, h, acctUserSpec{Username: "reset-target", Role: "author", Password: acctPassword})
	adminToken := h.token(admin)
	oldToken, _ := acctLogin(t, h, target.Username, acctPassword, "", acctRemote())

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
	if r := acctCall(t, h, acctRemote(), http.MethodPost, "/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, target.Username, acctPassword), "", map[string]string{"Content-Type": "application/json"}); r.code != http.StatusUnauthorized {
		t.Errorf("old password after reset returned %d, want 401", r.code)
	}
	if token, _ := acctLogin(t, h, target.Username, "reset-password-new-1", "", acctRemote()); token == "" {
		t.Error("new password after reset cannot log in")
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

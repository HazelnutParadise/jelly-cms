package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/plugin"
)

// User event tests (docs/tickets/28-plugin-events.md, 帳號 rows of the
// acceptance table). Every request goes through the real HTTP stack and every
// assertion waits for the runtime to flush before it reads a payload. A
// payload must never carry a password, so each check that reads a user object
// also checks that no "password" key exists.

// peuSettle lets log lines still in transit reach the capture sink before an
// exact count is read.
const peuSettle = 150 * time.Millisecond

// peuExactly flushes, waits for at least n deliveries of hook, settles and
// fails unless exactly n were delivered since the last reset.
func peuExactly(t *testing.T, h *pluginEventsHarness, hook plugin.Hook, n int) []pluginEvent {
	t.Helper()
	h.waitEvents(t, hook, n)
	time.Sleep(peuSettle)
	h.flush(t)
	got := h.events(t, pluginEventsFixtureID, hook)
	if len(got) != n {
		t.Fatalf("expected exactly %d %s deliveries, got %d: %+v\ncaptured log:\n%s", n, hook, len(got), got, h.sink.dump())
	}
	return got
}

// peuBase checks the fields every payload carries.
func peuBase(t *testing.T, payload map[string]any, name string) {
	t.Helper()
	if payload["event"] != name {
		t.Fatalf("payload event = %v, want %s", payload["event"], name)
	}
	at, ok := payload["at"].(string)
	if !ok {
		t.Fatalf("payload has no at string: %v", payload)
	}
	if _, err := time.Parse(time.RFC3339, at); err != nil {
		t.Fatalf("payload at %q is not RFC3339: %v", at, err)
	}
}

// peuNoPassword fails when a user object carries a password key.
func peuNoPassword(t *testing.T, user map[string]any) {
	t.Helper()
	if _, ok := user["password"]; ok {
		t.Fatalf("user object carries a password key: %v", user)
	}
}

// peuStrings returns payload[key] as a string array, failing when it is not one.
func peuStrings(t *testing.T, payload map[string]any, key string) []string {
	t.Helper()
	raw, ok := payload[key].([]any)
	if !ok {
		t.Fatalf("%s is not an array: %v", key, payload[key])
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s holds a non-string: %v", key, v)
		}
		out = append(out, s)
	}
	return out
}

// peuWantChanges fails unless every name appears in payload["changes"].
func peuWantChanges(t *testing.T, payload map[string]any, names ...string) {
	t.Helper()
	got := peuStrings(t, payload, "changes")
	for _, want := range names {
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("changes = %v, want it to contain %q", got, want)
		}
	}
}

// peuWantAdmin checks that the actor object is the given administrator.
func peuWantAdmin(t *testing.T, payload map[string]any, adminID uint) {
	t.Helper()
	actor := nested(t, payload, "actor")
	if number(t, actor, "id") != float64(adminID) {
		t.Fatalf("actor.id = %v, want %d", actor["id"], adminID)
	}
}

// peuUserID returns payload.user.id.
func peuUserID(t *testing.T, payload map[string]any) float64 {
	t.Helper()
	return number(t, nested(t, payload, "user"), "id")
}

func TestPluginEventsUsersInviteAndAccept(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "peu-invite-admin")
	token := h.token(admin)

	invite, res := usersAdminInvite(t, h.accessHarness, token, "peu.invitee@example.test", "editor")
	h.mustNoPanic(res, "invite create")
	if !acctWant(t, res, "POST /api/admin/users/invite", http.StatusCreated) {
		return
	}

	created := peuExactly(t, h, plugin.HookOnUserCreate, 1)[0].Payload
	peuBase(t, created, "OnUserCreate")
	user := nested(t, created, "user")
	if number(t, user, "id") != float64(invite.ID) {
		t.Errorf("OnUserCreate user.id = %v, want %d", user["id"], invite.ID)
	}
	if s, _ := user["username"].(string); s == "" {
		t.Errorf("OnUserCreate user.username is empty: %v", user)
	}
	if user["email"] != "peu.invitee@example.test" || user["role"] != "editor" || user["status"] != "invited" {
		t.Errorf("OnUserCreate user = %v, want email peu.invitee@example.test, role editor, status invited", user)
	}
	peuNoPassword(t, user)
	if created["source"] != "invite" {
		t.Errorf("OnUserCreate source = %v, want invite", created["source"])
	}
	peuWantAdmin(t, created, admin.ID)

	inviteToken := acctTokenFromURL(t, invite.InviteURL, "/invite/")
	accepted := usersAdminAccept(t, h.accessHarness, inviteToken, "peu-accepted-pass-1", "Accepted Person")
	if !acctWant(t, accepted, "POST /api/invites/accept", http.StatusOK) {
		return
	}

	updated := peuExactly(t, h, plugin.HookOnUserUpdate, 1)[0].Payload
	peuBase(t, updated, "OnUserUpdate")
	if got := peuUserID(t, updated); got != float64(invite.ID) {
		t.Errorf("OnUserUpdate user.id = %v, want %d", got, invite.ID)
	}
	if status := nested(t, updated, "user")["status"]; status != "active" {
		t.Errorf("OnUserUpdate user.status = %v, want active", status)
	}
	peuWantChanges(t, updated, "status")
	peuNoPassword(t, nested(t, updated, "user"))
}

func TestPluginEventsUsersPatchRoleAndDisplayName(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "peu-patch-admin")
	target := acctCreateUser(t, h.accessHarness, acctUserSpec{Username: "peu-patch-target", Role: "author", DisplayName: "Old Name"})
	token := h.token(admin)

	res := usersAdminPatch(t, h.accessHarness, token, target.ID, `{"role":"editor","display_name":"New Name"}`)
	if !acctWant(t, res, "PATCH role and display name", http.StatusOK) {
		return
	}
	payload := peuExactly(t, h, plugin.HookOnUserUpdate, 1)[0].Payload
	peuBase(t, payload, "OnUserUpdate")
	if got := peuUserID(t, payload); got != float64(target.ID) {
		t.Errorf("OnUserUpdate user.id = %v, want %d", got, target.ID)
	}
	user := nested(t, payload, "user")
	if user["role"] != "editor" {
		t.Errorf("OnUserUpdate user.role = %v, want editor", user["role"])
	}
	peuWantChanges(t, payload, "role", "display_name")
	peuWantAdmin(t, payload, admin.ID)
	peuNoPassword(t, user)

	h.reset(t)
	empty := usersAdminPatch(t, h.accessHarness, token, target.ID, `{}`)
	h.mustNoPanic(empty, "PATCH without changed fields")
	h.requireNoEvent(t, plugin.HookOnUserUpdate)
}

func TestPluginEventsUsersDeleteSingleAndBulk(t *testing.T) {
	h := newPluginEventsHarness(t)
	admin := h.user("admin", "peu-delete-admin")
	token := h.token(admin)
	single := acctCreateUser(t, h.accessHarness, acctUserSpec{Username: "peu-del-one", Role: "author"})
	b1 := acctCreateUser(t, h.accessHarness, acctUserSpec{Username: "peu-del-two", Role: "author"})
	b2 := acctCreateUser(t, h.accessHarness, acctUserSpec{Username: "peu-del-three", Role: "editor"})

	del := acctJSON(t, h.accessHarness, http.MethodDelete, fmt.Sprintf("/api/admin/users/%d", single.ID), "", token)
	h.mustNoPanic(del, "DELETE user")
	if !acctWantOK(t, del, "DELETE /api/admin/users/:id") {
		return
	}
	payload := peuExactly(t, h, plugin.HookOnUserDelete, 1)[0].Payload
	peuBase(t, payload, "OnUserDelete")
	user := nested(t, payload, "user")
	if number(t, user, "id") != float64(single.ID) || user["username"] != "peu-del-one" || user["email"] != single.Email || user["role"] != "author" {
		t.Errorf("OnUserDelete user = %v, want id %d, username peu-del-one, role author", user, single.ID)
	}
	peuWantAdmin(t, payload, admin.ID)

	h.reset(t)
	bulk, _ := usersAdminBulk(t, h.accessHarness, token, fmt.Sprintf(`{"ids":[%d,%d],"action":"delete"}`, b1.ID, b2.ID))
	if !acctWant(t, bulk, "bulk delete", http.StatusOK) {
		return
	}
	seen := map[float64]bool{}
	for _, ev := range peuExactly(t, h, plugin.HookOnUserDelete, 2) {
		peuBase(t, ev.Payload, "OnUserDelete")
		seen[peuUserID(t, ev.Payload)] = true
		peuWantAdmin(t, ev.Payload, admin.ID)
	}
	if !seen[float64(b1.ID)] || !seen[float64(b2.ID)] {
		t.Errorf("bulk OnUserDelete ids = %v, want %d and %d", seen, b1.ID, b2.ID)
	}
}

func TestPluginEventsUsersPasswordLoginSuccessAndFailure(t *testing.T) {
	const password = "peu-login-pass-1"
	h := newPluginEventsHarness(t)
	u := acctCreateUser(t, h.accessHarness, acctUserSpec{Username: "peu-login", Role: "author", Password: password})

	token, _ := acctLogin(t, h.accessHarness, u.Username, password, "", acctRemote())
	if token == "" {
		t.Fatal("login returned no token")
	}
	login := peuExactly(t, h, plugin.HookOnUserLogin, 1)[0].Payload
	peuBase(t, login, "OnUserLogin")
	if got := peuUserID(t, login); got != float64(u.ID) {
		t.Errorf("OnUserLogin user.id = %v, want %d", got, u.ID)
	}
	if login["method"] != "password" {
		t.Errorf("OnUserLogin method = %v, want password", login["method"])
	}
	if _, ok := login["ip_masked"]; !ok {
		t.Errorf("OnUserLogin has no ip_masked key: %v", login)
	}
	peuNoPassword(t, nested(t, login, "user"))

	h.reset(t)
	wrong := acctCall(t, h.accessHarness, acctRemote(), http.MethodPost, "/auth/login", loginAttemptPayload(u.Username, "peu-wrong-pass"), "", map[string]string{"Content-Type": "application/json"})
	h.mustNoPanic(wrong, "login with a wrong password")
	acctWant(t, wrong, "login with a wrong password", http.StatusUnauthorized)
	failed := peuExactly(t, h, plugin.HookOnUserLoginFailed, 1)[0].Payload
	peuBase(t, failed, "OnUserLoginFailed")
	if failed["identifier"] != u.Username {
		t.Errorf("OnUserLoginFailed identifier = %v, want %s", failed["identifier"], u.Username)
	}
	if reason, _ := failed["reason"].(string); strings.TrimSpace(reason) == "" {
		t.Errorf("OnUserLoginFailed reason is empty: %v", failed)
	}
	if _, ok := failed["ip_masked"]; !ok {
		t.Errorf("OnUserLoginFailed has no ip_masked key: %v", failed)
	}
	if strings.Contains(fmt.Sprint(failed), "peu-wrong-pass") {
		t.Errorf("OnUserLoginFailed payload carries the submitted password: %v", failed)
	}
	h.requireNoEvent(t, plugin.HookOnUserLogin)

	h.reset(t)
	const ghost = "peu-ghost-account"
	missing := acctCall(t, h.accessHarness, acctRemote(), http.MethodPost, "/auth/login", loginAttemptPayload(ghost, "peu-wrong-pass"), "", map[string]string{"Content-Type": "application/json"})
	h.mustNoPanic(missing, "login with an unknown account")
	acctWant(t, missing, "login with an unknown account", http.StatusUnauthorized)
	ghostFailed := peuExactly(t, h, plugin.HookOnUserLoginFailed, 1)[0].Payload
	if ghostFailed["identifier"] != ghost {
		t.Errorf("unknown-account OnUserLoginFailed identifier = %v, want %s", ghostFailed["identifier"], ghost)
	}
	h.requireNoEvent(t, plugin.HookOnUserLogin)
}

func TestPluginEventsUsersLogoutRevokesLogin(t *testing.T) {
	const password = "peu-logout-pass-1"
	h := newPluginEventsHarness(t)
	u := acctCreateUser(t, h.accessHarness, acctUserSpec{Username: "peu-logout", Role: "author", Password: password})
	token, cookies := acctLogin(t, h.accessHarness, u.Username, password, "", acctRemote())
	h.reset(t)

	out := revocationRequest(h.accessHarness, http.MethodPost, "/auth/logout", cookies, token, "")
	h.mustNoPanic(out, "logout")
	if !acctWant(t, out, "POST /auth/logout", http.StatusOK) {
		return
	}
	payload := peuExactly(t, h, plugin.HookOnUserLogout, 1)[0].Payload
	peuBase(t, payload, "OnUserLogout")
	if got := peuUserID(t, payload); got != float64(u.ID) {
		t.Errorf("OnUserLogout user.id = %v, want %d", got, u.ID)
	}
	peuNoPassword(t, nested(t, payload, "user"))
}

func TestPluginEventsUsersCheckoutCreatesCustomerOnce(t *testing.T) {
	h := newPluginEventsHarness(t)
	shopSetup(t, h.accessHarness)
	p := shopSeedProduct(t, h.accessHarness, "peu-account-tee", 12000, 10)
	v := shopVariantRows(t, h.accessHarness, p.ID)[0]
	const email = "peu.checkout@example.test"

	shopPlaceCheckout(t, h.accessHarness, v.ID, 1, email, "credit_card", "peu-dormant-1")
	first := peuExactly(t, h, plugin.HookOnUserCreate, 1)[0].Payload
	peuBase(t, first, "OnUserCreate")
	if first["source"] != "checkout" {
		t.Errorf("OnUserCreate source = %v, want checkout", first["source"])
	}
	user := nested(t, first, "user")
	if user["role"] != "customer" {
		t.Errorf("OnUserCreate user.role = %v, want customer", user["role"])
	}
	if s, _ := user["email"].(string); s != email {
		t.Errorf("OnUserCreate user.email = %v, want %s", user["email"], email)
	}
	peuNoPassword(t, user)

	shopPlaceCheckout(t, h.accessHarness, v.ID, 1, email, "credit_card", "peu-dormant-2")
	h.flush(t)
	time.Sleep(peuSettle)
	h.flush(t)
	if got := h.events(t, pluginEventsFixtureID, plugin.HookOnUserCreate); len(got) != 1 {
		t.Fatalf("second checkout with the same email emitted OnUserCreate again: %d deliveries total, want 1", len(got))
	}
}

package server

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
)

// Signed-in device acceptance (docs/tickets/06-accounts.md, S11 to S13 and the
// login_sessions contract). Helpers live in account_profile_integration_test.go.

const (
	devicesSafariUA  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15"
	devicesChromeUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	devicesFirefoxUA = "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0"
)

var devicesIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

type devicesRow map[string]any

// devicesList returns the caller's device rows, failing the test on a bad response.
func devicesList(t *testing.T, h *accessHarness, token, cookie string) []devicesRow {
	t.Helper()
	headers := map[string]string{}
	if cookie != "" {
		headers["Cookie"] = cookie
	}
	res := acctCall(t, h, acctRemote(), http.MethodGet, "/api/account/sessions", "", token, headers)
	h.mustNoPanic(res, "GET /api/account/sessions")
	if res.code != http.StatusOK {
		t.Fatalf("GET /api/account/sessions returned %d, want 200; body=%q", res.code, res.body)
	}
	var rows []devicesRow
	acctDecode(t, res, "sessions list", &rows)
	return rows
}

// devicesRowByLabel finds the one row whose device_label contains needle.
func devicesRowByLabel(t *testing.T, rows []devicesRow, needle string) devicesRow {
	t.Helper()
	var found devicesRow
	for _, row := range rows {
		if label, _ := row["device_label"].(string); strings.Contains(label, needle) {
			if found != nil {
				t.Fatalf("more than one device row labelled like %q", needle)
			}
			found = row
		}
	}
	if found == nil {
		t.Fatalf("no device row labelled like %q in %v", needle, rows)
	}
	return found
}

// devicesIDOfCurrent returns the id of the single row flagged current:true.
func devicesIDOfCurrent(t *testing.T, rows []devicesRow) string {
	t.Helper()
	var ids []string
	for _, row := range rows {
		if current, _ := row["current"].(bool); current {
			id, _ := row["id"].(string)
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 {
		t.Fatalf("rows flagged current = %v, want exactly one", ids)
	}
	return ids[0]
}

func devicesRowID(t *testing.T, row devicesRow) string {
	t.Helper()
	id, _ := row["id"].(string)
	if id == "" {
		t.Fatalf("device row has no id: %v", row)
	}
	return id
}

func TestDevicesListShowsCurrentFlagAndNoRawLoginID(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "devices-list", Role: "author", Password: acctPassword})
	first, firstCookies := acctLogin(t, h, u.Username, acctPassword, devicesSafariUA, "198.51.100.77:4321")
	second, _ := acctLogin(t, h, u.Username, acctPassword, devicesChromeUA, "198.51.100.78:4321")

	rows := devicesList(t, h, second, "")
	if len(rows) != 2 {
		t.Fatalf("sessions list has %d rows, want 2: %v", len(rows), rows)
	}
	for _, row := range rows {
		id := devicesRowID(t, row)
		if !devicesIDPattern.MatchString(id) {
			t.Errorf("session id %q is not 16 lowercase hex characters", id)
		}
		for _, key := range []string{"device_label", "ip_masked", "created_at", "last_seen_at", "current", "new_device"} {
			if _, ok := row[key]; !ok {
				t.Errorf("session row is missing key %q: %v", key, row)
			}
		}
		ip, _ := row["ip_masked"].(string)
		if !strings.HasSuffix(ip, ".0") && !strings.HasSuffix(ip, "::") {
			t.Errorf("ip_masked = %q, want the last IPv4 octet or IPv6 tail masked", ip)
		}
		if strings.Contains(ip, "198.51.100.77") || strings.Contains(ip, "198.51.100.78") {
			t.Errorf("ip_masked = %q still carries the raw address", ip)
		}
	}

	safari := devicesRowByLabel(t, rows, "Safari")
	chrome := devicesRowByLabel(t, rows, "Chrome")
	if safari["device_label"] == chrome["device_label"] {
		t.Errorf("device labels do not come from the User-Agent: both %v", safari["device_label"])
	}
	if current, _ := chrome["current"].(bool); !current {
		t.Errorf("the Chrome login made the request but its row is not current:true: %v", chrome)
	}
	if current, _ := safari["current"].(bool); current {
		t.Errorf("the Safari login did not make the request but its row is current:true: %v", safari)
	}

	fromCookies := devicesList(t, h, "", acctCookieHeader(firstCookies))
	if len(fromCookies) != 2 {
		t.Fatalf("cookie-authenticated list has %d rows, want 2", len(fromCookies))
	}
	if devicesIDOfCurrent(t, fromCookies) != devicesRowID(t, devicesRowByLabel(t, fromCookies, "Safari")) {
		t.Error("cookie session from the Safari login did not mark the Safari row as current")
	}

	raw := acctCall(t, h, acctRemote(), http.MethodGet, "/api/account/sessions", "", second, nil)
	jtiFirst := acctJTI(t, first)
	jtiSecond := acctJTI(t, second)
	for _, secret := range []string{jtiFirst, jtiSecond, first, second} {
		if strings.Contains(raw.body, secret) {
			t.Errorf("sessions response exposes a raw login credential or jti: %q", secret)
		}
	}
}

func TestDevicesRevokeOneKillsThatJWTOnly(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "devices-revoke-one", Role: "author", Password: acctPassword})
	other, _ := acctLogin(t, h, u.Username, acctPassword, devicesSafariUA, acctRemote())
	current, _ := acctLogin(t, h, u.Username, acctPassword, devicesChromeUA, acctRemote())

	rows := devicesList(t, h, current, "")
	target := devicesRowByLabel(t, rows, "Safari")
	res := acctJSON(t, h, http.MethodDelete, "/api/account/sessions/"+devicesRowID(t, target), "", current)
	h.mustNoPanic(res, "DELETE one session")
	acctWantOK(t, res, "DELETE /api/account/sessions/:id")

	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", other); r.code != http.StatusUnauthorized {
		t.Errorf("revoked device's JWT returned %d on /api/auth/me, want 401", r.code)
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", current); r.code != http.StatusOK {
		t.Errorf("other device's JWT returned %d on /api/auth/me, want 200", r.code)
	}
	after := devicesList(t, h, current, "")
	if len(after) != 1 {
		t.Fatalf("after revoking one device the list has %d rows, want 1", len(after))
	}
	if current, _ := after[0]["current"].(bool); !current {
		t.Errorf("remaining row is not flagged current: %v", after[0])
	}
}

func TestDevicesRevokeOthersKeepsCurrent(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "devices-revoke-others", Role: "author", Password: acctPassword})
	one, _ := acctLogin(t, h, u.Username, acctPassword, devicesSafariUA, acctRemote())
	two, _ := acctLogin(t, h, u.Username, acctPassword, devicesFirefoxUA, acctRemote())
	current, _ := acctLogin(t, h, u.Username, acctPassword, devicesChromeUA, acctRemote())

	if len(devicesList(t, h, current, "")) != 3 {
		t.Fatal("expected three signed-in devices before revoking the others")
	}
	res := acctJSON(t, h, http.MethodPost, "/api/account/sessions/revoke-others", "", current)
	h.mustNoPanic(res, "POST revoke-others")
	acctWantOK(t, res, "POST /api/account/sessions/revoke-others")

	for name, token := range map[string]string{"first other": one, "second other": two} {
		if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", token); r.code != http.StatusUnauthorized {
			t.Errorf("%s device's JWT returned %d after revoke-others, want 401", name, r.code)
		}
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", current); r.code != http.StatusOK {
		t.Errorf("current device's JWT returned %d after revoke-others, want 200", r.code)
	}
	rows := devicesList(t, h, current, "")
	if len(rows) != 1 {
		t.Fatalf("after revoke-others the list has %d rows, want 1", len(rows))
	}
	devicesIDOfCurrent(t, rows)
}

func TestDevicesRevokeOtherUsersSession404(t *testing.T) {
	h := newAccessHarness(t)
	owner := acctCreateUser(t, h, acctUserSpec{Username: "devices-owner", Role: "author", Password: acctPassword})
	intruder := acctCreateUser(t, h, acctUserSpec{Username: "devices-intruder", Role: "author", Password: acctPassword})
	ownerToken, _ := acctLogin(t, h, owner.Username, acctPassword, devicesSafariUA, acctRemote())
	intruderToken, _ := acctLogin(t, h, intruder.Username, acctPassword, devicesChromeUA, acctRemote())

	ownerRows := devicesList(t, h, ownerToken, "")
	if len(ownerRows) != 1 {
		t.Fatalf("owner list has %d rows, want 1", len(ownerRows))
	}
	ownerSession := devicesRowID(t, ownerRows[0])

	res := acctJSON(t, h, http.MethodDelete, "/api/account/sessions/"+ownerSession, "", intruderToken)
	h.mustNoPanic(res, "DELETE another user's session")
	acctWant(t, res, "DELETE another user's session id", http.StatusNotFound)

	missing := acctJSON(t, h, http.MethodDelete, "/api/account/sessions/0123456789abcdef", "", intruderToken)
	acctWant(t, missing, "DELETE unknown session id", http.StatusNotFound)

	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", ownerToken); r.code != http.StatusOK {
		t.Errorf("owner's session was disturbed by another user's revoke attempt: %d", r.code)
	}
}

func TestDevicesLogoutMarksSessionRevoked(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "devices-logout", Role: "author", Password: acctPassword})
	token, _ := acctLogin(t, h, u.Username, acctPassword, devicesSafariUA, acctRemote())

	before := acctCount(t, h, "SELECT COUNT(*) FROM login_sessions WHERE user_id = ? AND revoked_at IS NULL", u.ID)
	if before != 1 {
		t.Fatalf("active login_sessions rows before logout = %d, want 1", before)
	}

	res := acctJSON(t, h, http.MethodPost, "/auth/logout", "", token)
	h.mustNoPanic(res, "POST /auth/logout")
	if res.code >= 300 {
		t.Errorf("POST /auth/logout returned %d, want success; body=%q", res.code, res.body)
	}

	if n := acctCount(t, h, "SELECT COUNT(*) FROM login_sessions WHERE user_id = ? AND revoked_at IS NOT NULL", u.ID); n != 1 {
		t.Errorf("login_sessions rows with revoked_at set after logout = %d, want 1", n)
	}
	var session core.LoginSession
	if err := h.db.Where("user_id = ?", u.ID).First(&session).Error; err != nil {
		t.Fatalf("read login_sessions row: %v", err)
	}
	if session.RevokedAt == nil {
		t.Error("login_sessions.revoked_at is nil after logout")
	}
	if len(session.ID) != 64 {
		t.Errorf("login_sessions.id has length %d, want 64 (hashed login ID)", len(session.ID))
	}
	if r := acctJSON(t, h, http.MethodGet, "/api/auth/me", "", token); r.code != http.StatusUnauthorized {
		t.Errorf("logged-out JWT returned %d on /api/auth/me, want 401", r.code)
	}
}

func TestDevicesLoginRecordsLastLoginAt(t *testing.T) {
	h := newAccessHarness(t)
	u := acctCreateUser(t, h, acctUserSpec{Username: "devices-last-login", Role: "author", Password: acctPassword})
	if before := acctNullTime(t, h, "SELECT last_login_at FROM users WHERE id = ?", u.ID); before.Valid {
		t.Fatalf("last_login_at = %v before any login, want NULL", before.Time)
	}

	failed := acctCall(t, h, acctRemote(), http.MethodPost, "/auth/login", `{"username":"devices-last-login","password":"wrong-password"}`, "", map[string]string{"Content-Type": "application/json"})
	h.mustNoPanic(failed, "failed login")
	if failed.code != http.StatusUnauthorized {
		t.Errorf("wrong password login returned %d, want 401", failed.code)
	}
	if after := acctNullTime(t, h, "SELECT last_login_at FROM users WHERE id = ?", u.ID); after.Valid {
		t.Error("failed login set last_login_at")
	}

	start := time.Now().Add(-time.Minute)
	acctLogin(t, h, u.Username, acctPassword, devicesSafariUA, acctRemote())
	after := acctNullTime(t, h, "SELECT last_login_at FROM users WHERE id = ?", u.ID)
	if !after.Valid {
		t.Fatal("successful login did not set last_login_at")
	}
	if after.Time.Before(start) || after.Time.After(time.Now().Add(time.Minute)) {
		t.Errorf("last_login_at = %v, want about now", after.Time)
	}
	if n := acctCount(t, h, "SELECT COUNT(*) FROM login_sessions WHERE user_id = ?", u.ID); n != 1 {
		t.Errorf("login_sessions rows after one login = %d, want 1", n)
	}
}

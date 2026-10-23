package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

const oauthIdentityConcurrentCallbacksCount = 12

func TestOAuthIdentityNewGoogleAndGitHubAccountsKeepPublicLoginContract(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)

	for _, providerName := range []string{"google", "github"} {
		t.Run(providerName, func(t *testing.T) {
			provider := newOAuthTxnIntegrationProvider(providerName)
			provider.user = goth.User{UserID: "fixed-subject-" + providerName, Email: providerName + "@identity.example.test", Name: "Identity " + providerName, NickName: "identity-" + providerName, Provider: providerName}
			resetOAuthInputProviders(provider)
			before := accessUsers(t, h.db)

			entry := oauthIdentityBegin(t, h, providerName, "/admin/posts?tab=identity")
			callback := oauthIdentityCallback(h, entry, "new-account-"+providerName, echo.MIMEApplicationJSON)
			if callback.Code != http.StatusOK {
				t.Fatalf("%s JSON callback status=%d, want 200", providerName, callback.Code)
			}
			token, responseUserID := oauthIdentityJSONLogin(t, callback)
			if got := accessUsers(t, h.db); len(got) != len(before)+1 {
				t.Fatalf("%s callback changed users from %d to %d, want one new account", providerName, len(before), len(got))
			}
			created := accessUsers(t, h.db)[len(before)]
			if created.Role != "author" {
				t.Errorf("new %s account role=%q, want author", providerName, created.Role)
			}
			me := h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, "")
			if me.code != http.StatusOK {
				t.Fatalf("/api/auth/me status=%d, want 200", me.code)
			}
			var authenticated core.User
			if err := json.Unmarshal([]byte(me.body), &authenticated); err != nil {
				t.Fatalf("decode /api/auth/me: %v", err)
			}
			if authenticated.ID == 0 || authenticated.ID != responseUserID || authenticated.Email != providerName+"@identity.example.test" || authenticated.Provider != providerName {
				t.Errorf("/api/auth/me returned id=%d email=%q provider=%q, inconsistent with callback user id=%d", authenticated.ID, authenticated.Email, authenticated.Provider, responseUserID)
			}

			htmlEntry := oauthIdentityBegin(t, h, providerName, "/admin/posts?tab=identity")
			htmlCallback := oauthIdentityCallback(h, htmlEntry, "html-account-"+providerName, "text/html")
			if htmlCallback.Code != http.StatusFound || htmlCallback.Header().Get(echo.HeaderLocation) != "/admin/posts?tab=identity" {
				t.Errorf("%s HTML callback status/location=%d/%q, want 302 to the in-site destination", providerName, htmlCallback.Code, htmlCallback.Header().Get(echo.HeaderLocation))
			}
		})
	}
}

func TestOAuthIdentitySameProviderSubjectKeepsOriginalUserAcrossProfileChanges(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	for _, tc := range []struct {
		name     string
		email    string
		nameText string
	}{
		{name: "changed email and name", email: "changed@identity.example.test", nameText: "Changed Name"},
		{name: "missing email", email: "", nameText: "Another Name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := oauthTxnPrepareHarness(t)
			provider := newOAuthTxnIntegrationProvider("google")
			provider.user = goth.User{UserID: "same-fixed-subject", Email: "first@identity.example.test", Name: "First Name", NickName: "first-name", Provider: "google"}
			resetOAuthInputProviders(provider)
			first := oauthIdentityCallbackForEntry(t, h, provider, "first-profile")
			if first.Code != http.StatusOK {
				t.Fatalf("initial callback status=%d, want 200", first.Code)
			}
			initialUsers := accessUsers(t, h.db)
			if len(initialUsers) != 1 {
				t.Fatalf("initial callback created %d users, want one", len(initialUsers))
			}
			original := initialUsers[0]
			provider.mu.Lock()
			provider.user = goth.User{UserID: "same-fixed-subject", Email: tc.email, Name: tc.nameText, NickName: "changed-nickname", Provider: "google"}
			provider.mu.Unlock()
			callback := oauthIdentityCallbackForEntry(t, h, provider, "repeat-"+strings.ReplaceAll(tc.name, " ", "-"))
			if callback.Code != http.StatusOK {
				t.Errorf("same-subject callback status=%d, want 200", callback.Code)
				oauthIdentityAssertNoCredentials(t, callback)
			} else {
				token, responseUserID := oauthIdentityJSONLogin(t, callback)
				if responseUserID != original.ID {
					t.Errorf("same-subject callback user id=%d, want original id=%d", responseUserID, original.ID)
				}
				me := h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, "")
				var authenticated core.User
				if me.code != http.StatusOK || json.Unmarshal([]byte(me.body), &authenticated) != nil || authenticated.ID != original.ID {
					t.Errorf("same-subject token /api/auth/me returned status=%d id=%d, want original id=%d", me.code, authenticated.ID, original.ID)
				}
			}
			if got := accessUsers(t, h.db); !reflect.DeepEqual(initialUsers, got) {
				t.Errorf("same-subject login changed users; original id=%d", original.ID)
			}
		})
	}
}

func TestOAuthIdentityProviderNamespacesAllowSameSubjectAcrossProviders(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	google := newOAuthTxnIntegrationProvider("google")
	google.user = goth.User{UserID: "Subject-X", Email: "google-subject-x@identity.example.test", Name: "Google Subject", NickName: "google-subject", Provider: "google"}
	github := newOAuthTxnIntegrationProvider("github")
	github.user = goth.User{UserID: "Subject-X", Email: "github-subject-x@identity.example.test", Name: "GitHub Subject", NickName: "github-subject", Provider: "github"}
	resetOAuthInputProviders(google, github)
	before := accessUsers(t, h.db)

	googleCallback := oauthIdentityCallbackForEntry(t, h, google, "namespace-google")
	if googleCallback.Code != http.StatusOK {
		t.Fatalf("Google Subject-X callback returned %d, want 200", googleCallback.Code)
	}
	googleToken, googleResponseID := oauthIdentityJSONLogin(t, googleCallback)
	githubCallback := oauthIdentityCallbackForEntry(t, h, github, "namespace-github")
	if githubCallback.Code != http.StatusOK {
		t.Fatalf("GitHub Subject-X callback returned %d, want 200", githubCallback.Code)
	}
	githubToken, githubResponseID := oauthIdentityJSONLogin(t, githubCallback)
	if googleResponseID == githubResponseID {
		t.Errorf("same subject across providers returned same user id %d, want separate accounts", googleResponseID)
	}
	if users := accessUsers(t, h.db); len(users) != len(before)+2 {
		t.Errorf("cross-provider same subject created %d users, want two", len(users)-len(before))
	}
	for _, tc := range []struct {
		provider string
		token    string
		id       uint
		email    string
	}{
		{provider: "google", token: googleToken, id: googleResponseID, email: "google-subject-x@identity.example.test"},
		{provider: "github", token: githubToken, id: githubResponseID, email: "github-subject-x@identity.example.test"},
	} {
		me := h.request(http.MethodGet, "/api/auth/me", "", tc.token, echo.MIMEApplicationJSON, "")
		var user core.User
		if me.code != http.StatusOK || json.Unmarshal([]byte(me.body), &user) != nil || user.ID != tc.id || user.Provider != tc.provider || user.Email != tc.email {
			t.Errorf("%s token resolved status=%d id=%d provider=%q email=%q, want id=%d provider=%q email=%q", tc.provider, me.code, user.ID, user.Provider, user.Email, tc.id, tc.provider, tc.email)
		}
	}
}

func TestOAuthIdentityProviderNamespaceCollisionDoesNotLinkAccounts(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	google := newOAuthTxnIntegrationProvider("google")
	google.user = goth.User{UserID: "Subject-X", Email: "shared-subject-x@identity.example.test", Name: "Google Subject", NickName: "google-subject", Provider: "google"}
	github := newOAuthTxnIntegrationProvider("github")
	github.user = goth.User{UserID: "Subject-X", Email: "shared-subject-x@identity.example.test", Name: "GitHub Subject", NickName: "github-subject", Provider: "github"}
	resetOAuthInputProviders(google, github)
	first := oauthIdentityCallbackForEntry(t, h, google, "same-email-google")
	if first.Code != http.StatusOK {
		t.Fatalf("Google Subject-X callback returned %d, want 200", first.Code)
	}
	before := accessUsers(t, h.db)
	callback := oauthIdentityCallbackForEntry(t, h, github, "same-email-github")
	if callback.Code != http.StatusConflict {
		t.Errorf("GitHub Subject-X with Google's email returned %d, want 409", callback.Code)
	}
	oauthIdentityAssertNoCredentials(t, callback)
	if users := accessUsers(t, h.db); !reflect.DeepEqual(before, users) {
		t.Error("same-email cross-provider callback changed the existing account")
	}
}

func TestOAuthIdentityEmailCollisionDoesNotLogIntoExistingAccounts(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)

	for i, tc := range []struct {
		name     string
		provider string
		role     string
	}{
		{name: "local admin", provider: "local", role: "admin"},
		{name: "another OAuth provider", provider: "github", role: "author"},
		{name: "legacy OAuth row without subject", provider: "google", role: "author"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := core.User{Username: fmt.Sprintf("existing-%s-%s-%d", tc.provider, tc.role, i), Email: fmt.Sprintf("collision-%d@identity.example.test", i), Provider: tc.provider, Role: tc.role}
			if err := h.db.Create(&user).Error; err != nil {
				t.Fatalf("seed existing account: %v", err)
			}
			before := accessUsers(t, h.db)
			provider := newOAuthTxnIntegrationProvider("google")
			provider.user = goth.User{UserID: "new-fixed-subject", Email: user.Email, Name: "New Identity", NickName: "new-identity", Provider: "google"}
			resetOAuthInputProviders(provider)
			callback := oauthIdentityCallbackForEntry(t, h, provider, "email-collision")
			if callback.Code != http.StatusConflict {
				t.Errorf("new subject matching %s email returned %d, want 409", tc.name, callback.Code)
			}
			oauthIdentityAssertNoCredentials(t, callback)
			if got := accessUsers(t, h.db); !reflect.DeepEqual(before, got) {
				t.Errorf("email collision changed existing %s account", tc.name)
			}
		})
	}
}

func TestOAuthIdentityRejectsBlankSubjects(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	for i, subject := range []string{"", " \t\n"} {
		provider := newOAuthTxnIntegrationProvider("google")
		provider.user = goth.User{UserID: subject, Email: fmt.Sprintf("blank-subject-%d@identity.example.test", i), Name: "Blank Subject", Provider: "google"}
		resetOAuthInputProviders(provider)
		callback := oauthIdentityCallbackForEntry(t, h, provider, "blank-subject-"+fmt.Sprint(len(subject)))
		if callback.Code != http.StatusBadRequest {
			t.Errorf("blank UserID length %d returned %d, want 400", len(subject), callback.Code)
		}
		oauthIdentityAssertNoCredentials(t, callback)
	}
	if got := accessUsers(t, h.db); len(got) != 0 {
		t.Fatalf("blank subjects created %d users, want none", len(got))
	}

}

func TestOAuthIdentityRejectsCaseDistinctSubjects(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{UserID: "Subject", Email: "case-subject@identity.example.test", Name: "Original Case", NickName: "case-subject", Provider: "google"}
	resetOAuthInputProviders(provider)
	first := oauthIdentityCallbackForEntry(t, h, provider, "case-original-subject")
	if first.Code != http.StatusOK {
		t.Fatalf("initial case-sensitive subject returned %d, want 200", first.Code)
	}
	before := accessUsers(t, h.db)
	provider.mu.Lock()
	provider.user.UserID = "subject"
	provider.mu.Unlock()
	callback := oauthIdentityCallbackForEntry(t, h, provider, "case-different-subject")
	if callback.Code != http.StatusConflict {
		t.Errorf("case-distinct UserID with same email returned %d, want 409", callback.Code)
	}
	oauthIdentityAssertNoCredentials(t, callback)
	if got := accessUsers(t, h.db); !reflect.DeepEqual(before, got) {
		t.Error("case-distinct subject changed the existing account")
	}
}

func TestOAuthIdentityRejectsMissingEmailForNewSubject(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	for i, email := range []string{"", " \t\n"} {
		t.Run(fmt.Sprintf("missing-email-%d", i), func(t *testing.T) {
			h := oauthTxnPrepareHarness(t)
			provider := newOAuthTxnIntegrationProvider("google")
			provider.user = goth.User{UserID: "new-subject-without-email-" + fmt.Sprint(i), Email: email, Name: "No Email", Provider: "google"}
			resetOAuthInputProviders(provider)
			callback := oauthIdentityCallbackForEntry(t, h, provider, "missing-email-"+fmt.Sprint(i))
			if callback.Code != http.StatusBadRequest {
				t.Errorf("missing email returned %d, want 400", callback.Code)
			}
			oauthIdentityAssertNoCredentials(t, callback)
			if got := accessUsers(t, h.db); len(got) != 0 {
				t.Errorf("missing email created %d users, want none", len(got))
			}
		})
	}
}

func TestOAuthIdentityGeneratesSafeUniqueUsernames(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	cases := []struct {
		name, nickname, display string
	}{
		{name: "nickname collides with existing username", nickname: "taken-name", display: "Useful Display Name"},
		{name: "unicode nickname exceeds 50 runes", nickname: strings.Repeat("名", 51), display: "Fallback Display"},
		{name: "nickname and name are blank", nickname: " \t", display: " \n"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := oauthTxnPrepareHarness(t)
			reserved := core.User{Username: "taken-name", Email: "reserved@identity.example.test", Provider: "local", Role: "admin"}
			if err := h.db.Create(&reserved).Error; err != nil {
				t.Fatal("seed colliding username")
			}
			original := accessUsers(t, h.db)
			provider := newOAuthTxnIntegrationProvider("google")
			provider.user = goth.User{UserID: fmt.Sprintf("username-subject-%d", i), Email: fmt.Sprintf("username-%d@identity.example.test", i), NickName: tc.nickname, Name: tc.display, Provider: "google"}
			resetOAuthInputProviders(provider)
			callback := oauthIdentityCallbackForEntry(t, h, provider, fmt.Sprintf("username-%d", i))
			if callback.Code != http.StatusOK {
				t.Errorf("callback returned %d, want 200", callback.Code)
				oauthIdentityAssertNoCredentials(t, callback)
				return
			}
			var payload struct {
				User core.User `json:"user"`
			}
			if err := json.Unmarshal(callback.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode successful callback user: %v", err)
			}
			_, responseUserID := oauthIdentityJSONLogin(t, callback)
			if payload.User.ID != responseUserID {
				t.Errorf("OAuth JSON user id=%d, decoded login id=%d", payload.User.ID, responseUserID)
			}
			if strings.TrimSpace(payload.User.Username) == "" || utf8.RuneCountInString(payload.User.Username) > 50 || payload.User.Username == reserved.Username {
				t.Errorf("created username %q is blank, longer than 50 runes, or collides", payload.User.Username)
			}
			users := accessUsers(t, h.db)
			if len(users) != len(original)+1 {
				t.Errorf("username case created %d users, want one", len(users)-len(original))
			} else if users[len(original)].ID != responseUserID || payload.User.ID != users[len(original)].ID || payload.User.Email != fmt.Sprintf("username-%d@identity.example.test", i) || users[len(original)].Email != payload.User.Email {
				t.Errorf("callback user id=%d email=%q does not match newly created id=%d email=%q", payload.User.ID, payload.User.Email, users[len(original)].ID, users[len(original)].Email)
			}
			if len(users) < len(original) || !reflect.DeepEqual(original, users[:len(original)]) {
				t.Error("username generation changed the original account")
			}
		})
	}
}

func TestOAuthIdentityConcurrentCommitBetweenIdentityAndEmailChecks(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")

	for _, tc := range []struct {
		name         string
		pauseAtMiss  int64
		reserveAName bool
	}{
		{name: "winner commits after first identity miss", pauseAtMiss: 1},
		{name: "winner commits after rollback identity miss", pauseAtMiss: 2, reserveAName: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := oauthTxnPrepareHarness(t)
			profileA := goth.User{UserID: "commit-race-subject", Email: "commit-race@identity.example.test", Name: "Profile A", NickName: "profile-a", AvatarURL: "https://identity.example.test/a.png", Provider: "google"}
			profileB := goth.User{UserID: "commit-race-subject", Email: profileA.Email, Name: "Profile B", NickName: "profile-b", AvatarURL: "https://identity.example.test/b.png", Provider: "google"}
			if tc.reserveAName {
				profileA.NickName = "reserved-race-name"
				profileB.NickName = "winner-race-name"
				reserved := core.User{Username: profileA.NickName, Email: "reserved-race-name@identity.example.test", Provider: "local", Role: "editor"}
				if err := h.db.Create(&reserved).Error; err != nil {
					t.Fatalf("seed username reservation: %v", err)
				}
			}
			usersBefore := accessUsers(t, h.db)
			auditsBefore := oauthIdentityAuditCount(t, h.db)

			provider := newOAuthIdentityConcurrentProvider("google", []goth.User{profileA, profileB})
			resetOAuthInputProviders(provider)
			entryA := oauthIdentityBegin(t, h, "google", "/admin?race=a")
			entryB := oauthIdentityBegin(t, h, "google", "/admin?race=b")

			paused := make(chan struct{})
			release := make(chan struct{})
			var resultARec *httptest.ResponseRecorder
			var misses atomic.Int64
			var pauseOnce sync.Once
			const callbackName = "oauth_identity_pause_commit_race_after_mapping_miss"
			if err := h.db.Callback().Query().After("gorm:query").Register(callbackName, func(db *gorm.DB) {
				if db.Statement == nil || db.Statement.Table != "oauth_identities" || !errors.Is(db.Error, gorm.ErrRecordNotFound) {
					return
				}
				if misses.Add(1) != tc.pauseAtMiss {
					return
				}
				pauseOnce.Do(func() {
					close(paused)
					<-release
				})
			}); err != nil {
				t.Fatalf("register identity-miss scheduling callback: %v", err)
			}
			callbackRegistered := true
			callbackDone := make(chan struct{})
			released := false
			t.Cleanup(func() {
				if !released {
					close(release)
					released = true
				}
				select {
				case <-callbackDone:
				case <-time.After(10 * time.Second):
					t.Error("callback A did not finish within cleanup deadline")
				}
				if callbackRegistered {
					if err := h.db.Callback().Query().Remove(callbackName); err != nil {
						t.Errorf("remove identity-miss scheduling callback: %v", err)
					}
					callbackRegistered = false
				}
			})

			go func() {
				defer close(callbackDone)
				resultARec = oauthIdentityCallback(h, entryA, "commit-race-a", echo.MIMEApplicationJSON)
			}()
			select {
			case <-paused:
			case <-time.After(10 * time.Second):
				t.Fatalf("callback A did not pause after identity miss %d (observed %d misses)", tc.pauseAtMiss, misses.Load())
			}

			resultB := oauthIdentityCallback(h, entryB, "commit-race-b", echo.MIMEApplicationJSON)
			if resultB.Code != http.StatusOK {
				t.Fatalf("callback B returned %d while A was paused, want 200", resultB.Code)
			}
			tokenB, userIDB := oauthIdentityJSONLogin(t, resultB)
			mappingsAfterB := oauthIdentityActiveMappings(t, h.db)
			if len(mappingsAfterB) != 1 || mappingsAfterB[0].UserID != userIDB || mappingsAfterB[0].Provider != profileB.Provider || mappingsAfterB[0].Subject != profileB.UserID || !mappingsAfterB[0].UserExists {
				t.Fatalf("callback B did not commit its exact active identity mapping before A resumed: %+v", mappingsAfterB)
			}
			if !released {
				close(release)
				released = true
			}
			select {
			case <-callbackDone:
			case <-time.After(10 * time.Second):
				t.Fatal("callback A did not finish after release")
			}

			if resultARec.Code != http.StatusOK {
				t.Errorf("callback A returned %d after the same identity committed, want 200", resultARec.Code)
				oauthIdentityAssertNoCredentials(t, resultARec)
			}
			if len(accessUsers(t, h.db)) != len(usersBefore)+1 {
				t.Errorf("callbacks left %d users, want %d", len(accessUsers(t, h.db)), len(usersBefore)+1)
			}
			if tc.reserveAName {
				usersAfter := accessUsers(t, h.db)
				if !reflect.DeepEqual(usersBefore[0], usersAfter[0]) {
					t.Error("failed callback A changed the pre-existing username reservation")
				}
			}
			if got := oauthIdentityAuditCount(t, h.db); got != auditsBefore+1 {
				t.Errorf("race left %d identity audit rows, want exactly one winner row", got-auditsBefore)
			}
			oauthIdentityAssertIdentityCounts(t, h, len(usersBefore)+1, 1)
			persisted := core.User{}
			if err := h.db.First(&persisted, userIDB).Error; err != nil {
				t.Fatalf("load callback B winner user: %v", err)
			}
			if persisted.Email != profileB.Email || persisted.Username != profileB.NickName || persisted.AvatarURL != profileB.AvatarURL || persisted.Provider != profileB.Provider || persisted.Role != "author" {
				t.Errorf("persisted user differs from callback B profile: %+v", persisted)
			}
			meB := h.request(http.MethodGet, "/api/auth/me", "", tokenB, echo.MIMEApplicationJSON, "")
			var authenticatedB core.User
			if meB.code != http.StatusOK || json.Unmarshal([]byte(meB.body), &authenticatedB) != nil || authenticatedB.ID != userIDB || authenticatedB.Email != profileB.Email || authenticatedB.Username != profileB.NickName || authenticatedB.AvatarURL != profileB.AvatarURL {
				t.Errorf("callback B token /api/auth/me returned status=%d user=%+v, want B winner id=%d", meB.code, authenticatedB, userIDB)
			}
			if resultARec.Code == http.StatusOK {
				tokenA, userIDA := oauthIdentityJSONLogin(t, resultARec)
				if userIDA != userIDB {
					t.Errorf("callbacks resolved user ids %d and %d, want B's winner %d", userIDA, userIDB, userIDB)
				}
				meA := h.request(http.MethodGet, "/api/auth/me", "", tokenA, echo.MIMEApplicationJSON, "")
				var authenticatedA core.User
				if meA.code != http.StatusOK || json.Unmarshal([]byte(meA.body), &authenticatedA) != nil || authenticatedA.ID != userIDB || authenticatedA.Email != profileB.Email || authenticatedA.Username != profileB.NickName || authenticatedA.AvatarURL != profileB.AvatarURL {
					t.Errorf("callback A token /api/auth/me returned status=%d user=%+v, want B winner id=%d", meA.code, authenticatedA, userIDB)
				}
			}
		})
	}
}

func TestOAuthIdentityConcurrentSameSubjectDifferentProfiles(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	profiles := make([]goth.User, oauthIdentityConcurrentCallbacksCount)
	for i := range profiles {
		profiles[i] = goth.User{UserID: "parallel-same-subject", Email: fmt.Sprintf("parallel-profile-%02d@identity.example.test", i), Name: fmt.Sprintf("Profile %02d", i), NickName: fmt.Sprintf("profile-%02d", i), Provider: "google"}
	}
	fixture := oauthIdentityPrepareConcurrentFixture(t, "google", profiles)
	results := oauthIdentityConcurrentCallbacks(t, fixture, "same-subject-profile")
	oauthIdentityAssertConcurrentSuccesses(t, fixture, results, profiles, 1, true)
}

func TestOAuthIdentityConcurrentSameSubjectSameEmail(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	profiles := make([]goth.User, oauthIdentityConcurrentCallbacksCount)
	for i := range profiles {
		profiles[i] = goth.User{UserID: "parallel-same-subject", Email: "parallel-same-email@identity.example.test", Name: fmt.Sprintf("Profile %02d", i), NickName: fmt.Sprintf("profile-%02d", i), Provider: "google"}
	}
	fixture := oauthIdentityPrepareConcurrentFixture(t, "google", profiles)
	results := oauthIdentityConcurrentCallbacks(t, fixture, "same-subject-same-email")
	oauthIdentityAssertConcurrentSuccesses(t, fixture, results, profiles, 1, true)
}

func TestOAuthIdentityConcurrentDifferentSubjectsSameEmail(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	profiles := make([]goth.User, oauthIdentityConcurrentCallbacksCount)
	for i := range profiles {
		profiles[i] = goth.User{UserID: fmt.Sprintf("parallel-subject-%02d", i), Email: "parallel-contested-email@identity.example.test", Name: fmt.Sprintf("Profile %02d", i), NickName: fmt.Sprintf("profile-%02d", i), Provider: "google"}
	}
	fixture := oauthIdentityPrepareConcurrentFixture(t, "google", profiles)
	results := oauthIdentityConcurrentCallbacks(t, fixture, "different-subject-same-email")
	successIndexes := make([]int, 0, 1)
	conflicts := 0
	for i, result := range results {
		switch result.Code {
		case http.StatusOK:
			successIndexes = append(successIndexes, i)
		case http.StatusConflict:
			conflicts++
			oauthIdentityAssertNoCredentials(t, result)
		default:
			t.Errorf("callback %d for distinct subject returned %d, want 200 winner or 409 conflict", i, result.Code)
			oauthIdentityAssertNoCredentials(t, result)
		}
	}
	if len(successIndexes) != 1 || conflicts != oauthIdentityConcurrentCallbacksCount-1 {
		t.Errorf("different-subject/same-email outcomes: successes=%d conflicts=%d, want 1/%d", len(successIndexes), conflicts, oauthIdentityConcurrentCallbacksCount-1)
	}
	if len(successIndexes) == 1 {
		winner := successIndexes[0]
		oauthIdentityAssertConcurrentSuccesses(t, fixture, results, profiles, 1, false, winner)
	} else {
		oauthIdentityAssertIdentityCounts(t, fixture.h, 1, 1)
	}
}

func TestOAuthIdentityConcurrentDifferentSubjectsSameNickname(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	profiles := make([]goth.User, oauthIdentityConcurrentCallbacksCount)
	for i := range profiles {
		profiles[i] = goth.User{UserID: fmt.Sprintf("parallel-subject-%02d", i), Email: fmt.Sprintf("parallel-email-%02d@identity.example.test", i), Name: "Shared Nickname", NickName: "shared-nickname", Provider: "google"}
	}
	fixture := oauthIdentityPrepareConcurrentFixture(t, "google", profiles)
	results := oauthIdentityConcurrentCallbacks(t, fixture, "different-subject-same-nickname")
	oauthIdentityAssertConcurrentSuccesses(t, fixture, results, profiles, oauthIdentityConcurrentCallbacksCount, false)
	users := accessUsers(t, fixture.h.db)
	if len(users) != oauthIdentityConcurrentCallbacksCount {
		return
	}
	seenUsernames := make(map[string]bool, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.Username) == "" || utf8.RuneCountInString(user.Username) > 50 || seenUsernames[user.Username] {
			t.Errorf("concurrent account username %q is blank, over 50 runes, or duplicated", user.Username)
		}
		seenUsernames[user.Username] = true
	}
}

func TestOAuthIdentityUserAndIdentityInsertFailureRollsBackTogether(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	const triggerName = "oauth_identity_test_fail_before_insert"
	const functionName = "jelly_test_fail_oauth_identity_insert"
	const triggerMessage = "fake-secret-identity-write-failure"
	if err := h.db.Exec(`CREATE FUNCTION ` + functionName + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION '` + triggerMessage + `'; END; $$`).Error; err != nil {
		t.Fatalf("create identity failure trigger function: %v", err)
	}
	if err := h.db.Exec(`CREATE TRIGGER ` + triggerName + ` BEFORE INSERT ON oauth_identities FOR EACH ROW EXECUTE FUNCTION ` + functionName + `()`).Error; err != nil {
		t.Fatalf("create identity failure trigger: %v", err)
	}
	triggerInstalled := true
	t.Cleanup(func() {
		if triggerInstalled {
			if err := h.db.Exec(`DROP TRIGGER IF EXISTS ` + triggerName + ` ON oauth_identities`).Error; err != nil {
				t.Errorf("drop identity failure trigger during cleanup: %v", err)
			}
			if err := h.db.Exec(`DROP FUNCTION IF EXISTS ` + functionName + `()`).Error; err != nil {
				t.Errorf("drop identity failure function during cleanup: %v", err)
			}
		}
	})

	// Verify the test-owned database trigger raises for identity writes before
	// using it as the HTTP transaction failure seam. Roll back the probe entirely.
	probe := h.db.Begin()
	if probe.Error != nil {
		t.Fatalf("begin identity trigger probe: %v", probe.Error)
	}
	probeUser := core.User{Username: "identity-trigger-probe", Email: "identity-trigger-probe@identity.example.test", Provider: "local", Role: "author"}
	if err := probe.Create(&probeUser).Error; err != nil {
		_ = probe.Rollback().Error
		t.Fatalf("create identity trigger probe user: %v", err)
	}
	probeErr := probe.Create(&core.OAuthIdentity{UserID: probeUser.ID, Provider: "google", Subject: "trigger-probe-subject"}).Error
	if probeErr == nil || !strings.Contains(probeErr.Error(), triggerMessage) {
		_ = probe.Rollback().Error
		t.Fatal("test trigger did not reject an identity insert with its private marker")
	}
	if err := probe.Rollback().Error; err != nil {
		t.Fatalf("roll back identity trigger probe: %v", err)
	}

	usersBefore := accessUsers(t, h.db)
	identitiesBefore := oauthIdentityActiveMappings(t, h.db)
	auditsBefore := oauthIdentityAuditCount(t, h.db)
	rollbackProfile := goth.User{UserID: "rollback-subject", Email: "rollback@identity.example.test", Name: "Rollback", NickName: "rollback", Provider: "google"}
	provider := newOAuthIdentityConcurrentProvider("google", []goth.User{rollbackProfile, rollbackProfile})
	resetOAuthInputProviders(provider)
	entry := oauthIdentityBegin(t, h, "google", "/admin")
	failed := oauthIdentityCallback(h, entry, "identity-write-fails", echo.MIMEApplicationJSON)
	if failed.Code != http.StatusServiceUnavailable {
		t.Errorf("identity insert failure callback returned %d, want 503", failed.Code)
	}
	if strings.Contains(failed.Body.String(), triggerMessage) {
		t.Error("identity insert failure response exposed the database trigger error")
	}
	oauthIdentityAssertNoCredentials(t, failed)
	if got := accessUsers(t, h.db); !reflect.DeepEqual(usersBefore, got) {
		t.Error("failed identity insert left a new or changed user")
	}
	if got := oauthIdentityActiveMappings(t, h.db); !reflect.DeepEqual(identitiesBefore, got) {
		t.Error("failed identity insert left a mapping")
	}
	if got := oauthIdentityAuditCount(t, h.db); got != auditsBefore {
		t.Errorf("failed identity insert left %d audit rows, want unchanged count %d", got, auditsBefore)
	}

	if err := h.db.Exec(`DROP TRIGGER IF EXISTS ` + triggerName + ` ON oauth_identities`).Error; err != nil {
		t.Fatalf("remove identity failure trigger: %v", err)
	}
	if err := h.db.Exec(`DROP FUNCTION IF EXISTS ` + functionName + `()`).Error; err != nil {
		t.Fatalf("remove identity failure function: %v", err)
	}
	triggerInstalled = false
	retryEntry := oauthIdentityBegin(t, h, "google", "/admin")
	retry := oauthIdentityCallback(h, retryEntry, "identity-write-retry", echo.MIMEApplicationJSON)
	if retry.Code != http.StatusOK {
		t.Errorf("fresh OAuth state after removing identity trigger returned %d, want 200", retry.Code)
		oauthIdentityAssertNoCredentials(t, retry)
	} else {
		token, userID := oauthIdentityJSONLogin(t, retry)
		me := h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, "")
		var authenticated core.User
		if me.code != http.StatusOK || json.Unmarshal([]byte(me.body), &authenticated) != nil || authenticated.ID != userID || authenticated.Email != rollbackProfile.Email || authenticated.Provider != rollbackProfile.Provider {
			t.Errorf("successful retry /api/auth/me status=%d id=%d email=%q provider=%q, want id=%d email=%q provider=%q", me.code, authenticated.ID, authenticated.Email, authenticated.Provider, userID, rollbackProfile.Email, rollbackProfile.Provider)
		}
		usersAfter := accessUsers(t, h.db)
		mappingsAfter := oauthIdentityActiveMappings(t, h.db)
		if len(usersAfter) != len(usersBefore)+1 {
			t.Errorf("successful retry created %d users, want one", len(usersAfter)-len(usersBefore))
		}
		if len(mappingsAfter) != len(identitiesBefore)+1 {
			t.Errorf("successful retry created %d active identities, want one", len(mappingsAfter)-len(identitiesBefore))
		}
		if got := oauthIdentityAuditCount(t, h.db); got != auditsBefore+1 {
			t.Errorf("successful retry created %d audit rows, want one", got-auditsBefore)
		}
		if len(usersAfter) > len(usersBefore) && len(mappingsAfter) > len(identitiesBefore) {
			mapping := mappingsAfter[len(identitiesBefore)]
			if mapping.UserID != userID || mapping.Provider != "google" || mapping.Subject != "rollback-subject" || !mapping.UserExists {
				t.Errorf("retry returned user %d with mapping user=%d provider=%q subject=%q exists=%v", userID, mapping.UserID, mapping.Provider, mapping.Subject, mapping.UserExists)
			}
		}
	}
}

type oauthIdentityConcurrentFixture struct {
	h        *accessHarness
	provider *oauthIdentityConcurrentProvider
	entries  []oauthHTTPEntry
	profiles []goth.User
}

func oauthIdentityPrepareConcurrentFixture(t *testing.T, providerName string, profiles []goth.User) *oauthIdentityConcurrentFixture {
	t.Helper()
	if len(profiles) != oauthIdentityConcurrentCallbacksCount {
		t.Fatalf("concurrent fixture has %d profiles, want %d", len(profiles), oauthIdentityConcurrentCallbacksCount)
	}
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthIdentityConcurrentProvider(providerName, profiles)
	resetOAuthInputProviders(provider)
	entries := make([]oauthHTTPEntry, len(profiles))
	for i := range profiles {
		entries[i] = oauthIdentityBegin(t, h, providerName, "/admin?parallel="+fmt.Sprint(i))
	}
	return &oauthIdentityConcurrentFixture{h: h, provider: provider, entries: entries, profiles: append([]goth.User(nil), profiles...)}
}

func oauthIdentityConcurrentCallbacks(t *testing.T, fixture *oauthIdentityConcurrentFixture, codePrefix string) []*httptest.ResponseRecorder {
	t.Helper()
	results := make([]*httptest.ResponseRecorder, len(fixture.entries))
	ready := make(chan struct{}, len(fixture.entries))
	start := make(chan struct{})
	var callbacks sync.WaitGroup
	for i := range fixture.entries {
		callbacks.Add(1)
		go func(index int) {
			defer callbacks.Done()
			ready <- struct{}{}
			<-start
			results[index] = oauthIdentityCallback(fixture.h, fixture.entries[index], fmt.Sprintf("%s-%02d", codePrefix, index), echo.MIMEApplicationJSON)
		}(i)
	}
	for range fixture.entries {
		<-ready
	}
	close(start)
	callbacks.Wait()
	return results
}

func oauthIdentityAssertConcurrentSuccesses(t *testing.T, fixture *oauthIdentityConcurrentFixture, results []*httptest.ResponseRecorder, profiles []goth.User, expectedUsers int, requireSameUser bool, onlyIndexes ...int) {
	t.Helper()
	selected := onlyIndexes
	if len(selected) == 0 {
		selected = make([]int, len(results))
		for i := range selected {
			selected[i] = i
		}
	}
	users := accessUsers(t, fixture.h.db)
	if len(users) != expectedUsers {
		t.Errorf("concurrent callbacks left %d users, want %d", len(users), expectedUsers)
	}
	expectedMappings := make([]oauthIdentityExpectedMapping, 0, len(selected))
	var firstID uint
	for _, i := range selected {
		result := results[i]
		if result == nil || result.Code != http.StatusOK {
			gotStatus := 0
			if result != nil {
				gotStatus = result.Code
			}
			t.Errorf("callback %d returned %d, want 200", i, gotStatus)
			if result != nil {
				oauthIdentityAssertNoCredentials(t, result)
			}
			continue
		}
		token, responseID := oauthIdentityJSONLogin(t, result)
		if firstID == 0 {
			firstID = responseID
		} else if requireSameUser && responseID != firstID {
			t.Errorf("callback %d resolved id=%d, want shared id=%d", i, responseID, firstID)
		}
		var callbackPayload struct {
			User core.User `json:"user"`
		}
		if err := json.Unmarshal(result.Body.Bytes(), &callbackPayload); err != nil {
			t.Fatalf("decode callback %d user: %v", i, err)
		}
		me := fixture.h.request(http.MethodGet, "/api/auth/me", "", token, echo.MIMEApplicationJSON, "")
		var authenticated core.User
		if me.code != http.StatusOK || json.Unmarshal([]byte(me.body), &authenticated) != nil || authenticated.ID != responseID || callbackPayload.User.ID != responseID || callbackPayload.User.ID != authenticated.ID || callbackPayload.User.Email != authenticated.Email || callbackPayload.User.Provider != authenticated.Provider || authenticated.Provider != profiles[i].Provider {
			t.Errorf("callback %d token resolved /api/auth/me status=%d id=%d provider=%q email=%q; expected id=%d provider=%q email=%q", i, me.code, authenticated.ID, authenticated.Provider, authenticated.Email, responseID, profiles[i].Provider, profiles[i].Email)
		}
		if requireSameUser {
			var persisted core.User
			if err := fixture.h.db.First(&persisted, firstID).Error; err != nil {
				t.Errorf("load shared user id=%d: %v", firstID, err)
			} else if authenticated.ID != persisted.ID || authenticated.Email != persisted.Email || authenticated.Username != persisted.Username || authenticated.Provider != persisted.Provider || authenticated.Role != persisted.Role || callbackPayload.User.ID != persisted.ID || callbackPayload.User.Email != persisted.Email || callbackPayload.User.Username != persisted.Username || callbackPayload.User.Provider != persisted.Provider || callbackPayload.User.Role != persisted.Role {
				t.Errorf("callback %d returned a different user profile than persisted shared id=%d", i, persisted.ID)
			} else {
				profileEmailFound := false
				for _, profile := range profiles {
					if profile.Provider == persisted.Provider && profile.UserID == profiles[i].UserID && profile.Email == persisted.Email {
						profileEmailFound = true
						break
					}
				}
				if !profileEmailFound {
					t.Errorf("persisted shared email %q did not come from any profile for subject %q", persisted.Email, profiles[i].UserID)
				}
			}
		} else if authenticated.Email != profiles[i].Email {
			t.Errorf("callback %d resolved email=%q, want its distinct profile email %q", i, authenticated.Email, profiles[i].Email)
		}
		mappingKey := profiles[i].Provider + "\x00" + profiles[i].UserID
		alreadyExpected := false
		for _, expected := range expectedMappings {
			if expected.Provider+"\x00"+expected.Subject == mappingKey {
				alreadyExpected = true
				if expected.UserID != responseID {
					t.Errorf("same provider/subject resolved to users %d and %d", expected.UserID, responseID)
				}
				break
			}
		}
		if !alreadyExpected {
			expectedMappings = append(expectedMappings, oauthIdentityExpectedMapping{UserID: responseID, Provider: profiles[i].Provider, Subject: profiles[i].UserID})
		}
	}
	oauthIdentityAssertIdentityCounts(t, fixture.h, expectedUsers, expectedUsers)
	actualMappings := oauthIdentityActiveMappings(t, fixture.h.db)
	if len(actualMappings) != expectedUsers {
		t.Errorf("active mapping count=%d, want %d", len(actualMappings), expectedUsers)
	}
	for _, expected := range expectedMappings {
		found := false
		for _, actual := range actualMappings {
			if actual.UserID == expected.UserID && actual.Provider == expected.Provider && actual.Subject == expected.Subject && actual.UserExists {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no joined active mapping for user=%d provider=%q subject=%q", expected.UserID, expected.Provider, expected.Subject)
		}
	}
}

func oauthIdentityAssertIdentityCounts(t *testing.T, h *accessHarness, expectedUsers, expectedMappings int) {
	t.Helper()
	if users := accessUsers(t, h.db); len(users) != expectedUsers {
		t.Errorf("user count=%d, want %d", len(users), expectedUsers)
	}
	mappings := oauthIdentityActiveMappings(t, h.db)
	if len(mappings) != expectedMappings {
		t.Errorf("active identity mapping count=%d, want %d", len(mappings), expectedMappings)
	}
	for _, mapping := range mappings {
		if !mapping.UserExists {
			t.Errorf("active identity for user=%d provider=%q has no joined user", mapping.UserID, mapping.Provider)
		}
	}
}

func oauthIdentityActiveMappings(t *testing.T, db *gorm.DB) []oauthIdentityMappingRow {
	t.Helper()
	var mappings []oauthIdentityMappingRow
	err := db.Raw(`SELECT i.user_id, i.provider, i.subject, (u.id IS NOT NULL) AS user_exists
		FROM oauth_identities AS i LEFT JOIN users AS u ON u.id = i.user_id
		WHERE i.deleted_at IS NULL ORDER BY i.user_id, i.provider, i.subject`).Scan(&mappings).Error
	if err != nil {
		t.Fatalf("query active OAuth identity mappings: %v", err)
	}
	return mappings
}

func oauthIdentityAuditCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Raw("SELECT count(*) FROM oauth_identity_audit_log").Scan(&count).Error; err != nil {
		t.Fatalf("count OAuth identity audit rows: %v", err)
	}
	return count
}

type oauthIdentityExpectedMapping struct {
	UserID   uint
	Provider string
	Subject  string
}

type oauthIdentityMappingRow struct {
	UserID     uint
	Provider   string
	Subject    string
	UserExists bool
}

type oauthIdentityConcurrentProvider struct {
	name     string
	mu       sync.Mutex
	profiles []goth.User
	next     int
}

func newOAuthIdentityConcurrentProvider(name string, profiles []goth.User) *oauthIdentityConcurrentProvider {
	return &oauthIdentityConcurrentProvider{name: name, profiles: append([]goth.User(nil), profiles...)}
}

func (p *oauthIdentityConcurrentProvider) Name() string { return p.name }
func (*oauthIdentityConcurrentProvider) SetName(string) {}
func (p *oauthIdentityConcurrentProvider) BeginAuth(state string) (goth.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next >= len(p.profiles) {
		return nil, errors.New("parallel OAuth fixture has no profile for entry")
	}
	profile := p.profiles[p.next]
	p.next++
	return &oauthIdentityConcurrentSession{State: state, Profile: profile}, nil
}
func (*oauthIdentityConcurrentProvider) UnmarshalSession(raw string) (goth.Session, error) {
	var session oauthIdentityConcurrentSession
	err := json.Unmarshal([]byte(raw), &session)
	return &session, err
}
func (p *oauthIdentityConcurrentProvider) FetchUser(session goth.Session) (goth.User, error) {
	fixture, ok := session.(*oauthIdentityConcurrentSession)
	if !ok || fixture.AccessToken == "" || fixture.Profile.Provider != p.name {
		return goth.User{}, errors.New("parallel OAuth fixture session is invalid")
	}
	return fixture.Profile, nil
}
func (*oauthIdentityConcurrentProvider) Debug(bool)                                 {}
func (*oauthIdentityConcurrentProvider) RefreshToken(string) (*oauth2.Token, error) { return nil, nil }
func (*oauthIdentityConcurrentProvider) RefreshTokenAvailable() bool                { return false }

type oauthIdentityConcurrentSession struct {
	State       string    `json:"state"`
	AccessToken string    `json:"access_token,omitempty"`
	Profile     goth.User `json:"profile"`
}

func (s *oauthIdentityConcurrentSession) GetAuthURL() (string, error) {
	return "https://provider.invalid/authorize?" + url.Values{"state": {s.State}}.Encode(), nil
}
func (s *oauthIdentityConcurrentSession) Marshal() string {
	encoded, _ := json.Marshal(s)
	return string(encoded)
}
func (s *oauthIdentityConcurrentSession) Authorize(_ goth.Provider, params goth.Params) (string, error) {
	if strings.TrimSpace(params.Get("code")) == "" {
		return "", errors.New("parallel OAuth fixture authorization code is blank")
	}
	s.AccessToken = "parallel-token:" + params.Get("code")
	return s.AccessToken, nil
}

func TestOAuthIdentityAccountLookupFailureReturnsUnavailableWithoutCreatingAccount(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	user := core.User{Username: "lookup-admin", Email: "lookup-admin@identity.example.test", Provider: "local", Role: "admin"}
	if err := h.db.Create(&user).Error; err != nil {
		t.Fatal("seed account before query failure")
	}
	before := accessUsers(t, h.db)
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{UserID: "lookup-subject", Email: "new@identity.example.test", Name: "New Account", NickName: "new-account", Provider: "google"}
	resetOAuthInputProviders(provider)
	entry := oauthIdentityBegin(t, h, "google", "/admin")
	const callbackName = "oauth_identity_force_account_query_error"
	var failedQueries atomic.Int64
	if err := h.db.Callback().Query().Before("gorm:query").Register(callbackName, func(db *gorm.DB) {
		failedQueries.Add(1)
		db.AddError(errors.New("account lookup fixture fakeSecret-should-not-leak"))
	}); err != nil {
		t.Fatalf("register account-query failure callback: %v", err)
	}
	removed := false
	t.Cleanup(func() {
		if !removed {
			if err := h.db.Callback().Query().Remove(callbackName); err != nil {
				t.Errorf("remove account-query failure callback during cleanup: %v", err)
			}
		}
	})
	callback := oauthIdentityCallback(h, entry, "lookup-failure", echo.MIMEApplicationJSON)
	if err := h.db.Callback().Query().Remove(callbackName); err != nil {
		t.Fatalf("remove users-query failure callback: %v", err)
	}
	removed = true
	if failedQueries.Load() == 0 {
		t.Fatal("account-lookup fault injection did not intercept a GORM query")
	}
	if callback.Code != http.StatusServiceUnavailable {
		t.Errorf("OAuth callback after users-query failure returned %d, want 503", callback.Code)
	}
	if strings.Contains(callback.Body.String(), "fakeSecret-should-not-leak") {
		t.Error("users-query failure response exposed the database error")
	}
	oauthIdentityAssertNoCredentials(t, callback)
	if got := accessUsers(t, h.db); !reflect.DeepEqual(before, got) {
		t.Error("users-query failure was treated as not-found and changed users")
	}
}

func oauthIdentityBegin(t *testing.T, h *accessHarness, providerName, redirect string) oauthHTTPEntry {
	t.Helper()
	entry := oauthTxnServe(h, http.MethodGet, "/auth/"+providerName+"?redirect="+url.QueryEscape(redirect), nil)
	if entry.Code != http.StatusTemporaryRedirect {
		t.Fatalf("OAuth entry returned %d, want 307", entry.Code)
	}
	location, err := url.Parse(entry.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	gothCookie := oauthTxnResponseCookie(entry, gothic.SessionName)
	appCookie := oauthTxnResponseCookie(entry, "jelly-cms-session")
	state := location.Query().Get("state")
	if state == "" || gothCookie == nil || appCookie == nil {
		t.Fatal("OAuth entry omitted state or signed cookies")
	}
	return oauthHTTPEntry{provider: providerName, state: state, gothCookie: gothCookie, appCookie: appCookie}
}

func oauthIdentityCallbackForEntry(t *testing.T, h *accessHarness, provider *oauthTxnIntegrationProvider, code string) *httptest.ResponseRecorder {
	t.Helper()
	entry := oauthIdentityBegin(t, h, provider.Name(), "/admin")
	return oauthIdentityCallback(h, entry, code, echo.MIMEApplicationJSON)
}

func oauthIdentityCallback(h *accessHarness, entry oauthHTTPEntry, code, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/"+entry.provider+"/callback?state="+url.QueryEscape(entry.state)+"&code="+url.QueryEscape(code), nil)
	req.Header.Set(echo.HeaderAccept, accept)
	req.AddCookie(entry.gothCookie)
	req.AddCookie(entry.appCookie)
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func oauthIdentityJSONLogin(t *testing.T, rec *httptest.ResponseRecorder) (string, uint) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode callback JSON: %v", err)
	}
	if len(payload) != 3 || payload["message"] == nil || payload["user"] == nil || payload["token"] == nil {
		t.Fatalf("OAuth JSON response must contain exactly message, user, token")
	}
	var token string
	var user core.User
	if err := json.Unmarshal(payload["token"], &token); err != nil || token == "" {
		t.Fatal("OAuth success response omitted token")
	}
	if err := json.Unmarshal(payload["user"], &user); err != nil || user.ID == 0 {
		t.Fatal("OAuth success response omitted user identity")
	}
	return token, user.ID
}

func oauthIdentityAssertNoCredentials(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "jwt_token" || cookie.Name == "jelly-cms-session" {
			t.Errorf("failed OAuth callback issued a login credential cookie named %q", cookie.Name)
		}
	}
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Error("failed OAuth callback response included a token")
	}
}

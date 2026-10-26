package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/oauth2"
)

const oauthRecoveryJWTSecret = "oauth-recovery-integration-jwt-secret-0123456789"

func TestOAuthRecoveryGrantFormatAndEntryBodyOnly(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "admin")
	grant := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), nil)
	provider := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(provider)

	for _, tc := range []struct {
		name, contentType, body, query, accept string
		wantStatus                             int
	}{
		{name: "JSON body", contentType: echo.MIMEApplicationJSON, body: `{"grant":"` + grant + `"}`, accept: echo.MIMEApplicationJSON, wantStatus: http.StatusOK},
		{name: "form body", contentType: echo.MIMEApplicationForm, body: "grant=" + url.QueryEscape(grant), accept: "text/html", wantStatus: http.StatusSeeOther},
		{name: "query only", contentType: echo.MIMEApplicationJSON, query: "?grant=" + url.QueryEscape(grant), body: `{}`, accept: echo.MIMEApplicationJSON, wantStatus: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/recover"+tc.query, strings.NewReader(tc.body))
			req.Header.Set(echo.HeaderContentType, tc.contentType)
			req.Header.Set(echo.HeaderAccept, tc.accept)
			req.Header.Set("X-Jelly-CSRF", "1")
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("recovery entry status=%d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), grant) || strings.Contains(rec.Header().Get(echo.HeaderLocation), grant) {
				t.Error("recovery grant was reflected in response or Location")
			}
			if tc.wantStatus == http.StatusSeeOther && (rec.Code == http.StatusTemporaryRedirect || rec.Code == http.StatusPermanentRedirect) {
				t.Errorf("form recovery used redirect status %d, which preserves POST body", rec.Code)
			}
			if tc.name == "JSON body" && rec.Code == http.StatusOK {
				var payload map[string]string
				if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload["auth_url"] == "" {
					t.Errorf("JSON entry response lacks auth_url: %s", rec.Body.String())
				}
			}
		})
	}
}

func TestOAuthRecoveryRestoresLegacyUserAndConsumesGrantOnce(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "admin")
	article := core.Post{Title: "Legacy article", Slug: "oauth-recovery-legacy-article", Content: "owned before recovery", Type: "post", Status: "published", AuthorID: legacy.ID}
	if err := h.db.Create(&article).Error; err != nil {
		t.Fatalf("create legacy article: %v", err)
	}
	grant := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), nil)
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "stable-google-subject", Email: "changed-email@example.test", Name: "Changed Profile"}
	resetOAuthInputProviders(provider)

	entry := oauthRecoveryPostEntry(h, grant, "application/json")
	if entry.Code != http.StatusOK {
		t.Fatalf("valid recovery entry returned %d; new recovery route/migration unavailable: %s", entry.Code, entry.Body.String())
	}
	var entryBody struct {
		AuthURL string `json:"auth_url"`
	}
	if err := json.Unmarshal(entry.Body.Bytes(), &entryBody); err != nil || entryBody.AuthURL == "" {
		t.Fatalf("decode recovery entry: auth_url=%q err=%v body=%s", entryBody.AuthURL, err, entry.Body.String())
	}
	entryURL, err := url.Parse(entryBody.AuthURL)
	if err != nil {
		t.Fatalf("parse auth_url: %v", err)
	}
	state := entryURL.Query().Get("state")
	gothCookie := oauthRecoveryResponseCookie(entry, gothic.SessionName)
	if state == "" || gothCookie == nil {
		t.Fatal("recovery entry omitted OAuth state or signed Goth cookie")
	}
	callback := httptest.NewRequest(http.MethodGet, "/auth/google/callback?state="+url.QueryEscape(state)+"&code=recovery-success", nil)
	callback.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	callback.AddCookie(gothCookie)
	callbackResponse := httptest.NewRecorder()
	h.echo.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != http.StatusOK {
		t.Fatalf("recovery callback returned %d: %s", callbackResponse.Code, callbackResponse.Body.String())
	}
	var login struct {
		Token string    `json:"token"`
		User  core.User `json:"user"`
	}
	if err := json.Unmarshal(callbackResponse.Body.Bytes(), &login); err != nil || login.Token == "" || login.User.ID != legacy.ID {
		t.Fatalf("recovery did not authenticate original user %d: token=%t user=%+v err=%v", legacy.ID, login.Token != "", login.User, err)
	}
	me := h.request(http.MethodGet, "/api/auth/me", "", login.Token, echo.MIMEApplicationJSON, "")
	if me.code != http.StatusOK {
		t.Fatalf("recovered token /api/auth/me returned %d: %s", me.code, me.body)
	}
	var meUser core.User
	if err := json.Unmarshal([]byte(me.body), &meUser); err != nil || meUser.ID != legacy.ID || meUser.Role != legacy.Role {
		t.Errorf("/api/auth/me identity=%+v err=%v, want id=%d role=%q", meUser, err, legacy.ID, legacy.Role)
	}
	if grantMe := h.request(http.MethodGet, "/api/auth/me", "", grant, echo.MIMEApplicationJSON, ""); grantMe.code == http.StatusOK {
		t.Error("recovery grant was accepted as an application bearer token")
	}
	var after core.User
	if err := h.db.First(&after, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	// A successful recovery login records login activity; every other legacy
	// account field must remain unchanged.
	legacy.LastLoginAt, after.LastLoginAt = nil, nil
	legacy.UpdatedAt, after.UpdatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(legacy, after) {
		t.Errorf("recovery altered legacy account fields: before=%+v after=%+v", legacy, after)
	}
	var afterArticle core.Post
	if err := h.db.First(&afterArticle, article.ID).Error; err != nil || afterArticle.AuthorID != legacy.ID {
		t.Errorf("recovery changed article ownership: author=%d err=%v", afterArticle.AuthorID, err)
	}
	var users int64
	if err := h.db.Model(&core.User{}).Count(&users).Error; err != nil || users != 1 {
		t.Errorf("recovery user count=%d err=%v, want exactly one", users, err)
	}
	var mapping struct {
		UserID            uint
		Provider, Subject string
	}
	if err := h.db.Table("oauth_identities").Select("user_id, provider, subject").Where("provider = ? AND subject = ? AND deleted_at IS NULL", "google", "stable-google-subject").Take(&mapping).Error; err != nil {
		t.Fatalf("read created identity mapping: %v", err)
	}
	if mapping.UserID != legacy.ID || mapping.Provider != "google" || mapping.Subject != "stable-google-subject" {
		t.Errorf("identity mapping=%+v, want original user/provider/subject", mapping)
	}
	oauthRecoveryAssertConsumptionAndAudit(t, h, grant, mapping)

	replay := oauthRecoveryPostEntry(h, grant, "application/json")
	if replay.Code != http.StatusConflict {
		t.Errorf("consumed grant replay status=%d, want 409: %s", replay.Code, replay.Body.String())
	}
	callbackReplay := oauthRecoveryCallback(h, "google", oauthRecoveryPending{state: state, cookie: gothCookie}, "recovery-second-code", nil, echo.MIMEApplicationJSON)
	if callbackReplay.Code != http.StatusBadRequest && callbackReplay.Code != http.StatusConflict {
		t.Errorf("consumed recovery callback replay status=%d, want 400 or 409: %s", callbackReplay.Code, callbackReplay.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, callbackReplay)

	// Once linked, a normal OAuth login with the same provider subject but a
	// changed email must continue to resolve to the original legacy account.
	normalEntry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	if normalEntry.Code != http.StatusTemporaryRedirect {
		t.Fatalf("normal OAuth re-entry returned %d: %s", normalEntry.Code, normalEntry.Body.String())
	}
	normalURL, err := url.Parse(normalEntry.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatal(err)
	}
	normal := oauthRecoveryCallback(h, "google", oauthRecoveryPending{state: normalURL.Query().Get("state"), cookie: oauthRecoveryResponseCookie(normalEntry, gothic.SessionName)}, "normal-same-subject-new-email", nil, echo.MIMEApplicationJSON)
	if normal.Code != http.StatusOK {
		t.Errorf("normal OAuth login after recovery returned %d: %s", normal.Code, normal.Body.String())
	} else {
		var normalPayload struct {
			User core.User `json:"user"`
		}
		if err := json.Unmarshal(normal.Body.Bytes(), &normalPayload); err != nil || normalPayload.User.ID != legacy.ID {
			t.Errorf("normal OAuth after recovery resolved user=%d, want original %d err=%v", normalPayload.User.ID, legacy.ID, err)
		}
	}
}

func TestOAuthRecoveryRejectsMalformedAndWrongPurposeGrants(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	provider := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(provider)
	valid := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), nil)
	wrongPurpose := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), map[string]any{"purpose": "login"})
	ordinaryLoginJWT, err := auth.GenerateJWT(legacy.ID, legacy.Email, legacy.Role)
	if err != nil {
		t.Fatal(err)
	}
	for name, grant := range map[string]string{
		"missing": "", "forged signature": oauthRecoveryForgeSignature(t, valid), "ordinary login JWT": ordinaryLoginJWT,
		"wrong purpose":        wrongPurpose,
		"wrong issuer":         oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), map[string]any{"iss": "other"}),
		"wrong audience":       oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), map[string]any{"aud": "other"}),
		"expired":              oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(-time.Minute), nil),
		"missing exp":          oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(9*time.Minute), map[string]any{"exp": nil}),
		"over ten minutes":     oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(11*time.Minute), nil),
		"unsupported provider": oauthRecoveryGrant(t, legacy.ID, "local", time.Now().Add(9*time.Minute), nil),
		"invalid subject":      oauthRecoveryGrantWithSubject(t, "0", "google", time.Now().Add(9*time.Minute), nil),
		"non numeric subject":  oauthRecoveryGrantWithSubject(t, "no-user", "google", time.Now().Add(9*time.Minute), nil),
	} {
		t.Run(name, func(t *testing.T) {
			before := oauthRecoveryCaptureState(t, h)
			rec := oauthRecoveryPostEntry(h, grant, "application/json")
			if rec.Code != http.StatusBadRequest {
				t.Errorf("invalid recovery grant status=%d, want 400: %s", rec.Code, rec.Body.String())
			}
			if grant != "" && strings.Contains(rec.Body.String(), grant) {
				t.Error("error response reflected recovery grant")
			}
			for _, cookie := range (&http.Response{Header: rec.Header()}).Cookies() {
				if cookie.Name == "jwt_token" || cookie.Name == "jelly-cms-session" {
					t.Errorf("invalid grant issued application login cookie %q", cookie.Name)
				}
			}
			oauthRecoveryAssertStateUnchanged(t, h, before)
		})
	}
}

func TestOAuthRecoveryRejectsIneligibleUsersAndExistingIdentity(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	local := oauthRecoveryCreateLegacyUser(t, h, "local", "admin")
	local.Password = "fixture-local-password-hash"
	if err := h.db.Save(&local).Error; err != nil {
		t.Fatalf("mark local account as usable: %v", err)
	}
	provider := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(provider)
	if err := h.db.Exec("INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, 'google', 'already-linked-subject')", legacy.ID).Error; err != nil {
		t.Fatalf("insert existing active identity: %v", err)
	}
	for _, tc := range []struct {
		name string
		user core.User
	}{
		{name: "local account", user: local},
		{name: "already linked legacy account", user: legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := oauthRecoveryCaptureState(t, h)
			grant := oauthRecoveryGrant(t, tc.user.ID, "google", time.Now().Add(8*time.Minute), nil)
			rec := oauthRecoveryPostEntry(h, grant, echo.MIMEApplicationJSON)
			if rec.Code != http.StatusConflict && rec.Code != http.StatusBadRequest {
				t.Errorf("ineligible target returned %d, want 400 or 409: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), grant) || strings.Contains(rec.Header().Get(echo.HeaderLocation), grant) {
				t.Error("ineligible target response exposed recovery grant")
			}
			oauthRecoveryAssertStateUnchanged(t, h, before)
		})
	}
}

func TestOAuthRecoveryRejectsCrossOriginAndQueryOnlyWrites(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	provider := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(provider)
	grant := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(8*time.Minute), nil)
	for _, tc := range []struct {
		name, target, body string
	}{
		{name: "cross origin", target: "/auth/recover", body: "grant=" + url.QueryEscape(grant)},
		{name: "query only", target: "/auth/recover?grant=" + url.QueryEscape(grant), body: ""},
		{name: "valid grant without source proof", target: "/auth/recover", body: "grant=" + url.QueryEscape(grant)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := oauthRecoveryCaptureState(t, h)
			req := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(tc.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
			req.Header.Set(echo.HeaderAccept, "text/html")
			if tc.name == "cross origin" {
				req.Header.Set("Origin", "https://attacker.invalid")
				req.Header.Set("X-Jelly-CSRF", "1")
			} else if tc.name == "query only" {
				req.Header.Set("X-Jelly-CSRF", "1")
			}
			rec := httptest.NewRecorder()
			h.echo.ServeHTTP(rec, req)
			if tc.name == "cross origin" && rec.Code != http.StatusForbidden {
				t.Errorf("cross-origin recovery POST returned %d, want 403: %s", rec.Code, rec.Body.String())
			}
			if tc.name == "query only" && rec.Code != http.StatusBadRequest {
				t.Errorf("query-only recovery returned %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if tc.name == "valid grant without source proof" && rec.Code != http.StatusForbidden {
				t.Errorf("valid grant without source proof returned %d, want 403: %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), grant) || strings.Contains(rec.Header().Get(echo.HeaderLocation), grant) {
				t.Error("rejected recovery request reflected grant")
			}
			assertOAuthRecoveryNoCredential(t, rec)
			oauthRecoveryAssertStateUnchanged(t, h, before)
		})
	}
}

func TestOAuthRecoveryRejectsWrongCallbackProviderAndUpstreamFailures(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	googleProvider := newOAuthTxnIntegrationProvider("google")
	githubProvider := newOAuthTxnIntegrationProvider("github")
	resetOAuthInputProviders(googleProvider, githubProvider)
	grant := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(8*time.Minute), nil)
	before := oauthRecoveryCaptureState(t, h)
	entry := oauthRecoveryBegin(t, h, grant)
	wrong := oauthRecoveryCallback(h, "github", entry, "wrong-provider-code", nil, echo.MIMEApplicationJSON)
	if wrong.Code != http.StatusBadRequest {
		t.Errorf("different-provider callback returned %d, want 400: %s", wrong.Code, wrong.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, wrong)
	oauthRecoveryAssertNoLeaks(t, wrong, grant, "wrong-provider-code")
	oauthRecoveryAssertStateUnchanged(t, h, before)

	grant = oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(8*time.Minute), nil)
	entry = oauthRecoveryBegin(t, h, grant)
	googleProvider.user.UserID = " \t "
	emptySubject := oauthRecoveryCallback(h, "google", entry, "empty-subject-code", nil, echo.MIMEApplicationJSON)
	if emptySubject.Code != http.StatusBadRequest {
		t.Errorf("blank provider subject callback returned %d, want 400: %s", emptySubject.Code, emptySubject.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, emptySubject)
	oauthRecoveryAssertNoLeaks(t, emptySubject, grant, "empty-subject-code")
	oauthRecoveryAssertStateUnchanged(t, h, before)

	grant = oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(8*time.Minute), nil)
	entry = oauthRecoveryBegin(t, h, grant)
	providerError := oauthRecoveryCallback(h, "google", entry, "provider-failure-code", nil, echo.MIMEApplicationJSON)
	if providerError.Code != http.StatusBadGateway {
		t.Errorf("upstream failure returned %d, want 502: %s", providerError.Code, providerError.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, providerError)
	oauthRecoveryAssertNoLeaks(t, providerError, grant, "provider-failure-code", "barrier-token:")
	for _, secret := range []string{grant, "provider-failure-code", "barrier-token:"} {
		if strings.Contains(providerError.Body.String(), secret) {
			t.Errorf("upstream failure response reflected secret %q", secret)
		}
	}
	oauthRecoveryAssertStateUnchanged(t, h, before)
}

func TestOAuthRecoveryGoogleAndGitHubJSONAndHTMLLogin(t *testing.T) {
	for _, providerName := range []string{"google", "github"} {
		for _, accept := range []string{echo.MIMEApplicationJSON, "text/html"} {
			t.Run(providerName+"/"+accept, func(t *testing.T) {
				t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
				t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
				t.Setenv("APP_ENV", "test")
				h := oauthTxnPrepareHarness(t)
				legacy := oauthRecoveryCreateLegacyUser(t, h, providerName, "author")
				provider := newOAuthTxnIntegrationProvider(providerName)
				provider.user = goth.User{Provider: providerName, UserID: "stable-" + providerName + "-recovery-subject", Email: "new-profile@example.test", Name: "Changed Profile"}
				resetOAuthInputProviders(provider)
				grant := oauthRecoveryGrant(t, legacy.ID, providerName, time.Now().Add(8*time.Minute), nil)
				pending := oauthRecoveryBegin(t, h, grant)
				rec := oauthRecoveryCallback(h, providerName, pending, "success-"+providerName, nil, accept)
				if accept == echo.MIMEApplicationJSON {
					if rec.Code != http.StatusOK {
						t.Fatalf("JSON %s recovery returned %d: %s", providerName, rec.Code, rec.Body.String())
					}
					var payload struct {
						Token string    `json:"token"`
						User  core.User `json:"user"`
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload.Token == "" || payload.User.ID != legacy.ID {
						t.Fatalf("JSON recovery identity=%+v token=%t err=%v", payload.User, payload.Token != "", err)
					}
					me := h.request(http.MethodGet, "/api/auth/me", "", payload.Token, echo.MIMEApplicationJSON, "")
					if me.code != http.StatusOK || !strings.Contains(me.body, fmt.Sprintf(`"id":%d`, legacy.ID)) || !strings.Contains(me.body, `"role":"author"`) {
						t.Errorf("recovered JSON token /api/auth/me status/body=%d/%s", me.code, me.body)
					}
				} else {
					if rec.Code != http.StatusFound {
						t.Fatalf("HTML %s recovery returned %d, want 302: %s", providerName, rec.Code, rec.Body.String())
					}
					cookie := oauthRecoveryResponseCookie(rec, "jelly-cms-session")
					if cookie == nil {
						t.Fatal("HTML recovery omitted application session cookie")
					}
					meReq := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
					meReq.AddCookie(cookie)
					meRec := httptest.NewRecorder()
					h.echo.ServeHTTP(meRec, meReq)
					if meRec.Code != http.StatusOK || !strings.Contains(meRec.Body.String(), fmt.Sprintf(`"id":%d`, legacy.ID)) || !strings.Contains(meRec.Body.String(), `"role":"author"`) {
						t.Errorf("HTML recovery session /api/auth/me status/body=%d/%s", meRec.Code, meRec.Body.String())
					}
				}
			})
		}
	}
}

func TestOAuthRecoveryConcurrentCallbacksConsumeGrantAtomically(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "concurrent-recovery-subject", Email: "different@example.test"}
	resetOAuthInputProviders(provider)
	grant := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(8*time.Minute), nil)
	pending := []oauthRecoveryPending{oauthRecoveryBegin(t, h, grant), oauthRecoveryBegin(t, h, grant)}
	start := make(chan struct{})
	responses := make([]*httptest.ResponseRecorder, len(pending))
	var wg sync.WaitGroup
	for i := range pending {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			responses[i] = oauthRecoveryCallback(h, "google", pending[i], fmt.Sprintf("concurrent-recovery-%d", i), nil, echo.MIMEApplicationJSON)
		}(i)
	}
	close(start)
	wg.Wait()
	ok, rejected := 0, 0
	for _, response := range responses {
		switch response.Code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			rejected++
			assertOAuthRecoveryNoCredential(t, response)
		default:
			t.Errorf("concurrent callback returned %d: %s", response.Code, response.Body.String())
		}
	}
	if ok != 1 || rejected != 1 {
		t.Errorf("concurrent recovery successes/conflicts=%d/%d, want 1/1", ok, rejected)
	}
	var mappings int64
	if err := h.db.Table("oauth_identities").Where("user_id = ? AND provider = 'google' AND deleted_at IS NULL", legacy.ID).Count(&mappings).Error; err != nil || mappings != 1 {
		t.Errorf("active recovery mappings=%d err=%v, want one", mappings, err)
	}
}

func TestOAuthRecoveryNormalOAuthCannotLoginByMatchingEmail(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "admin")
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "not-linked-subject", Email: legacy.Email}
	resetOAuthInputProviders(provider)
	entry := oauthTxnServe(h, http.MethodGet, "/auth/google", nil)
	parsed, err := url.Parse(entry.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("parse OAuth URL: %v", err)
	}
	response := oauthRecoveryCallback(h, "google", oauthRecoveryPending{state: parsed.Query().Get("state"), cookie: oauthRecoveryResponseCookie(entry, gothic.SessionName)}, "same-email-no-grant", nil, echo.MIMEApplicationJSON)
	if response.Code != http.StatusConflict {
		t.Errorf("ordinary OAuth with matching email returned %d, want 409: %s", response.Code, response.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, response)
	var users int64
	if err := h.db.Model(&core.User{}).Count(&users).Error; err != nil || users != 1 {
		t.Errorf("matching-email OAuth changed user count to %d err=%v", users, err)
	}
}

func TestOAuthRecoveryExpiryIsRecheckedAfterUpstreamWait(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	provider := oauthRecoveryNewBarrierProvider("google", goth.User{Provider: "google", UserID: "expiry-barrier-subject", Email: "other@example.test"})
	resetOAuthInputProviders(provider)
	expires := time.Now().Add(5 * time.Second)
	grant := oauthRecoveryGrant(t, legacy.ID, "google", expires, nil)
	pending := oauthRecoveryBegin(t, h, grant)
	before := oauthRecoveryCaptureState(t, h)
	callbackResult := make(chan *httptest.ResponseRecorder, 1)
	callbackDone := make(chan struct{})
	go func() {
		defer close(callbackDone)
		callbackResult <- oauthRecoveryCallback(h, "google", pending, "expire-after-upstream", nil, echo.MIMEApplicationJSON)
	}()
	t.Cleanup(func() {
		provider.unblock()
		select {
		case <-callbackDone:
		case <-time.After(5 * time.Second):
			t.Error("recovery callback goroutine remained after releasing fake provider")
		}
	})
	select {
	case <-provider.fetchStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not reach fake upstream")
	}
	if until := time.Until(time.Unix(expires.Unix(), 0).Add(150 * time.Millisecond)); until > 0 {
		time.Sleep(until)
	}
	provider.unblock()
	var response *httptest.ResponseRecorder
	select {
	case response = <-callbackResult:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery callback did not finish after upstream release")
	}
	if response.Code != http.StatusBadRequest {
		t.Errorf("recovery grant that expired during upstream wait returned %d, want 400: %s", response.Code, response.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, response)
	oauthRecoveryAssertStateUnchanged(t, h, before)
	oauthRecoveryAssertNoConsumption(t, h, grant)
}

func TestOAuthRecoveryExpiryIsRecheckedAfterUserRowLockWait(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	provider := oauthRecoveryNewBarrierProvider("google", goth.User{Provider: "google", UserID: "row-lock-expiry-subject", Email: "other@example.test"})
	resetOAuthInputProviders(provider)
	expires := time.Now().Add(5 * time.Second)
	grant := oauthRecoveryGrant(t, legacy.ID, "google", expires, nil)
	pending := oauthRecoveryBegin(t, h, grant)
	lockTx := h.db.Begin()
	if lockTx.Error != nil {
		t.Fatal(lockTx.Error)
	}
	defer lockTx.Rollback()
	if err := lockTx.Exec("SELECT id FROM users WHERE id = ? FOR UPDATE", legacy.ID).Error; err != nil {
		t.Fatalf("lock legacy User row: %v", err)
	}
	before := oauthRecoveryCaptureState(t, h)
	callbackResult := make(chan *httptest.ResponseRecorder, 1)
	callbackDone := make(chan struct{})
	go func() {
		defer close(callbackDone)
		callbackResult <- oauthRecoveryCallback(h, "google", pending, "expire-after-row-lock", nil, echo.MIMEApplicationJSON)
	}()
	t.Cleanup(func() {
		provider.unblock()
		_ = lockTx.Rollback().Error
		select {
		case <-callbackDone:
		case <-time.After(5 * time.Second):
			t.Error("row-lock recovery callback remained after releasing test locks")
		}
	})
	select {
	case <-provider.fetchStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("callback did not reach fake upstream")
	}
	provider.unblock()
	deadline := time.Now().Add(3 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		var lockWaits int64
		if err := h.db.Raw(`SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query ILIKE '%users%'`).Scan(&lockWaits).Error; err != nil {
			t.Fatalf("observe row-lock wait: %v", err)
		}
		if lockWaits > 0 {
			waiting = true
			break
		}
		select {
		case response := <-callbackResult:
			t.Fatalf("callback completed before target User row lock was observed, status=%d body=%s", response.Code, response.Body.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !waiting {
		t.Fatal("callback never waited on the locked target User row")
	}
	if until := time.Until(time.Unix(expires.Unix(), 0).Add(150 * time.Millisecond)); until > 0 {
		time.Sleep(until)
	}
	if err := lockTx.Rollback().Error; err != nil {
		t.Fatalf("release target User row lock: %v", err)
	}
	var response *httptest.ResponseRecorder
	select {
	case response = <-callbackResult:
	case <-time.After(5 * time.Second):
		t.Fatal("row-lock recovery callback did not finish after releasing the User lock")
	}
	if response.Code != http.StatusBadRequest {
		t.Errorf("grant expiring while callback waited on User lock returned %d, want 400: %s", response.Code, response.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, response)
	oauthRecoveryAssertStateUnchanged(t, h, before)
	oauthRecoveryAssertNoConsumption(t, h, grant)
}

func TestOAuthRecoveryIdentityFailureRollsBackConsumptionAndAllowsNewEntryRetry(t *testing.T) {
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	legacy := oauthRecoveryCreateLegacyUser(t, h, "google", "author")
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "rollback-recovery-subject", Email: "new@example.test"}
	resetOAuthInputProviders(provider)
	grant := oauthRecoveryGrant(t, legacy.ID, "google", time.Now().Add(8*time.Minute), nil)
	if err := h.db.Exec(`CREATE FUNCTION reject_oauth_recovery_identity() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture identity write failure'; END $$`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec(`CREATE TRIGGER reject_oauth_recovery_identity BEFORE INSERT ON oauth_identities FOR EACH ROW EXECUTE FUNCTION reject_oauth_recovery_identity()`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.db.Exec("DROP TRIGGER IF EXISTS reject_oauth_recovery_identity ON oauth_identities").Error
		_ = h.db.Exec("DROP FUNCTION IF EXISTS reject_oauth_recovery_identity()").Error
	})
	pending := oauthRecoveryBegin(t, h, grant)
	failed := oauthRecoveryCallback(h, "google", pending, "rollback-failure", nil, echo.MIMEApplicationJSON)
	if failed.Code != http.StatusServiceUnavailable {
		t.Errorf("identity write failure returned %d, want 503: %s", failed.Code, failed.Body.String())
	}
	assertOAuthRecoveryNoCredential(t, failed)
	var mappings int64
	if err := h.db.Table("oauth_identities").Where("user_id = ? AND deleted_at IS NULL", legacy.ID).Count(&mappings).Error; err != nil || mappings != 0 {
		t.Errorf("identity write failure left mappings=%d err=%v", mappings, err)
	}
	oauthRecoveryAssertNoConsumption(t, h, grant)
	oldStateReplay := oauthRecoveryCallback(h, "google", pending, "rollback-state-replay", nil, echo.MIMEApplicationJSON)
	if oldStateReplay.Code != http.StatusBadRequest {
		t.Errorf("callback state replay after failed identity transaction returned %d, want 400", oldStateReplay.Code)
	}
	assertOAuthRecoveryNoCredential(t, oldStateReplay)
	if err := h.db.Exec("DROP TRIGGER reject_oauth_recovery_identity ON oauth_identities").Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Exec("DROP FUNCTION reject_oauth_recovery_identity()").Error; err != nil {
		t.Fatal(err)
	}
	retry := oauthRecoveryBegin(t, h, grant)
	succeeded := oauthRecoveryCallback(h, "google", retry, "rollback-retry-success", nil, echo.MIMEApplicationJSON)
	if succeeded.Code != http.StatusOK {
		t.Errorf("new entry retry after removing identity failure returned %d: %s", succeeded.Code, succeeded.Body.String())
	}
}

func oauthRecoveryCreateLegacyUser(t *testing.T, h *accessHarness, provider, role string) core.User {
	t.Helper()
	u := core.User{Username: fmt.Sprintf("legacy-%s-%s", provider, role), Email: fmt.Sprintf("legacy-%s-%s@example.test", provider, role), Provider: provider, Role: role}
	u.CreatedAt = time.Date(2024, 1, 2, 3, 4, 5, 123456789, time.UTC)
	u.UpdatedAt = u.CreatedAt
	if err := h.db.Create(&u).Error; err != nil {
		t.Fatalf("create legacy OAuth user: %v", err)
	}
	var persisted core.User
	if err := h.db.First(&persisted, u.ID).Error; err != nil {
		t.Fatalf("read back legacy OAuth user: %v", err)
	}
	return persisted
}

func oauthRecoveryGrant(t *testing.T, userID uint, provider string, expires time.Time, overrides map[string]any) string {
	t.Helper()
	return oauthRecoveryGrantWithSubject(t, fmt.Sprint(userID), provider, expires, overrides)
}

func oauthRecoveryGrantWithSubject(t *testing.T, subject, provider string, expires time.Time, overrides map[string]any) string {
	t.Helper()
	jtiBytes := make([]byte, 32)
	if _, err := rand.Read(jtiBytes); err != nil {
		t.Fatalf("generate recovery fixture jti: %v", err)
	}
	claims := map[string]any{
		"purpose": "oauth-recovery", "sub": subject, "provider": provider,
		"jti": base64.RawURLEncoding.EncodeToString(jtiBytes), "iat": time.Now().Unix(), "exp": expires.Unix(),
		"iss": "jelly-cms", "aud": "jelly-oauth-recovery",
	}
	for key, value := range overrides {
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}
	}
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	derived := hmac.New(sha256.New, []byte(oauthRecoveryJWTSecret))
	_, _ = derived.Write([]byte("jelly-oauth-recovery-v1"))
	signer := hmac.New(sha256.New, derived.Sum(nil))
	_, _ = signer.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signer.Sum(nil))
}

func oauthRecoveryForgeSignature(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[2] == "" {
		t.Fatal("fixture token has malformed signature")
	}
	first := parts[2][0]
	if first == 'A' {
		parts[2] = "B" + parts[2][1:]
	} else {
		parts[2] = "A" + parts[2][1:]
	}
	return strings.Join(parts, ".")
}

func oauthRecoveryAssertConsumptionAndAudit(t *testing.T, h *accessHarness, grant string, mapping struct {
	UserID            uint
	Provider, Subject string
}) {
	t.Helper()
	parts := strings.Split(grant, ".")
	if len(parts) != 3 {
		t.Fatalf("decode grant jti: malformed JWT")
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode grant claims: %v", err)
	}
	var claims struct {
		JTI string `json:"jti"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil || claims.JTI == "" {
		t.Fatalf("decode recovery grant claims: jti=%q err=%v", claims.JTI, err)
	}
	digest := sha256.Sum256([]byte("oauth-recovery\x00" + claims.JTI))
	expectedID := hex.EncodeToString(digest[:])
	var consumed struct{ ExpiresAt time.Time }
	if err := h.db.Table("revoked_logins").Select("expires_at").Where("id = ?", expectedID).Take(&consumed).Error; err != nil {
		t.Errorf("recovery purpose consumption record missing: %v", err)
	} else if consumed.ExpiresAt.Before(time.Unix(claims.Exp, 0).Add(time.Second)) {
		t.Errorf("recovery consumption expires at %s, before grant exp+1s %s", consumed.ExpiresAt, time.Unix(claims.Exp, 0).Add(time.Second))
	}
	for _, table := range []string{"oauth_identities", "oauth_identity_audit_log", "revoked_logins"} {
		var rawGrantRows, rawJTIRows int64
		if err := h.db.Table(table).Where("CAST(row_to_json("+table+") AS text) LIKE ?", "%"+grant+"%").Count(&rawGrantRows).Error; err != nil {
			t.Errorf("scan %s for raw grant: %v", table, err)
		}
		if err := h.db.Table(table).Where("CAST(row_to_json("+table+") AS text) LIKE ?", "%"+claims.JTI+"%").Count(&rawJTIRows).Error; err != nil {
			t.Errorf("scan %s for raw jti: %v", table, err)
		}
		if rawGrantRows != 0 || rawJTIRows != 0 {
			t.Errorf("database table %s stored raw recovery secret: grant rows=%d jti rows=%d", table, rawGrantRows, rawJTIRows)
		}
	}
	var identityID uint
	if err := h.db.Table("oauth_identities").Select("id").Where("provider = ? AND subject = ? AND deleted_at IS NULL", "google", mapping.Subject).Scan(&identityID).Error; err != nil {
		t.Fatal(err)
	}
	var audit struct {
		ActorID            *uint
		OldState, NewState string
	}
	if err := h.db.Table("oauth_identity_audit_log").Select("actor_id, old_state::text AS old_state, new_state::text AS new_state").Where("identity_id = ?", identityID).Order("id DESC").Take(&audit).Error; err != nil {
		t.Errorf("read recovery identity audit: %v", err)
	} else {
		if audit.ActorID != nil {
			t.Errorf("recovery audit actor=%d, want null", *audit.ActorID)
		}
		for _, secret := range []string{grant, claims.JTI, mapping.Subject, "access_token", "refresh_token"} {
			if strings.Contains(audit.OldState+audit.NewState, secret) {
				t.Errorf("recovery audit snapshot contains sensitive value %q", secret)
			}
		}
	}
}

func oauthRecoveryAssertNoConsumption(t *testing.T, h *accessHarness, grant string) {
	t.Helper()
	jti := oauthRecoveryGrantJTI(t, grant)
	digest := sha256.Sum256([]byte("oauth-recovery\x00" + jti))
	var rows int64
	if err := h.db.Table("revoked_logins").Where("id = ?", hex.EncodeToString(digest[:])).Count(&rows).Error; err != nil {
		t.Fatalf("check rolled-back recovery consumption: %v", err)
	}
	if rows != 0 {
		t.Errorf("failed identity transaction left %d recovery consumption records", rows)
	}
}

func oauthRecoveryGrantJTI(t *testing.T, grant string) string {
	t.Helper()
	parts := strings.Split(grant, ".")
	if len(parts) != 3 {
		t.Fatal("decode grant jti: malformed JWT")
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode grant claims: %v", err)
	}
	var claims struct {
		JTI string `json:"jti"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil || claims.JTI == "" {
		t.Fatalf("decode grant jti: jti=%q err=%v", claims.JTI, err)
	}
	return claims.JTI
}

type oauthRecoveryBarrierProvider struct {
	name         string
	user         goth.User
	fetchStarted chan struct{}
	released     chan struct{}
	releaseOnce  sync.Once
}

func oauthRecoveryNewBarrierProvider(name string, user goth.User) *oauthRecoveryBarrierProvider {
	return &oauthRecoveryBarrierProvider{name: name, user: user, fetchStarted: make(chan struct{}), released: make(chan struct{})}
}

func (p *oauthRecoveryBarrierProvider) Name() string        { return p.name }
func (p *oauthRecoveryBarrierProvider) SetName(name string) { p.name = name }
func (p *oauthRecoveryBarrierProvider) BeginAuth(state string) (goth.Session, error) {
	return &oauthRecoveryBarrierSession{owner: p, State: state}, nil
}
func (p *oauthRecoveryBarrierProvider) UnmarshalSession(raw string) (goth.Session, error) {
	var session oauthRecoveryBarrierSession
	if err := json.Unmarshal([]byte(raw), &session); err != nil {
		return nil, err
	}
	session.owner = p
	return &session, nil
}
func (p *oauthRecoveryBarrierProvider) FetchUser(session goth.Session) (goth.User, error) {
	close(p.fetchStarted)
	<-p.released
	if strings.Contains(session.(*oauthRecoveryBarrierSession).AccessToken, "provider-failure") {
		return goth.User{}, fmt.Errorf("fake provider rejected request")
	}
	return p.user, nil
}
func (*oauthRecoveryBarrierProvider) Debug(bool)                                 {}
func (*oauthRecoveryBarrierProvider) RefreshToken(string) (*oauth2.Token, error) { return nil, nil }
func (*oauthRecoveryBarrierProvider) RefreshTokenAvailable() bool                { return false }
func (p *oauthRecoveryBarrierProvider) unblock()                                 { p.releaseOnce.Do(func() { close(p.released) }) }

type oauthRecoveryBarrierSession struct {
	owner       *oauthRecoveryBarrierProvider `json:"-"`
	State       string                        `json:"state"`
	AccessToken string                        `json:"access_token,omitempty"`
}

func (s *oauthRecoveryBarrierSession) GetAuthURL() (string, error) {
	return "https://provider.invalid/authorize?" + url.Values{"state": {s.State}}.Encode(), nil
}
func (s *oauthRecoveryBarrierSession) Marshal() string { b, _ := json.Marshal(s); return string(b) }
func (s *oauthRecoveryBarrierSession) Authorize(_ goth.Provider, params goth.Params) (string, error) {
	s.AccessToken = "barrier-token:" + params.Get("code")
	return s.AccessToken, nil
}

func oauthRecoveryPostEntry(h *accessHarness, grant, contentType string) *httptest.ResponseRecorder {
	body := "grant=" + url.QueryEscape(grant)
	if contentType == echo.MIMEApplicationJSON {
		encoded, _ := json.Marshal(map[string]string{"grant": grant})
		body = string(encoded)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/recover", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, contentType)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	req.Header.Set("X-Jelly-CSRF", "1")
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

type oauthRecoveryPending struct {
	state  string
	cookie *http.Cookie
}

func oauthRecoveryBegin(t *testing.T, h *accessHarness, grant string) oauthRecoveryPending {
	t.Helper()
	rec := oauthRecoveryPostEntry(h, grant, echo.MIMEApplicationJSON)
	if rec.Code != http.StatusOK {
		t.Fatalf("recovery entry returned %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		AuthURL string `json:"auth_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode recovery entry: %v", err)
	}
	parsed, err := url.Parse(payload.AuthURL)
	if err != nil {
		t.Fatalf("parse recovery auth_url: %v", err)
	}
	pending := oauthRecoveryPending{state: parsed.Query().Get("state"), cookie: oauthRecoveryResponseCookie(rec, gothic.SessionName)}
	if pending.state == "" || pending.cookie == nil {
		t.Fatal("recovery entry omitted state or signed Goth cookie")
	}
	return pending
}

func oauthRecoveryCallback(h *accessHarness, routeProvider string, pending oauthRecoveryPending, code string, extraCookie *http.Cookie, accept string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/"+routeProvider+"/callback?state="+url.QueryEscape(pending.state)+"&code="+url.QueryEscape(code), nil)
	req.Header.Set(echo.HeaderAccept, accept)
	req.AddCookie(pending.cookie)
	if extraCookie != nil {
		req.AddCookie(extraCookie)
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func assertOAuthRecoveryNoCredential(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for _, cookie := range (&http.Response{Header: rec.Header()}).Cookies() {
		if cookie.Name == "jwt_token" || cookie.Name == "jelly-cms-session" {
			t.Errorf("failed recovery issued login cookie %q", cookie.Name)
		}
	}
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Error("failed recovery response contained application token field")
	}
}

func oauthRecoveryAssertNoLeaks(t *testing.T, rec *httptest.ResponseRecorder, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(rec.Body.String()+rec.Header().Get(echo.HeaderLocation), secret) {
			t.Errorf("recovery error response leaked secret %q", secret)
		}
	}
}

type oauthRecoveryDBState struct {
	users, mappings int64
}

func oauthRecoveryCaptureState(t *testing.T, h *accessHarness) oauthRecoveryDBState {
	t.Helper()
	var state oauthRecoveryDBState
	if err := h.db.Model(&core.User{}).Count(&state.users).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Table("oauth_identities").Where("deleted_at IS NULL").Count(&state.mappings).Error; err != nil {
		t.Fatal(err)
	}
	return state
}

func oauthRecoveryAssertStateUnchanged(t *testing.T, h *accessHarness, before oauthRecoveryDBState) {
	t.Helper()
	after := oauthRecoveryCaptureState(t, h)
	if after != before {
		t.Errorf("rejected recovery changed users/mappings: before=%+v after=%+v", before, after)
	}
}

func oauthRecoveryResponseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range (&http.Response{Header: rec.Header()}).Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

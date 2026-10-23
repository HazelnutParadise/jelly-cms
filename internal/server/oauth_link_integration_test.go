package server

import (
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

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth"
	"github.com/markbates/goth/gothic"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestOAuthAccountPublicIndexAndIdentityList(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(provider)
	user := h.user("author", "oauth-account-list")
	oauthAccountSetPassword(t, h, user.ID, "local-password")
	login := oauthAccountLoginFor(t, h, user)

	indexResponse := oauthAccountServe(h, http.MethodGet, "/api/auth", nil, nil, "", "")
	if indexResponse.Code != http.StatusOK {
		t.Fatalf("GET /api/auth returned %d, want 200", indexResponse.Code)
	}
	var index struct {
		OpenAPIURL string `json:"openapi_url"`
		Operations []struct {
			Method  string `json:"method"`
			Path    string `json:"path"`
			Summary string `json:"summary"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(indexResponse.Body.Bytes(), &index); err != nil {
		t.Fatalf("decode auth API index: %v", err)
	}
	if index.OpenAPIURL != "/api/auth/openapi.json" {
		t.Errorf("openapi_url=%q, want /api/auth/openapi.json", index.OpenAPIURL)
	}
	documented := map[string]bool{}
	for _, operation := range index.Operations {
		if operation.Method != strings.ToUpper(operation.Method) || operation.Summary == "" {
			t.Errorf("index operation must use uppercase method and nonempty summary: %+v", operation)
		}
		documented[operation.Method+" "+operation.Path] = true
	}
	for _, operation := range []string{
		"POST /auth/login",
		"POST /auth/logout",
		"GET /auth/{provider}",
		"GET /auth/{provider}/callback",
		"GET /api/auth/me",
		"POST /auth/recover",
		"GET /api/auth/identities",
		"POST /api/auth/identities/{provider}/start",
		"DELETE /api/auth/identities/{provider}",
	} {
		if !documented[operation] {
			t.Errorf("auth API index omitted operation %q", operation)
		}
	}
	openapiResponse := oauthAccountServe(h, http.MethodGet, index.OpenAPIURL, nil, nil, "", "")
	if openapiResponse.Code != http.StatusOK {
		t.Fatalf("GET %s returned %d, want 200", index.OpenAPIURL, openapiResponse.Code)
	}
	var openapi map[string]any
	if err := json.Unmarshal(openapiResponse.Body.Bytes(), &openapi); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	paths, _ := openapi["paths"].(map[string]any)
	for path, method := range map[string]string{
		"/auth/login":                           "post",
		"/auth/logout":                          "post",
		"/auth/{provider}":                      "get",
		"/auth/{provider}/callback":             "get",
		"/api/auth/me":                          "get",
		"/auth/recover":                         "post",
		"/api/auth/identities":                  "get",
		"/api/auth/identities/{provider}/start": "post",
		"/api/auth/identities/{provider}":       "delete",
	} {
		operations, _ := paths[path].(map[string]any)
		if operations[method] == nil {
			t.Errorf("OpenAPI paths omitted lowercase %s %s", method, path)
		}
	}
	openapiJSON, _ := json.Marshal(openapi)
	for _, schemaProperty := range []string{"auth_url", "local_login", "grant"} {
		if !strings.Contains(string(openapiJSON), schemaProperty) {
			t.Errorf("OpenAPI schemas omitted %q", schemaProperty)
		}
	}
	for label, body := range map[string]string{"index": indexResponse.Body.String(), "OpenAPI": openapiResponse.Body.String()} {
		for _, secret := range []string{user.Email, "local-password", "SESSION_SECRET", "JWT_SECRET", "provider-token:"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s documentation exposed account or configuration data %q", label, secret)
			}
		}
	}

	unauthenticated := oauthAccountServe(h, http.MethodGet, "/api/auth/identities", nil, nil, "", "")
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Errorf("anonymous identity list returned %d, want 401", unauthenticated.Code)
	}
	listed := oauthAccountServe(h, http.MethodGet, "/api/auth/identities", []*http.Cookie{login.jwtCookie}, nil, "", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("authenticated identity list returned %d, want 200", listed.Code)
	}
	var payload struct {
		LocalLogin bool `json:"local_login"`
		Providers  []struct {
			Provider string `json:"provider"`
			Enabled  bool   `json:"enabled"`
			Linked   bool   `json:"linked"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode identity list: %v", err)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(listed.Body.Bytes(), &topLevel); err != nil || len(topLevel) != 2 || topLevel["local_login"] == nil || topLevel["providers"] == nil {
		t.Errorf("identity list has unexpected top-level fields: %s (%v)", listed.Body.String(), err)
	}
	if !payload.LocalLogin || len(payload.Providers) != 2 {
		t.Errorf("identity list local_login/providers=%t/%+v, want true and fixed Google/GitHub entries", payload.LocalLogin, payload.Providers)
	}
	providers := map[string]struct{ enabled, linked bool }{}
	for _, item := range payload.Providers {
		providers[item.Provider] = struct{ enabled, linked bool }{item.Enabled, item.Linked}
	}
	if providers["google"] != (struct{ enabled, linked bool }{true, false}) || providers["github"] != (struct{ enabled, linked bool }{false, false}) {
		t.Errorf("identity provider state=%v, want only loaded Google enabled", providers)
	}
	for _, forbidden := range []string{user.Email, "local-hash", "subject", "token", "client_secret", "SESSION_SECRET"} {
		if strings.Contains(strings.ToLower(listed.Body.String()), strings.ToLower(forbidden)) {
			t.Errorf("identity list exposed forbidden value/field %q", forbidden)
		}
	}
	withoutPassword := h.user("author", "oauth-account-no-local-password")
	noPasswordLogin := oauthAccountLoginFor(t, h, withoutPassword)
	noPasswordList := oauthAccountServe(h, http.MethodGet, "/api/auth/identities", []*http.Cookie{noPasswordLogin.jwtCookie}, nil, "", "")
	var noPasswordPayload struct {
		LocalLogin bool `json:"local_login"`
	}
	if noPasswordList.Code != http.StatusOK || json.Unmarshal(noPasswordList.Body.Bytes(), &noPasswordPayload) != nil || noPasswordPayload.LocalLogin {
		t.Errorf("Provider local without a stored Password must have local_login=false; status=%d body=%s", noPasswordList.Code, noPasswordList.Body.String())
	}
}

func TestOAuthAccountLinkStartRequiresCookieOnlyAndLoadedProvider(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	google := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(google)
	user := h.user("author", "oauth-account-start")
	login := oauthAccountLoginFor(t, h, user)
	path := "/api/auth/identities/google/start"
	noLogin := oauthAccountServe(h, http.MethodPost, path, nil, nil, "", "")
	if noLogin.Code != http.StatusUnauthorized {
		t.Errorf("link start without a login cookie returned %d, want 401", noLogin.Code)
	}

	withAuthorization := oauthAccountServe(h, http.MethodPost, path, []*http.Cookie{login.jwtCookie}, nil, "Bearer "+login.token, "")
	if withAuthorization.Code < 400 || withAuthorization.Code >= 500 {
		t.Errorf("start with Authorization header returned %d, want a 4xx", withAuthorization.Code)
	}
	bearerOnly := oauthAccountServe(h, http.MethodPost, path, nil, nil, "Bearer "+login.token, "")
	if bearerOnly.Code < 400 || bearerOnly.Code >= 500 {
		t.Errorf("start with only a Bearer header returned %d, want rejection", bearerOnly.Code)
	}
	emptyAuthorizationRequest := httptest.NewRequest(http.MethodPost, path, nil)
	emptyAuthorizationRequest.AddCookie(login.jwtCookie)
	emptyAuthorizationRequest.Header.Set(echo.HeaderAuthorization, "")
	emptyAuthorizationRequest.Header.Set("X-Jelly-CSRF", "1")
	emptyAuthorizationResponse := httptest.NewRecorder()
	h.echo.ServeHTTP(emptyAuthorizationResponse, emptyAuthorizationRequest)
	if emptyAuthorizationResponse.Code < 400 || emptyAuthorizationResponse.Code >= 500 {
		t.Errorf("start with an explicitly empty Authorization header returned %d, want a 4xx", emptyAuthorizationResponse.Code)
	}
	if active, history := oauthAccountIdentityCounts(t, h.db, user.ID, "google"); active != 0 || history != 0 {
		t.Errorf("start with an explicitly empty Authorization header created Google identity rows active/history=%d/%d", active, history)
	}
	for _, cookie := range (&http.Response{Header: emptyAuthorizationResponse.Header()}).Cookies() {
		if cookie.Name == gothic.SessionName && cookie.Value != "" && cookie.MaxAge >= 0 {
			t.Error("start with an explicitly empty Authorization header created a Goth pending cookie")
		}
	}
	start := oauthAccountServe(h, http.MethodPost, path, []*http.Cookie{login.jwtCookie}, nil, "", "")
	if start.Code != http.StatusOK {
		t.Fatalf("cookie-authenticated link start returned %d, want 200", start.Code)
	}
	var response struct {
		AuthURL string `json:"auth_url"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &response); err != nil || response.AuthURL == "" {
		t.Fatalf("link start response has no auth_url: %q (%v)", start.Body.String(), err)
	}
	parsed, err := url.Parse(response.AuthURL)
	if err != nil || parsed.Query().Get("state") == "" {
		t.Fatalf("auth_url has invalid/missing state: %q (%v)", response.AuthURL, err)
	}
	if count, err := oauthAccountActiveIdentityCount(h.db, user.ID); err == nil && count != 0 {
		t.Errorf("starting link created %d identity rows", count)
	}
	for _, unregistered := range []string{"github", "not-a-provider"} {
		rec := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/"+unregistered+"/start", []*http.Cookie{login.jwtCookie}, nil, "", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("start for unregistered/unknown %q returned %d, want 400", unregistered, rec.Code)
		}
	}
	if got := oauthAccountActiveIdentityCountOrZero(t, h.db, user.ID); got != 0 {
		t.Errorf("rejected starts created %d identity rows", got)
	}
}

func TestOAuthAccountOpenAPIHeaderSchemas(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)

	response := oauthAccountServe(h, http.MethodGet, "/api/auth/openapi.json", nil, nil, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/openapi.json returned %d, want 200: %s", response.Code, response.Body.String())
	}
	var document map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI document has no paths object")
	}
	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}
	headerCount := 0
	for path, rawPathItem := range paths {
		pathItem, ok := rawPathItem.(map[string]any)
		if !ok {
			t.Errorf("OpenAPI path %q is not an object", path)
			continue
		}
		if _, isReference := pathItem["$ref"]; isReference {
			continue
		}
		for method, rawOperation := range pathItem {
			if !methods[method] {
				continue
			}
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				t.Errorf("OpenAPI operation %s %s is not an object", strings.ToUpper(method), path)
				continue
			}
			responses, ok := operation["responses"].(map[string]any)
			if !ok {
				t.Errorf("OpenAPI operation %s %s has no responses object", strings.ToUpper(method), path)
				continue
			}
			for status, rawResponse := range responses {
				responseObject, ok := rawResponse.(map[string]any)
				if !ok {
					t.Errorf("OpenAPI response %s for %s %s is not an object", status, strings.ToUpper(method), path)
					continue
				}
				if _, isReference := responseObject["$ref"]; isReference {
					continue
				}
				headers, ok := responseObject["headers"].(map[string]any)
				if !ok {
					continue
				}
				for name, rawHeader := range headers {
					header, ok := rawHeader.(map[string]any)
					if !ok {
						t.Errorf("OpenAPI response header %q for %s %s status %s is not an object", name, strings.ToUpper(method), path, status)
						continue
					}
					if _, isReference := header["$ref"]; isReference {
						continue
					}
					headerCount++
					_, schemaPresent := header["schema"]
					_, contentPresent := header["content"]
					if schemaPresent == contentPresent || (schemaPresent && header["schema"] == nil) || (contentPresent && header["content"] == nil) {
						t.Errorf("OpenAPI response header %q for %s %s status %s must define exactly one non-null schema or content", name, strings.ToUpper(method), path, status)
					}
				}
			}
		}
	}
	if headerCount == 0 {
		t.Error("OpenAPI document contains no inline response Header Objects to validate")
	}
}

func TestOAuthAccountLinkCallbackPreservesAccountAndRejectsConflicts(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "fixed-link-subject", Email: "different@link.example.test", Name: "Changed Outside Name"}
	resetOAuthInputProviders(provider)
	owner := h.user("admin", "oauth-account-owner")
	owner.Role = "admin"
	oauthAccountSetPassword(t, h, owner.ID, "owner-password")
	other := h.user("author", "oauth-account-other")
	if err := oauthAccountInsertIdentity(h.db, other.ID, "github", "occupied-by-other"); err != nil {
		t.Fatalf("seed another user's provider identity: %v", err)
	}
	login := oauthAccountLoginFor(t, h, owner)
	usersBefore := accessUsers(t, h.db)
	ownedPost := core.Post{Title: "Account-owned post", Slug: "oauth-account-owned-post", Content: "owned before link", Type: "post", Status: "draft", AuthorID: owner.ID}
	if err := h.db.Create(&ownedPost).Error; err != nil {
		t.Fatalf("seed account-owned post: %v", err)
	}

	entry := oauthAccountBeginLink(t, h, provider, login, "")
	callback := oauthAccountCallback(h, entry, "link-success", true)
	if callback.Code != http.StatusOK {
		t.Fatalf("valid link callback returned %d, want 200: %s", callback.Code, callback.Body.String())
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(callback.Body.Bytes(), &result); err != nil || len(result) != 1 || result["message"] == nil {
		t.Errorf("link JSON response=%s, want only message (%v)", callback.Body.String(), err)
	}
	if got := accessUsers(t, h.db); !reflect.DeepEqual(usersBefore, got) {
		t.Error("successful link changed a User, role, password, or account-owned data")
	}
	if oauthAccountHasLoginCookie(callback) {
		t.Error("link callback reissued an application login/session cookie")
	}
	me := oauthAccountServe(h, http.MethodGet, "/api/auth/me", []*http.Cookie{login.jwtCookie}, nil, "", "")
	var current core.User
	if me.Code != http.StatusOK || json.Unmarshal(me.Body.Bytes(), &current) != nil || current.ID != owner.ID {
		t.Errorf("original login after link returned status/user=%d/%d, want 200/%d", me.Code, current.ID, owner.ID)
	}
	var ownedPostAfter core.Post
	if err := h.db.First(&ownedPostAfter, ownedPost.ID).Error; err != nil || ownedPostAfter.AuthorID != owner.ID || ownedPostAfter.Title != ownedPost.Title {
		t.Errorf("link changed original post ownership/content: %+v (%v)", ownedPostAfter, err)
	}
	if got := oauthAccountActiveIdentityCountOrZero(t, h.db, owner.ID); got != 1 {
		t.Errorf("successful callback left %d active identities for owner, want one", got)
	}
	if got := oauthAccountSubjectOwner(t, h.db, "google", "fixed-link-subject"); got != int64(owner.ID) {
		t.Errorf("linked provider subject belongs to user %d, want owner %d", got, owner.ID)
	}
	linkedList := oauthAccountServe(h, http.MethodGet, "/api/auth/identities", []*http.Cookie{login.jwtCookie}, nil, "", "")
	var linkedState struct {
		Providers []struct {
			Provider string `json:"provider"`
			Linked   bool   `json:"linked"`
		} `json:"providers"`
	}
	if linkedList.Code != http.StatusOK || json.Unmarshal(linkedList.Body.Bytes(), &linkedState) != nil {
		t.Fatalf("identity list after link returned %d: %s", linkedList.Code, linkedList.Body.String())
	}
	googleLinked := false
	for _, item := range linkedState.Providers {
		if item.Provider == "google" {
			googleLinked = item.Linked
		}
	}
	if !googleLinked {
		t.Error("public identity list did not report the successfully linked Google account")
	}
	replay := oauthAccountCallback(h, entry, "link-success", true)
	if replay.Code < 400 || replay.Code >= 500 {
		t.Errorf("replayed successful link callback returned %d, want rejection", replay.Code)
	}
	retryEntry := oauthAccountBeginLink(t, h, provider, login, "")
	retry := oauthAccountCallback(h, retryEntry, "same-link-retry", true)
	if retry.Code != http.StatusOK {
		t.Errorf("same linked subject retried through a fresh OAuth state returned %d, want idempotent 200", retry.Code)
	}
	if got := oauthAccountActiveIdentityCountOrZero(t, h.db, owner.ID); got != 1 {
		t.Errorf("same-subject retry left %d active identities, want one", got)
	}

	for _, tc := range []struct {
		name, subject string
		status        int
	}{
		{name: "different identity at same provider", subject: "second-google-subject", status: http.StatusConflict},
		{name: "blank fixed identity", subject: " \t", status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.user.UserID = tc.subject
			provider.user.Email = "another-email@link.example.test"
			provider.mu.Unlock()
			before := oauthAccountActiveIdentityCountOrZero(t, h.db, owner.ID)
			entry := oauthAccountBeginLink(t, h, provider, login, "")
			rec := oauthAccountCallback(h, entry, "link-"+strings.ReplaceAll(tc.name, " ", "-"), true)
			if rec.Code != tc.status {
				t.Errorf("callback for %s returned %d, want %d", tc.name, rec.Code, tc.status)
			}
			if oauthAccountActiveIdentityCountOrZero(t, h.db, owner.ID) != before {
				t.Errorf("failed callback for %s changed owner's active identity count", tc.name)
			}
			if oauthAccountHasLoginCookie(rec) || strings.Contains(rec.Body.String(), "provider-token:") {
				t.Errorf("failed callback for %s issued credential or exposed provider data", tc.name)
			}
		})
	}

	provider.mu.Lock()
	provider.user.UserID = "fixed-link-subject"
	provider.user.Email = "html-link@link.example.test"
	provider.mu.Unlock()
	htmlEntry := oauthAccountBeginLink(t, h, provider, login, "")
	htmlCallback := oauthAccountCallback(h, htmlEntry, "html-link", false)
	if htmlCallback.Code != http.StatusFound || htmlCallback.Header().Get(echo.HeaderLocation) != "/account" {
		t.Errorf("HTML link callback status/location=%d/%q, want 302 to /account", htmlCallback.Code, htmlCallback.Header().Get(echo.HeaderLocation))
	}
	provider.mu.Lock()
	provider.user.UserID = "missing-email-subject"
	provider.user.Email = ""
	provider.mu.Unlock()
	withoutEmailUser := h.user("author", "oauth-account-missing-email")
	withoutEmailLogin := oauthAccountLoginFor(t, h, withoutEmailUser)
	missingEmailEntry := oauthAccountBeginLink(t, h, provider, withoutEmailLogin, "")
	missingEmail := oauthAccountCallback(h, missingEmailEntry, "link-missing-email", true)
	if missingEmail.Code != http.StatusOK {
		t.Errorf("valid fixed identity without email returned %d, want 200", missingEmail.Code)
	}
	if oauthAccountActiveIdentityCountOrZero(t, h.db, owner.ID) != 1 || oauthAccountActiveIdentityCountOrZero(t, h.db, withoutEmailUser.ID) != 1 {
		t.Error("email-less identity must link a separate account without violating one identity per provider")
	}
	if got := oauthAccountSubjectOwner(t, h.db, "google", "fixed-link-subject"); got != int64(owner.ID) {
		t.Errorf("email-less callback changed existing subject owner to %d, want original owner %d", got, owner.ID)
	}
	if got := oauthAccountSubjectOwner(t, h.db, "google", "missing-email-subject"); got != int64(withoutEmailUser.ID) {
		t.Errorf("email-less subject belongs to user %d, want new user %d", got, withoutEmailUser.ID)
	}

	provider.mu.Lock()
	provider.user.UserID = "login-id-mismatch-subject"
	provider.user.Email = "login-id-mismatch@link.example.test"
	provider.mu.Unlock()
	otherLoginID := oauthAccountLoginFor(t, h, owner)
	mismatchEntry := oauthAccountBeginLink(t, h, provider, login, "")
	mismatchCallback := oauthAccountCallbackWithLogin(h, mismatchEntry, "login-id-mismatch", otherLoginID.jwtCookie)
	if mismatchCallback.Code < 400 || mismatchCallback.Code >= 500 {
		t.Errorf("callback with a different JWT login ID returned %d, want rejection", mismatchCallback.Code)
	}
	if oauthAccountSubjectOwner(t, h.db, "google", "login-id-mismatch-subject") != 0 {
		t.Error("callback with a different login ID created a mapping")
	}

	otherUserLogin := oauthAccountLoginFor(t, h, other)
	provider.mu.Lock()
	provider.user.UserID = "account-mismatch-subject"
	provider.user.Email = "account-mismatch@link.example.test"
	provider.mu.Unlock()
	accountMismatchEntry := oauthAccountBeginLink(t, h, provider, login, "")
	accountMismatch := oauthAccountCallbackWithLogin(h, accountMismatchEntry, "account-mismatch", otherUserLogin.jwtCookie)
	if accountMismatch.Code < 400 || accountMismatch.Code >= 500 {
		t.Errorf("callback switched to another account returned %d, want rejection", accountMismatch.Code)
	}
	if oauthAccountSubjectOwner(t, h.db, "google", "account-mismatch-subject") != 0 {
		t.Error("callback switched to another account created a mapping")
	}
}

func TestOAuthAccountRejectsProviderIdentityOwnedByAnotherUser(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "subject-owned-by-other", Email: "different-owner-email@link.example.test", Name: "Other Owner"}
	resetOAuthInputProviders(provider)
	owner := h.user("author", "oauth-account-claim-owner")
	other := h.user("author", "oauth-account-claim-existing")
	if err := oauthAccountInsertIdentity(h.db, other.ID, "google", "subject-owned-by-other"); err != nil {
		t.Fatalf("seed existing Google identity: %v", err)
	}
	login := oauthAccountLoginFor(t, h, owner)
	entry := oauthAccountBeginLink(t, h, provider, login, "")
	callback := oauthAccountCallback(h, entry, "owned-by-other", true)
	if callback.Code != http.StatusConflict {
		t.Fatalf("linking another user's Google subject returned %d, want 409", callback.Code)
	}
	if oauthAccountActiveIdentityCountOrZero(t, h.db, owner.ID) != 0 {
		t.Error("conflicting owner acquired an identity mapping")
	}
	if oauthAccountActiveIdentityCountOrZero(t, h.db, other.ID) != 1 || oauthAccountSubjectOwner(t, h.db, "google", "subject-owned-by-other") != int64(other.ID) {
		t.Error("conflict changed the original owner's Google identity mapping")
	}
}

func TestOAuthAccountNormalOAuthQueryCannotCreateLinkIntent(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "normal-entry-query-subject", Email: "normal-entry-query@link.example.test", Name: "Normal Entry"}
	resetOAuthInputProviders(provider)
	user := h.user("admin", "oauth-account-query-owner")
	login := oauthAccountLoginFor(t, h, user)
	before := accessUsers(t, h.db)
	query := url.Values{"purpose": {"link"}, "link": {"true"}, "user_id": {fmt.Sprint(user.ID)}}
	entry := oauthAccountBeginNormal(t, h, provider, login, query.Encode())
	callback := oauthAccountCallback(h, entry, "normal-query-entry", true)
	if callback.Code != http.StatusOK {
		t.Fatalf("normal OAuth callback with forged link query returned %d: %s", callback.Code, callback.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(callback.Body.Bytes(), &payload); err != nil || len(payload) != 3 || payload["token"] == nil {
		t.Errorf("normal OAuth callback contract was not preserved: %s (%v)", callback.Body.String(), err)
	}
	if got := accessUsers(t, h.db); len(got) != len(before)+1 {
		t.Errorf("forged link query created %d users, want a separate normal OAuth account", len(got)-len(before))
	}
	if got := oauthAccountActiveIdentityCountOrZero(t, h.db, user.ID); got != 0 {
		t.Errorf("normal entry query linked %d identities to the supplied user", got)
	}
}

func TestOAuthAccountAuthenticationSourceSelectionAndCallbackRevalidation(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthTxnIntegrationProvider("google")
	resetOAuthInputProviders(provider)
	user := h.user("author", "oauth-account-source-selection")
	oauthAccountSetPassword(t, h, user.ID, "source-password")
	loginA := oauthAccountPublicLoginFor(t, h, user, "source-password")
	loginB := oauthAccountPublicLoginFor(t, h, user, "source-password")
	if loginA.jwtCookie.Value == loginB.jwtCookie.Value || loginA.sessionCookie.Value == loginB.sessionCookie.Value {
		t.Fatal("two public local logins unexpectedly reused credentials")
	}
	emptyJWTCookie := &http.Cookie{Name: "jwt_token", Value: "", Path: "/"}
	jwtCookieAndSession := []*http.Cookie{emptyJWTCookie, loginA.sessionCookie}
	currentUser := oauthAccountServe(h, http.MethodGet, "/api/auth/me", jwtCookieAndSession, nil, "", "")
	if currentUser.Code != http.StatusUnauthorized {
		t.Errorf("me with an empty JWT cookie and valid Session A returned %d, want 401 without Session fallback", currentUser.Code)
	}
	blockedLinkStart := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/google/start", jwtCookieAndSession, nil, "", "")
	if blockedLinkStart.Code != http.StatusUnauthorized {
		t.Errorf("link start with an empty JWT cookie and valid Session A returned %d, want 401 without Session fallback", blockedLinkStart.Code)
	}
	sessionOnlyLinkStart := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/google/start", []*http.Cookie{loginA.sessionCookie}, nil, "", "")
	if sessionOnlyLinkStart.Code != http.StatusOK {
		t.Errorf("link start with valid Session A only returned %d, want 200", sessionOnlyLinkStart.Code)
	}

	provider.user = goth.User{Provider: "google", UserID: "source-session-to-jwt", Email: "source-session-to-jwt@link.example.test", Name: "Source Switch"}
	entry := oauthAccountBeginLinkWithCookies(t, h, provider, []*http.Cookie{loginA.sessionCookie}, loginA.jwtCookie, "")
	switched := oauthAccountCallbackWithCookies(h, entry, "source-switch", echo.MIMEApplicationJSON, []*http.Cookie{loginA.jwtCookie})
	if switched.Code < 400 || switched.Code >= 500 {
		t.Errorf("callback switching from Session-only start to its paired JWT returned %d, want rejection", switched.Code)
	}
	if oauthAccountSubjectOwner(t, h.db, "google", "source-session-to-jwt") != 0 {
		t.Error("source-switched callback created an OAuth identity")
	}

	provider.user = goth.User{Provider: "google", UserID: "source-priority-rejected", Email: "source-priority-rejected@link.example.test", Name: "Source Priority"}
	startCookies := []*http.Cookie{loginA.jwtCookie, loginB.sessionCookie}
	entry = oauthAccountBeginLinkWithCookies(t, h, provider, startCookies, nil, "")
	otherSource := oauthAccountCallbackWithCookies(h, entry, "source-b-only", echo.MIMEApplicationJSON, []*http.Cookie{loginB.sessionCookie})
	if otherSource.Code < 400 || otherSource.Code >= 500 {
		t.Errorf("callback using lower-priority Session B after JWT A start returned %d, want rejection", otherSource.Code)
	}
	if oauthAccountSubjectOwner(t, h.db, "google", "source-priority-rejected") != 0 {
		t.Error("callback using lower-priority Session B created an OAuth identity")
	}

	provider.user = goth.User{Provider: "google", UserID: "source-priority-a", Email: "source-priority-a@link.example.test", Name: "JWT A Wins"}
	entry = oauthAccountBeginLinkWithCookies(t, h, provider, startCookies, nil, "")
	selectedA := oauthAccountCallbackWithCookies(h, entry, "source-a-wins", echo.MIMEApplicationJSON, startCookies)
	if selectedA.Code != http.StatusOK {
		t.Errorf("callback retaining JWT A plus lower-priority Session B returned %d, want 200", selectedA.Code)
	}
	if oauthAccountSubjectOwner(t, h.db, "google", "source-priority-a") != int64(user.ID) {
		t.Error("JWT A plus Session B did not preserve JWT A as the selected account login")
	}
}

func TestOAuthAccountLinkRevalidatesLoginAfterUpstreamLogout(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	base := newOAuthTxnIntegrationProvider("google")
	base.user = goth.User{Provider: "google", UserID: "logout-race-subject", Email: "raced@link.example.test", Name: "Logout Race"}
	provider := &oauthAccountBarrierProvider{oauthTxnIntegrationProvider: base, fetching: make(chan struct{}), releaseCh: make(chan struct{})}
	resetOAuthInputProviders(provider)
	user := h.user("author", "oauth-account-logout-race")
	login := oauthAccountLoginFor(t, h, user)
	entry := oauthAccountBeginLink(t, h, provider, login, "")
	run := oauthAccountStartBarrierCallback(t, h, provider, entry, "logout-race")
	select {
	case <-provider.fetching:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not reach the controlled upstream wait")
	}

	logout := oauthAccountServe(h, http.MethodPost, "/auth/logout", []*http.Cookie{login.jwtCookie}, nil, "", "")
	if logout.Code != http.StatusOK && logout.Code != http.StatusNoContent && logout.Code != http.StatusFound {
		t.Fatalf("public logout while provider was waiting returned %d, want successful logout", logout.Code)
	}
	provider.releaseOnce.Do(func() { close(provider.releaseCh) })
	callback := oauthAccountWaitBarrierCallback(t, run)
	if callback.Code < 400 || callback.Code >= 500 {
		t.Errorf("callback after logout returned %d, want rejection", callback.Code)
	}
	if got := oauthAccountActiveIdentityCountOrZero(t, h.db, user.ID); got != 0 {
		t.Errorf("callback after logout created %d active identity rows", got)
	}
}

func TestOAuthAccountLinkRevalidatesShortJWTExpiryAfterUpstreamWait(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	base := newOAuthTxnIntegrationProvider("google")
	base.user = goth.User{Provider: "google", UserID: "expired-during-upstream-subject", Email: "expired@link.example.test", Name: "Expiry Race"}
	provider := &oauthAccountBarrierProvider{oauthTxnIntegrationProvider: base, fetching: make(chan struct{}), releaseCh: make(chan struct{})}
	resetOAuthInputProviders(provider)
	user := h.user("author", "oauth-account-expiry-race")
	login := oauthAccountLoginFor(t, h, user)
	shortToken, expiresAt := oauthAccountShortenJWT(t, login.token, 4*time.Second)
	shortCookie := *login.jwtCookie
	shortCookie.Value = shortToken
	entry := oauthAccountBeginLinkWithCookies(t, h, provider, []*http.Cookie{&shortCookie}, &shortCookie, "")
	run := oauthAccountStartBarrierCallback(t, h, provider, entry, "expiry-race")
	select {
	case <-provider.fetching:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not reach the controlled upstream wait before JWT expiry")
	}
	if pause := time.Until(expiresAt.Add(100 * time.Millisecond)); pause > 0 {
		timer := time.NewTimer(pause)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-time.After(5 * time.Second):
			t.Fatal("waiting for the short JWT expiry exceeded the test bound")
		}
	}
	provider.releaseOnce.Do(func() { close(provider.releaseCh) })
	callback := oauthAccountWaitBarrierCallback(t, run)
	if callback.Code < 400 || callback.Code >= 500 {
		t.Errorf("callback after JWT expiry returned %d, want rejection", callback.Code)
	}
	if oauthAccountActiveIdentityCountOrZero(t, h.db, user.ID) != 0 {
		t.Error("callback after JWT expiry created an OAuth identity")
	}
}

func TestOAuthAccountUnlinkRetainsHistoryAndEnforcesBackupLogin(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	google := newOAuthTxnIntegrationProvider("google")
	github := newOAuthTxnIntegrationProvider("github")
	resetOAuthInputProviders(google, github)
	user := h.user("author", "oauth-account-unlink")
	login := oauthAccountLoginFor(t, h, user)
	if err := oauthAccountInsertIdentity(h.db, user.ID, "google", "unlink-google"); err != nil {
		t.Fatalf("seed Google identity: %v", err)
	}
	if err := oauthAccountInsertIdentity(h.db, user.ID, "github", "unlink-github"); err != nil {
		t.Fatalf("seed GitHub identity: %v", err)
	}
	other := h.user("author", "oauth-account-unlink-other")
	if err := oauthAccountInsertIdentity(h.db, other.ID, "google", "unlink-other-google"); err != nil {
		t.Fatalf("seed another user's Google identity: %v", err)
	}
	response := oauthAccountServe(h, http.MethodDelete, "/api/auth/identities/google", []*http.Cookie{login.jwtCookie}, nil, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("unlink Google with GitHub backup returned %d, want 200", response.Code)
	}
	if active, history := oauthAccountIdentityCounts(t, h.db, user.ID, "google"); active != 0 || history != 1 {
		t.Errorf("Google unlink active/history=%d/%d, want 0/1", active, history)
	}
	second := oauthAccountServe(h, http.MethodDelete, "/api/auth/identities/google", []*http.Cookie{login.jwtCookie}, nil, "", "")
	if second.Code != http.StatusOK {
		t.Errorf("idempotent unlink returned %d, want 200", second.Code)
	}
	if active, history := oauthAccountIdentityCounts(t, h.db, user.ID, "google"); active != 0 || history != 1 {
		t.Errorf("repeated unlink active/history=%d/%d, want 0/1", active, history)
	}
	if active, history := oauthAccountIdentityCounts(t, h.db, other.ID, "google"); active != 1 || history != 1 {
		t.Errorf("unlink changed another user's Google identity active/history=%d/%d, want 1/1", active, history)
	}
	listed := oauthAccountServe(h, http.MethodGet, "/api/auth/identities", []*http.Cookie{login.jwtCookie}, nil, "", "")
	var state struct {
		Providers []struct {
			Provider string `json:"provider"`
			Linked   bool   `json:"linked"`
		} `json:"providers"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &state) != nil {
		t.Fatalf("identity list after unlink returned %d: %s", listed.Code, listed.Body.String())
	}
	for _, p := range state.Providers {
		if p.Provider == "google" && p.Linked || p.Provider == "github" && !p.Linked {
			t.Errorf("post-unlink provider state is wrong: %+v", p)
		}
	}

	for i, name := range []string{"last OAuth identity", "local provider without password", "disabled provider is not backup", "OAuth source with nonempty password"} {
		t.Run(name, func(t *testing.T) {
			fresh := h.user("author", fmt.Sprintf("oa-last-%d", i))
			freshLogin := oauthAccountLoginFor(t, h, fresh)
			if name == "local provider without password" {
				if err := h.db.Model(&core.User{}).Where("id = ?", fresh.ID).Update("provider", "local").Error; err != nil {
					t.Fatal(err)
				}
			}
			if name == "OAuth source with nonempty password" {
				if err := h.db.Model(&core.User{}).Where("id = ?", fresh.ID).Update("provider", "google").Error; err != nil {
					t.Fatal(err)
				}
				oauthAccountSetPassword(t, h, fresh.ID, "oauth-password-does-not-count")
			}
			if err := oauthAccountInsertIdentity(h.db, fresh.ID, "google", "last-google-"+fmt.Sprint(fresh.ID)); err != nil {
				t.Fatal(err)
			}
			if name == "disabled provider is not backup" {
				if err := oauthAccountInsertIdentity(h.db, fresh.ID, "github", "disabled-github-"+fmt.Sprint(fresh.ID)); err != nil {
					t.Fatal(err)
				}
				resetOAuthInputProviders(google)
			}
			rec := oauthAccountServe(h, http.MethodDelete, "/api/auth/identities/google", []*http.Cookie{freshLogin.jwtCookie}, nil, "", "")
			if rec.Code != http.StatusConflict {
				t.Errorf("unlink without an available backup returned %d, want 409", rec.Code)
			}
			if active, _ := oauthAccountIdentityCounts(t, h.db, fresh.ID, "google"); active != 1 {
				t.Errorf("rejected unlink removed the only active identity")
			}
		})
	}
	passwordUser := h.user("author", "oauth-account-password-backup")
	passwordLogin := oauthAccountLoginFor(t, h, passwordUser)
	oauthAccountSetPassword(t, h, passwordUser.ID, "real-local-password")
	if err := oauthAccountInsertIdentity(h.db, passwordUser.ID, "google", "password-backup-google"); err != nil {
		t.Fatal(err)
	}
	passwordUnlink := oauthAccountServe(h, http.MethodDelete, "/api/auth/identities/google", []*http.Cookie{passwordLogin.jwtCookie}, nil, "", "")
	if passwordUnlink.Code != http.StatusOK {
		t.Errorf("unlink with a valid local password backup returned %d, want 200", passwordUnlink.Code)
	}
}

func TestOAuthAccountUnlinkConcurrentAndCrossOriginWrites(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	google := newOAuthTxnIntegrationProvider("google")
	github := newOAuthTxnIntegrationProvider("github")
	resetOAuthInputProviders(google, github)
	user := h.user("author", "oauth-account-concurrent-unlink")
	login := oauthAccountLoginFor(t, h, user)
	if err := oauthAccountInsertIdentity(h.db, user.ID, "google", "parallel-google"); err != nil {
		t.Fatal(err)
	}
	if err := oauthAccountInsertIdentity(h.db, user.ID, "github", "parallel-github"); err != nil {
		t.Fatal(err)
	}

	crossOrigin := oauthAccountServe(h, http.MethodDelete, "/api/auth/identities/google", []*http.Cookie{login.jwtCookie}, nil, "", "https://attacker.invalid")
	if crossOrigin.Code != http.StatusForbidden {
		t.Errorf("cross-origin DELETE returned %d, want 403", crossOrigin.Code)
	}
	if active, _ := oauthAccountIdentityCounts(t, h.db, user.ID, "google"); active != 1 {
		t.Error("cross-origin DELETE changed the identity")
	}
	startCrossOrigin := oauthAccountServe(h, http.MethodPost, "/api/auth/identities/google/start", []*http.Cookie{login.jwtCookie}, nil, "", "https://attacker.invalid")
	if startCrossOrigin.Code != http.StatusForbidden {
		t.Errorf("cross-origin link start returned %d, want 403", startCrossOrigin.Code)
	}

	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, p := range []string{"google", "github"} {
		wg.Add(1)
		go func(provider string) {
			defer wg.Done()
			rec := oauthAccountServe(h, http.MethodDelete, "/api/auth/identities/"+provider, []*http.Cookie{login.jwtCookie}, nil, "", "")
			results <- rec.Code
		}(p)
	}
	wg.Wait()
	close(results)
	oks, conflicts := 0, 0
	for status := range results {
		if status == http.StatusOK {
			oks++
		}
		if status == http.StatusConflict {
			conflicts++
		}
	}
	if oks != 1 || conflicts != 1 {
		t.Errorf("parallel removal returned %d successes and %d conflicts, want exactly one each", oks, conflicts)
	}
	var active int64
	if err := h.db.Raw("SELECT count(*) FROM oauth_identities WHERE user_id = ? AND deleted_at IS NULL", user.ID).Scan(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Errorf("parallel unlink left %d active identities, want one", active)
	}
}

func TestOAuthAccountAssociationWriteFailureIsGenericAndAtomic(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := oauthTxnPrepareHarness(t)
	provider := newOAuthTxnIntegrationProvider("google")
	provider.user = goth.User{Provider: "google", UserID: "write-fault-subject", Email: "write-fault@link.example.test", Name: "Write Fault"}
	resetOAuthInputProviders(provider)
	user := h.user("author", "oauth-account-write-failure")
	login := oauthAccountLoginFor(t, h, user)
	beforeUsers := accessUsers(t, h.db)
	entry := oauthAccountBeginLink(t, h, provider, login, "")
	const triggerName = "test_oauth_account_insert_failure"
	const functionName = "test_oauth_account_insert_failure_fn"
	if err := h.db.Exec("CREATE FUNCTION " + functionName + "() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture private-write-failure-secret'; END $$").Error; err != nil {
		t.Fatalf("create isolated identity write fault: %v", err)
	}
	if err := h.db.Exec("CREATE TRIGGER " + triggerName + " BEFORE INSERT ON oauth_identities FOR EACH ROW EXECUTE FUNCTION " + functionName + "()").Error; err != nil {
		t.Fatalf("attach isolated identity write fault: %v", err)
	}
	t.Cleanup(func() {
		if err := h.db.Exec("DROP TRIGGER IF EXISTS " + triggerName + " ON oauth_identities").Error; err != nil {
			t.Errorf("remove identity write trigger: %v", err)
		}
		if err := h.db.Exec("DROP FUNCTION IF EXISTS " + functionName + "()").Error; err != nil {
			t.Errorf("remove identity write fault function: %v", err)
		}
	})
	callback := oauthAccountCallback(h, entry, "write-fault", true)
	if callback.Code != http.StatusServiceUnavailable {
		t.Errorf("injected identity write failure returned %d, want 503", callback.Code)
	}
	if strings.Contains(callback.Body.String(), "private-write-failure-secret") || strings.Contains(callback.Body.String(), "write-fault-subject") {
		t.Error("write failure response exposed internal identity data")
	}
	if got := accessUsers(t, h.db); !reflect.DeepEqual(beforeUsers, got) {
		t.Error("failed link changed User rows")
	}
	if got := oauthAccountActiveIdentityCountOrZero(t, h.db, user.ID); got != 0 {
		t.Errorf("failed link left %d active identity rows", got)
	}
}

type oauthAccountLogin struct {
	token         string
	jwtCookie     *http.Cookie
	sessionCookie *http.Cookie
}

func oauthAccountLoginFor(t *testing.T, h *accessHarness, user core.User) oauthAccountLogin {
	t.Helper()
	token := h.token(user)
	return oauthAccountLogin{token: token, jwtCookie: &http.Cookie{Name: "jwt_token", Value: token, Path: "/", HttpOnly: true}}
}

func oauthAccountPublicLoginFor(t *testing.T, h *accessHarness, user core.User, password string) oauthAccountLogin {
	t.Helper()
	result := h.request(http.MethodPost, "/auth/login", fmt.Sprintf(`{"username":%q,"password":%q}`, user.Username, password), "", echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if result.code != http.StatusOK {
		t.Fatalf("public local login returned %d: %s", result.code, result.body)
	}
	var login oauthAccountLogin
	for _, cookie := range (&http.Response{Header: result.header}).Cookies() {
		copy := *cookie
		switch cookie.Name {
		case "jwt_token":
			login.jwtCookie = &copy
			login.token = cookie.Value
		case "jelly-cms-session":
			login.sessionCookie = &copy
		}
	}
	if login.jwtCookie == nil || login.sessionCookie == nil || login.token == "" {
		t.Fatalf("public local login omitted its paired JWT/Session cookies: %v", result.header.Values(echo.HeaderSetCookie))
	}
	return login
}

func oauthAccountSetPassword(t *testing.T, h *accessHarness, userID uint, password string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password fixture: %v", err)
	}
	if err := h.db.Model(&core.User{}).Where("id = ?", userID).Update("password", string(hash)).Error; err != nil {
		t.Fatalf("save password fixture: %v", err)
	}
}

func oauthAccountServe(h *accessHarness, method, path string, cookies []*http.Cookie, body []byte, authorization, origin string) *httptest.ResponseRecorder {
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(string(body))
	}
	req := httptest.NewRequest(method, path, reader)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		req.Header.Set("X-Jelly-CSRF", "1")
	}
	if authorization != "" {
		req.Header.Set(echo.HeaderAuthorization, authorization)
	}
	if origin != "" {
		req.Header.Set(echo.HeaderOrigin, origin)
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func oauthAccountBeginLink(t *testing.T, h *accessHarness, provider goth.Provider, login oauthAccountLogin, query string) oauthHTTPEntry {
	return oauthAccountBeginLinkWithCookies(t, h, provider, []*http.Cookie{login.jwtCookie}, login.jwtCookie, query)
}

func oauthAccountBeginLinkWithCookies(t *testing.T, h *accessHarness, provider goth.Provider, startCookies []*http.Cookie, callbackCookie *http.Cookie, query string) oauthHTTPEntry {
	t.Helper()
	path := "/api/auth/identities/" + provider.Name() + "/start"
	if query != "" {
		path += "?" + query
	}
	rec := oauthAccountServe(h, http.MethodPost, path, startCookies, nil, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("link start returned %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AuthURL string `json:"auth_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.AuthURL == "" {
		t.Fatalf("decode link auth_url: %s (%v)", rec.Body.String(), err)
	}
	authURL, err := url.Parse(response.AuthURL)
	if err != nil {
		t.Fatalf("parse link auth_url: %v", err)
	}
	state := authURL.Query().Get("state")
	gothCookie := oauthTxnResponseCookie(rec, gothic.SessionName)
	if state == "" || gothCookie == nil {
		t.Fatalf("link start omitted state or signed Goth cookie: url=%q", response.AuthURL)
	}
	return oauthHTTPEntry{provider: provider.Name(), state: state, gothCookie: gothCookie, appCookie: callbackCookie}
}

func oauthAccountCallback(h *accessHarness, entry oauthHTTPEntry, code string, jsonResponse bool) *httptest.ResponseRecorder {
	accept := "text/html"
	if jsonResponse {
		accept = echo.MIMEApplicationJSON
	}
	return oauthAccountCallbackWithAcceptAndLogin(h, entry, code, accept, entry.appCookie)
}

func oauthAccountCallbackWithLogin(h *accessHarness, entry oauthHTTPEntry, code string, loginCookie *http.Cookie) *httptest.ResponseRecorder {
	return oauthAccountCallbackWithAcceptAndLogin(h, entry, code, echo.MIMEApplicationJSON, loginCookie)
}

func oauthAccountCallbackWithAcceptAndLogin(h *accessHarness, entry oauthHTTPEntry, code, accept string, loginCookie *http.Cookie) *httptest.ResponseRecorder {
	var cookies []*http.Cookie
	if loginCookie != nil {
		cookies = append(cookies, loginCookie)
	}
	return oauthAccountCallbackWithCookies(h, entry, code, accept, cookies)
}

func oauthAccountCallbackWithCookies(h *accessHarness, entry oauthHTTPEntry, code, accept string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/auth/"+entry.provider+"/callback?state="+url.QueryEscape(entry.state)+"&code="+url.QueryEscape(code), nil)
	req.Header.Set(echo.HeaderAccept, accept)
	req.AddCookie(entry.gothCookie)
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func oauthAccountBeginNormal(t *testing.T, h *accessHarness, provider goth.Provider, login oauthAccountLogin, query string) oauthHTTPEntry {
	t.Helper()
	rec := oauthAccountServe(h, http.MethodGet, "/auth/"+provider.Name()+"?"+query, []*http.Cookie{login.jwtCookie}, nil, "", "")
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("normal OAuth start returned %d, want 307", rec.Code)
	}
	location, err := url.Parse(rec.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("parse normal OAuth URL: %v", err)
	}
	state := location.Query().Get("state")
	gothCookie := oauthTxnResponseCookie(rec, gothic.SessionName)
	if state == "" || gothCookie == nil {
		t.Fatal("normal OAuth start omitted state or signed cookie")
	}
	return oauthHTTPEntry{provider: provider.Name(), state: state, gothCookie: gothCookie, appCookie: login.jwtCookie}
}

func oauthAccountInsertIdentity(db *gorm.DB, userID uint, provider, subject string) error {
	return db.Exec("INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, ?, ?)", userID, provider, subject).Error
}

func oauthAccountActiveIdentityCount(db *gorm.DB, userID uint) (int64, error) {
	var count int64
	err := db.Raw("SELECT count(*) FROM oauth_identities WHERE user_id = ? AND deleted_at IS NULL", userID).Scan(&count).Error
	return count, err
}

func oauthAccountActiveIdentityCountOrZero(t *testing.T, db *gorm.DB, userID uint) int64 {
	t.Helper()
	count, err := oauthAccountActiveIdentityCount(db, userID)
	if err != nil {
		t.Fatalf("read active OAuth identities for user %d: %v", userID, err)
	}
	return count
}

func oauthAccountSubjectOwner(t *testing.T, db *gorm.DB, provider, subject string) int64 {
	t.Helper()
	var id int64
	if err := db.Raw("SELECT user_id FROM oauth_identities WHERE provider = ? AND subject = ? AND deleted_at IS NULL", provider, subject).Scan(&id).Error; err != nil {
		t.Fatalf("read OAuth subject owner: %v", err)
	}
	return id
}

func oauthAccountIdentityCounts(t *testing.T, db *gorm.DB, userID uint, provider string) (active, history int64) {
	t.Helper()
	if err := db.Raw("SELECT count(*) FILTER (WHERE deleted_at IS NULL), count(*) FROM oauth_identities WHERE user_id = ? AND provider = ?", userID, provider).Row().Scan(&active, &history); err != nil {
		t.Fatalf("read identity active/history counts: %v", err)
	}
	return active, history
}

func oauthAccountHasLoginCookie(rec *httptest.ResponseRecorder) bool {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == "jwt_token" || cookie.Name == "jelly-cms-session" {
			return true
		}
	}
	return false
}

type oauthAccountBarrierProvider struct {
	*oauthTxnIntegrationProvider
	fetching    chan struct{}
	releaseCh   chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func (p *oauthAccountBarrierProvider) FetchUser(session goth.Session) (goth.User, error) {
	user, err := p.oauthTxnIntegrationProvider.FetchUser(session)
	p.once.Do(func() { close(p.fetching) })
	<-p.releaseCh
	return user, err
}

type oauthAccountCallbackRun struct {
	result chan *httptest.ResponseRecorder
	done   chan struct{}
}

func oauthAccountStartBarrierCallback(t *testing.T, h *accessHarness, provider *oauthAccountBarrierProvider, entry oauthHTTPEntry, code string) *oauthAccountCallbackRun {
	t.Helper()
	run := &oauthAccountCallbackRun{result: make(chan *httptest.ResponseRecorder, 1), done: make(chan struct{})}
	t.Cleanup(func() {
		provider.releaseOnce.Do(func() { close(provider.releaseCh) })
		select {
		case <-run.done:
		case <-time.After(5 * time.Second):
			t.Errorf("OAuth callback handler did not finish within the cleanup bound")
		}
	})
	go func() {
		defer close(run.done)
		run.result <- oauthAccountCallback(h, entry, code, true)
	}()
	return run
}

func oauthAccountWaitBarrierCallback(t *testing.T, run *oauthAccountCallbackRun) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case result := <-run.result:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("OAuth callback handler did not finish within the test bound")
		return nil
	}
}

func oauthAccountShortenJWT(t *testing.T, token string, lifetime time.Duration) (string, time.Time) {
	t.Helper()
	parsed, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		t.Fatalf("parse fixture JWT claims: %v", err)
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatalf("fixture JWT claims have unexpected type %T", parsed.Claims)
	}
	expiresAt := time.Now().Add(lifetime).Truncate(time.Second)
	if !expiresAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("short JWT lifetime %s is too small for the test", lifetime)
	}
	claims["iat"] = time.Now().Add(-time.Minute).Unix()
	claims["exp"] = expiresAt.Unix()
	shortToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(oauthTxnJWTSecret))
	if err != nil {
		t.Fatalf("sign short-lived fixture JWT: %v", err)
	}
	return shortToken, expiresAt
}

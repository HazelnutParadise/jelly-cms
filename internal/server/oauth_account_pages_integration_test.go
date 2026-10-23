package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
	nethtml "golang.org/x/net/html"
)

func TestOAuthAccountPagePublicGETForEveryAuthenticatedRole(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := newAccessHarness(t)

	for _, role := range []string{"author", "editor", "admin"} {
		t.Run(role, func(t *testing.T) {
			user := h.user(role, "oauth-account-page-"+role)
			subjectSentinel := "oauth-account-page-subject-" + role + "-must-not-render"
			oauthAccountSetPassword(t, h, user.ID, "page-test-local-password")
			if err := h.db.Exec("INSERT INTO oauth_identities (user_id, provider, subject) VALUES (?, ?, ?)", user.ID, "google", subjectSentinel).Error; err != nil {
				t.Fatalf("seed provider identity fixture: %v", err)
			}
			var stored core.User
			if err := h.db.First(&stored, user.ID).Error; err != nil {
				t.Fatalf("read seeded account: %v", err)
			}
			loginToken := h.token(user)
			for _, language := range []struct{ query, lang, heading string }{
				{query: "zh-TW", lang: "zh-TW", heading: "登入方式"},
				{query: "en", lang: "en", heading: "Login methods"},
			} {
				t.Run(language.lang, func(t *testing.T) {
					target := "/account?lang=" + url.QueryEscape(language.query)
					rec := oauthAccountPageGET(h, target, loginToken)
					if rec.Code != http.StatusOK {
						t.Fatalf("GET %s returned %d, want 200 HTML: %s", target, rec.Code, rec.Body.String())
					}
					oauthAccountPageAssertNoStore(t, rec)
					doc := oauthAccountPageParseHTML(t, rec)
					title, mainText := oauthAccountPageTitleAndMain(doc)
					if title == "" || strings.EqualFold(title, "title") {
						t.Errorf("account page title is empty or an untranslated key: %q", title)
					}
					if !strings.Contains(mainText, language.heading) {
						t.Errorf("account page main content %q lacks localized heading %q", mainText, language.heading)
					}
					if got := oauthAccountPageHTMLLang(doc); got != language.lang {
						t.Errorf("account page html lang=%q, want %q", got, language.lang)
					}
					if strings.TrimSpace(rec.Body.String()) == "" {
						t.Fatal("account page returned blank HTML")
					}
					oauthAccountPageAssertNoCredentialLeak(t, rec.Body.String(), []string{
						stored.Password, subjectSentinel, loginToken,
						oauthTxnSessionSecret, oauthTxnJWTSecret, "page-test-local-password",
					})
				})
			}
		})
	}
}

func TestOAuthAccountPageAnonymousRedirectPreservesCompleteAccountPath(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := newAccessHarness(t)

	target := "/account?tab=security&next=%2Fposts%3Fpage%3D2"
	rec := oauthAccountPageGET(h, target, "")
	if rec.Code != http.StatusFound {
		t.Fatalf("anonymous GET %s returned %d, want 302 to login: %s", target, rec.Code, rec.Body.String())
	}
	oauthAccountPageAssertNoStore(t, rec)
	location, err := url.Parse(rec.Header().Get(echo.HeaderLocation))
	if err != nil {
		t.Fatalf("parse anonymous account redirect: %v", err)
	}
	if location.Path != "/login" {
		t.Errorf("anonymous account redirect path=%q, want /login", location.Path)
	}
	if got := location.Query().Get("redirect"); got != target {
		t.Errorf("login redirect target=%q, want complete in-site path %q", got, target)
	}
}

func TestOAuthRecoveryPagePublicGETHasBodyOnlyPasswordFormInBothLanguages(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthRecoveryJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := newAccessHarness(t)
	queryGrant := "recovery-query-must-not-prefill-or-reflect"

	for _, language := range []struct{ lang, heading, button string }{
		{lang: "zh-TW", heading: "復原第三方帳號", button: "繼續"},
		{lang: "en", heading: "Recover OAuth account", button: "Continue"},
	} {
		t.Run(language.lang, func(t *testing.T) {
			target := "/account/recover?lang=" + url.QueryEscape(language.lang) + "&grant=" + url.QueryEscape(queryGrant)
			rec := oauthAccountPageGET(h, target, "")
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s returned %d, want 200 HTML: %s", target, rec.Code, rec.Body.String())
			}
			oauthAccountPageAssertNoStore(t, rec)
			doc := oauthAccountPageParseHTML(t, rec)
			title, mainText := oauthAccountPageTitleAndMain(doc)
			if title == "" || strings.EqualFold(title, "title") {
				t.Errorf("recovery page title is empty or an untranslated key: %q", title)
			}
			if !strings.Contains(mainText, language.heading) {
				t.Errorf("recovery page main content %q lacks localized heading %q", mainText, language.heading)
			}
			if !strings.Contains(mainText, language.button) {
				t.Errorf("recovery page main content %q lacks localized submit label %q", mainText, language.button)
			}
			if got := oauthAccountPageHTMLLang(doc); got != language.lang {
				t.Errorf("recovery page html lang=%q, want %q", got, language.lang)
			}
			if strings.TrimSpace(rec.Body.String()) == "" {
				t.Fatal("recovery page returned blank HTML")
			}
			form, grantInput := oauthAccountPageRecoveryForm(doc)
			if form == nil {
				t.Fatal("recovery page has no semantic form")
			}
			if !oauthAccountPageHasSubmitButton(form, language.button) {
				t.Errorf("recovery form has no submit control labeled %q", language.button)
			}
			if got := strings.ToLower(oauthAccountPageAttr(form, "method")); got != "post" {
				t.Errorf("recovery form method=%q, want post", got)
			}
			if got := oauthAccountPageAttr(form, "action"); got != "/auth/recover" {
				t.Errorf("recovery form action=%q, want /auth/recover", got)
			}
			if grantInput == nil {
				t.Fatal("recovery form has no grant input")
			}
			if got := strings.ToLower(oauthAccountPageAttr(grantInput, "type")); got != "password" {
				t.Errorf("recovery grant input type=%q, want password", got)
			}
			inputID := oauthAccountPageAttr(grantInput, "id")
			if inputID == "" || !oauthAccountPageHasLabelFor(doc, inputID) {
				t.Errorf("password input id=%q has no explicitly associated label", inputID)
			}
			if oauthAccountPageAttr(grantInput, "value") != "" {
				t.Error("recovery grant input is prefilled")
			}
			if strings.Contains(rec.Body.String(), queryGrant) {
				t.Error("recovery query grant was reflected into HTML")
			}
			oauthAccountPageAssertNoCredentialLeak(t, rec.Body.String(), []string{
				queryGrant, oauthRecoveryJWTSecret, oauthTxnSessionSecret, oauthTxnJWTSecret,
			})
		})
	}
}

func TestOAuthAccountPageEntryLinksAreDiscoverableByHref(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := newAccessHarness(t)
	user := h.user("admin", "oauth-account-entry-admin")
	token := h.token(user)
	pages := map[string]*nethtml.Node{}
	for _, target := range []string{"/login", "/admin"} {
		var rec *httptest.ResponseRecorder
		if target == "/login" {
			rec = oauthAccountPageGET(h, target, "")
		} else {
			rec = oauthAccountPageGET(h, target, token)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s returned %d, want HTML for navigation check: %s", target, rec.Code, rec.Body.String())
		}
		pages[target] = oauthAccountPageParseHTML(t, rec)
	}
	if !oauthAccountPageHasHref(pages, "/account") {
		t.Error("login or authenticated backend HTML has no anchor href to /account")
	}
	if !oauthAccountPageHasHref(pages, "/account/recover") {
		t.Error("login or authenticated backend HTML has no anchor href to /account/recover")
	}
}

func TestOAuthAccountPageLanguageLinksPreserveSelectedLanguage(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := newAccessHarness(t)
	user := h.user("admin", "oauth-account-language-links")
	token := h.token(user)

	for _, language := range []struct{ lang, heading string }{
		{lang: "en", heading: "Recover OAuth account"},
		{lang: "zh-TW", heading: "復原第三方帳號"},
	} {
		t.Run(language.lang, func(t *testing.T) {
			account := oauthAccountPageGET(h, "/account?lang="+url.QueryEscape(language.lang), token)
			if account.Code != http.StatusOK {
				t.Errorf("GET /account for %s returned %d: %s", language.lang, account.Code, account.Body.String())
				return
			}
			accountDoc := oauthAccountPageParseHTML(t, account)
			recoverURL, ok := oauthAccountPageHref(accountDoc, "/account/recover")
			if !ok {
				t.Errorf("%s account page has no same-site /account/recover link", language.lang)
				return
			}

			// Follow the actual href without carrying a language cookie so this checks the link itself.
			recovery := oauthAccountPageGET(h, recoverURL.RequestURI(), "")
			if recovery.Code != http.StatusOK {
				t.Errorf("following account recovery href %q returned %d: %s", recoverURL.String(), recovery.Code, recovery.Body.String())
				return
			}
			recoveryDoc := oauthAccountPageParseHTML(t, recovery)
			if got := oauthAccountPageHTMLLang(recoveryDoc); got != language.lang {
				t.Errorf("following %q rendered lang=%q, want %q", recoverURL.String(), got, language.lang)
			}
			_, recoveryMain := oauthAccountPageTitleAndMain(recoveryDoc)
			if !strings.Contains(recoveryMain, language.heading) {
				t.Errorf("following %q rendered recovery content %q, want %q", recoverURL.String(), recoveryMain, language.heading)
			}

			loginURL, ok := oauthAccountPageHref(recoveryDoc, "/login")
			if !ok {
				t.Errorf("%s recovery page has no same-site /login link", language.lang)
				return
			}
			login := oauthAccountPageGET(h, loginURL.RequestURI(), "")
			if login.Code != http.StatusOK {
				t.Errorf("following recovery login href %q returned %d: %s", loginURL.String(), login.Code, login.Body.String())
				return
			}
			loginDoc := oauthAccountPageParseHTML(t, login)
			if !oauthAccountPageHasLinkText(loginDoc, "/account/recover", language.heading) {
				t.Errorf("following %q did not render the %q recovery label", loginURL.String(), language.heading)
			}
		})
	}
}

func TestOAuthAccountPageLanguageCookiePersistsAndQueryOverrides(t *testing.T) {
	t.Setenv("SESSION_SECRET", oauthTxnSessionSecret)
	t.Setenv("JWT_SECRET", oauthTxnJWTSecret)
	t.Setenv("APP_ENV", "test")
	h := newAccessHarness(t)
	user := h.user("admin", "oauth-account-language-cookie")
	token := h.token(user)

	for _, language := range []struct{ lang, recoverLabel string }{
		{lang: "en", recoverLabel: "Recover OAuth account"},
		{lang: "zh-TW", recoverLabel: "復原第三方帳號"},
	} {
		t.Run(language.lang, func(t *testing.T) {
			oldLang := "en"
			if language.lang == "en" {
				oldLang = "zh-TW"
			}
			for _, source := range []struct {
				path, follow string
			}{
				{path: "/account", follow: "/account"},
				{path: "/account/recover", follow: "/login"},
			} {
				t.Run(source.path, func(t *testing.T) {
					request := source.path + "?lang=" + url.QueryEscape(language.lang)
					initial := oauthAccountPageGETWithCookies(h, request, token, &http.Cookie{Name: "lang", Value: oldLang, Path: "/"})
					if initial.Code != http.StatusOK {
						t.Errorf("GET %s returned %d: %s", request, initial.Code, initial.Body.String())
						return
					}
					initialDoc := oauthAccountPageParseHTML(t, initial)
					if got := oauthAccountPageHTMLLang(initialDoc); got != language.lang {
						t.Errorf("GET %s with old lang cookie %q rendered lang=%q, want query language %q", request, oldLang, got, language.lang)
					}

					var langCookie *http.Cookie
					for _, cookie := range initial.Result().Cookies() {
						if cookie.Name == "lang" {
							langCookie = cookie
							break
						}
					}
					if langCookie == nil {
						t.Errorf("GET %s did not return a lang cookie", request)
					} else {
						if langCookie.Value != language.lang {
							t.Errorf("GET %s returned lang cookie %q, want %q", request, langCookie.Value, language.lang)
						}
						if langCookie.Path != "" && langCookie.Path != "/" {
							t.Errorf("GET %s returned lang cookie path %q, which does not cover %s", request, langCookie.Path, source.follow)
						}
					}

					followToken := ""
					if source.follow == "/account" {
						followToken = token
					}
					followed := oauthAccountPageGETWithCookies(h, source.follow, followToken, langCookie)
					if followed.Code != http.StatusOK {
						t.Errorf("GET %s with response lang cookie returned %d: %s", source.follow, followed.Code, followed.Body.String())
						return
					}
					followedDoc := oauthAccountPageParseHTML(t, followed)
					if source.follow == "/login" {
						if !oauthAccountPageHasLinkText(followedDoc, "/account/recover", language.recoverLabel) {
							t.Errorf("GET %s with response lang cookie did not render recovery label %q", source.follow, language.recoverLabel)
						}
					} else if got := oauthAccountPageHTMLLang(followedDoc); got != language.lang {
						t.Errorf("GET %s with response lang cookie rendered lang=%q, want %q", source.follow, got, language.lang)
					}
				})
			}
		})
	}
}

func oauthAccountPageGET(h *accessHarness, target, token string) *httptest.ResponseRecorder {
	return oauthAccountPageGETWithCookies(h, target, token)
}

func oauthAccountPageGETWithCookies(h *accessHarness, target, token string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: "jwt_token", Value: token, Path: "/"})
	}
	for _, cookie := range cookies {
		if cookie != nil {
			req.AddCookie(cookie)
		}
	}
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	return rec
}

func oauthAccountPageAssertNoStore(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if !strings.Contains(strings.ToLower(rec.Header().Get(echo.HeaderCacheControl)), "no-store") {
		t.Errorf("Cache-Control=%q, want no-store", rec.Header().Get(echo.HeaderCacheControl))
	}
}

func oauthAccountPageParseHTML(t *testing.T, rec *httptest.ResponseRecorder) *nethtml.Node {
	t.Helper()
	if contentType := strings.ToLower(rec.Header().Get(echo.HeaderContentType)); !strings.Contains(contentType, "text/html") {
		t.Fatalf("Content-Type=%q, want HTML", contentType)
	}
	doc, err := nethtml.Parse(strings.NewReader(rec.Body.String()))
	if err != nil {
		t.Fatalf("parse HTML response: %v", err)
	}
	if oauthAccountPageFind(doc, "html") == nil || oauthAccountPageFind(doc, "body") == nil {
		t.Fatalf("response is not a complete HTML document: %q", rec.Body.String())
	}
	return doc
}

func oauthAccountPageTitleAndMain(doc *nethtml.Node) (string, string) {
	titleNode := oauthAccountPageFind(doc, "title")
	mainNode := oauthAccountPageFind(doc, "main")
	if titleNode == nil || mainNode == nil {
		return "", ""
	}
	return strings.TrimSpace(oauthAccountPageText(titleNode)), strings.TrimSpace(oauthAccountPageText(mainNode))
}

func oauthAccountPageHTMLLang(doc *nethtml.Node) string {
	node := oauthAccountPageFind(doc, "html")
	if node == nil {
		return ""
	}
	return oauthAccountPageAttr(node, "lang")
}

func oauthAccountPageRecoveryForm(doc *nethtml.Node) (*nethtml.Node, *nethtml.Node) {
	var form, input *nethtml.Node
	var visit func(*nethtml.Node)
	visit = func(node *nethtml.Node) {
		if node.Type == nethtml.ElementNode && node.Data == "form" && oauthAccountPageAttr(node, "action") == "/auth/recover" {
			form = node
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				var findInput func(*nethtml.Node)
				findInput = func(current *nethtml.Node) {
					if current.Type == nethtml.ElementNode && current.Data == "input" && oauthAccountPageAttr(current, "name") == "grant" {
						input = current
					}
					for nested := current.FirstChild; nested != nil; nested = nested.NextSibling {
						findInput(nested)
					}
				}
				findInput(child)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return form, input
}

func oauthAccountPageHasLabelFor(doc *nethtml.Node, id string) bool {
	var visit func(*nethtml.Node) bool
	visit = func(node *nethtml.Node) bool {
		if node.Type == nethtml.ElementNode && node.Data == "label" && oauthAccountPageAttr(node, "for") == id && strings.TrimSpace(oauthAccountPageText(node)) != "" {
			return true
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(doc)
}

func oauthAccountPageHasSubmitButton(form *nethtml.Node, label string) bool {
	var visit func(*nethtml.Node) bool
	visit = func(node *nethtml.Node) bool {
		if node.Type == nethtml.ElementNode && node.Data == "button" {
			typeValue := strings.ToLower(oauthAccountPageAttr(node, "type"))
			if (typeValue == "" || typeValue == "submit") && strings.TrimSpace(oauthAccountPageText(node)) == label {
				return true
			}
		}
		if node.Type == nethtml.ElementNode && node.Data == "input" && strings.EqualFold(oauthAccountPageAttr(node, "type"), "submit") && oauthAccountPageAttr(node, "value") == label {
			return true
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(form)
}

func oauthAccountPageHasHref(pages map[string]*nethtml.Node, href string) bool {
	want, err := url.Parse(href)
	if err != nil || want.Scheme != "" || want.Host != "" || want.Opaque != "" || !strings.HasPrefix(want.Path, "/") {
		return false
	}
	for _, doc := range pages {
		if _, ok := oauthAccountPageHref(doc, want.Path); ok {
			return true
		}
	}
	return false
}

func oauthAccountPageHref(doc *nethtml.Node, wantPath string) (*url.URL, bool) {
	var found *url.URL
	var visit func(*nethtml.Node)
	visit = func(node *nethtml.Node) {
		if found != nil {
			return
		}
		if node.Type == nethtml.ElementNode && node.Data == "a" && strings.TrimSpace(oauthAccountPageText(node)) != "" {
			href := oauthAccountPageAttr(node, "href")
			parsed, err := url.Parse(href)
			if err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.Opaque == "" && strings.HasPrefix(parsed.Path, "/") && parsed.Path == wantPath {
				found = parsed
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(doc)
	return found, found != nil
}

func oauthAccountPageHasLinkText(doc *nethtml.Node, wantPath, text string) bool {
	var visit func(*nethtml.Node) bool
	visit = func(node *nethtml.Node) bool {
		if node.Type == nethtml.ElementNode && node.Data == "a" {
			if parsed, err := url.Parse(oauthAccountPageAttr(node, "href")); err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.Opaque == "" && strings.HasPrefix(parsed.Path, "/") && parsed.Path == wantPath && strings.Contains(strings.TrimSpace(oauthAccountPageText(node)), text) {
				return true
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	return visit(doc)
}

func oauthAccountPageFind(node *nethtml.Node, tag string) *nethtml.Node {
	if node.Type == nethtml.ElementNode && node.Data == tag {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := oauthAccountPageFind(child, tag); found != nil {
			return found
		}
	}
	return nil
}

func oauthAccountPageText(node *nethtml.Node) string {
	if node.Type == nethtml.TextNode {
		return node.Data
	}
	var parts []string
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		parts = append(parts, oauthAccountPageText(child))
	}
	return strings.Join(parts, " ")
}

func oauthAccountPageAttr(node *nethtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func oauthAccountPageAssertNoCredentialLeak(t *testing.T, body string, secrets []string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(body, secret) {
			t.Errorf("HTML response contains credential, password hash, or OAuth subject sentinel %q", secret)
		}
	}
}

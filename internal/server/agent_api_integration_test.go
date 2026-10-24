package server

// Acceptance tests for ticket 14 (docs/tickets/14-agent-api.md): the agent-facing API under
// /api/agent/v1. Slots: A3 (key and permission middleware, reused /api/admin handlers, type scoping,
// base_updated_at version guard, error envelope and request id, settings masking, maintenance bypass),
// A4 (discovery index and OpenAPI 3.1 document from one route table), A5 (GET /logs) and A7 (the Agent
// Skill file). The contract is the 契約 section of the ticket plus its 「測試另外釘下的約定」; helpers
// live in agent_keys_integration_test.go.
//
// These tests are expected to fail now: every /api/agent/v1 and /api/admin/agents route answers 501
// and skills/jelly-cms/SKILL.md does not exist. Do not edit them to match an implementation. All but
// TestAgentSkillFileDescribesDiscovery need JELLY_TEST_POSTGRES=1 and the dedicated PostgreSQL container.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/labstack/echo/v4"
)

// agtRouteSet lists the registered Echo routes as "METHOD /path".
func agtRouteSet(h *accessHarness) map[string]bool {
	routes := map[string]bool{}
	for _, route := range h.echo.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	return routes
}

var agtBraceParam = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)

// agtEchoPath turns an OpenAPI-style path into Echo's ":param" form.
func agtEchoPath(path string) string {
	return agtBraceParam.ReplaceAllString(path, ":$1")
}

// agtSetup returns an admin and an agent holding the given permissions.
func agtSetup(t *testing.T, h *accessHarness, name string, permissions ...string) (core.User, string, agtCreated) {
	t.Helper()
	admin := h.user("admin", "agt-"+strings.ReplaceAll(strings.ToLower(name), " ", "-")+"-owner")
	token := h.token(admin)
	return admin, token, agtCreate(t, h, token, name, permissions...)
}

type agtIndexEndpoint struct {
	Method        string   `json:"method"`
	Path          string   `json:"path"`
	Permission    string   `json:"permission"`
	Summary       string   `json:"summary"`
	RequiredRoles []string `json:"required_roles"`
	VersionGuard  bool     `json:"version_guard"`
	Auth          string   `json:"auth"`
}

type agtIndex struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	BasePath       string `json:"base_path"`
	OpenAPIURL     string `json:"openapi_url"`
	Authentication struct {
		Type      string `json:"type"`
		Header    string `json:"header"`
		KeyPrefix string `json:"key_prefix"`
	} `json:"authentication"`
	Permissions []struct {
		Name          string             `json:"name"`
		Description   string             `json:"description"`
		RequiredRoles []string           `json:"required_roles"`
		Endpoints     []agtIndexEndpoint `json:"endpoints"`
	} `json:"permissions"`
	Endpoints []agtIndexEndpoint `json:"endpoints"`
	Errors    struct {
		Codes []struct {
			Code   string `json:"code"`
			Status int    `json:"status"`
		} `json:"codes"`
	} `json:"errors"`
	Examples []json.RawMessage `json:"examples"`
}

func TestAgentDiscoveryIndexNeedsNoKey(t *testing.T) {
	h := newAccessHarness(t)
	for _, bearer := range []string{"", "jla_not-a-real-key"} {
		r := agtDo(h, http.MethodGet, "/api/agent/v1", "", bearer, "")
		if r.code != http.StatusOK {
			t.Fatalf("GET /api/agent/v1 with bearer %q returned %d, want 200 without a key; body=%q", bearer, r.code, r.body)
		}
		if !strings.Contains(r.header.Get(echo.HeaderContentType), "application/json") {
			t.Errorf("GET /api/agent/v1 content type=%q, want JSON", r.header.Get(echo.HeaderContentType))
		}
	}
	var index agtIndex
	agtDecode(t, agtDo(h, http.MethodGet, "/api/agent/v1", "", "", ""), &index, "discovery index")

	if index.OpenAPIURL != "/api/agent/v1/openapi.json" || index.BasePath != "/api/agent/v1" {
		t.Errorf("openapi_url=%q base_path=%q, want /api/agent/v1/openapi.json and /api/agent/v1", index.OpenAPIURL, index.BasePath)
	}
	if index.Authentication.KeyPrefix != "jla_" || !strings.EqualFold(index.Authentication.Type, "bearer") || index.Authentication.Header != "Authorization" {
		t.Errorf("authentication=%+v, want bearer in Authorization with key_prefix jla_", index.Authentication)
	}
	if len(index.Examples) == 0 {
		t.Error("discovery index has no examples")
	}

	var names []string
	for _, p := range index.Permissions {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != strings.Join(agtAllPermissions, ",") {
		t.Fatalf("permissions=%v, want every permission in catalogue order %v", names, agtAllPermissions)
	}

	routes := agtRouteSet(h)
	byPerm := map[string][]agtIndexEndpoint{}
	for _, p := range index.Permissions {
		if len(p.Endpoints) == 0 {
			t.Errorf("permission %s lists no endpoints", p.Name)
		}
		if p.Description == "" {
			t.Errorf("permission %s has no description", p.Name)
		}
		for _, ep := range p.Endpoints {
			byPerm[p.Name] = append(byPerm[p.Name], ep)
			if !agtSameStrings(ep.RequiredRoles, p.RequiredRoles) && len(ep.RequiredRoles) > 0 {
				t.Errorf("%s %s required_roles=%v differ from permission %s required_roles=%v", ep.Method, ep.Path, ep.RequiredRoles, p.Name, p.RequiredRoles)
			}
		}
	}
	roleCases := map[string][]string{
		"posts.read": {"admin", "editor", "author"}, "posts.write": {"admin", "editor", "author"},
		"pages.write": {"admin", "editor"}, "media.write": {"admin", "editor", "author"},
		"products.write": {"admin"}, "orders.read": {"admin"}, "themes.write": {"admin"},
		"plugins.write": {"admin"}, "settings.read": {"admin"}, "settings.write": {"admin"}, "logs.read": {"admin"},
	}
	for _, p := range index.Permissions {
		if want, ok := roleCases[p.Name]; ok && !agtSameStrings(p.RequiredRoles, want) {
			t.Errorf("permission %s required_roles=%v, want %v (the role set of the reused /api/admin handler)", p.Name, p.RequiredRoles, want)
		}
	}
	mustList := map[string][]string{
		"posts.read":     {"GET /api/agent/v1/posts", "GET /api/agent/v1/posts/{id}"},
		"posts.write":    {"POST /api/agent/v1/posts", "PUT /api/agent/v1/posts/{id}", "DELETE /api/agent/v1/posts/{id}"},
		"pages.read":     {"GET /api/agent/v1/pages"},
		"pages.write":    {"PUT /api/agent/v1/pages/{id}"},
		"products.write": {"PUT /api/agent/v1/products/{id}"},
		"orders.read":    {"GET /api/agent/v1/orders"},
		"settings.read":  {"GET /api/agent/v1/settings"},
		"logs.read":      {"GET /api/agent/v1/logs"},
	}
	for perm, wants := range mustList {
		have := map[string]bool{}
		for _, ep := range byPerm[perm] {
			have[ep.Method+" "+ep.Path] = true
		}
		for _, want := range wants {
			if !have[want] {
				t.Errorf("permission %s does not list %s", perm, want)
			}
		}
	}
	for _, guarded := range []string{"PUT /api/agent/v1/posts/{id}", "PUT /api/agent/v1/pages/{id}", "PUT /api/agent/v1/products/{id}"} {
		found := false
		for _, ep := range index.Endpoints {
			if ep.Method+" "+ep.Path == guarded {
				found = true
				if !ep.VersionGuard {
					t.Errorf("%s is not marked version_guard", guarded)
				}
			}
		}
		if !found {
			t.Errorf("endpoints does not list %s", guarded)
		}
	}

	listed := map[string]agtIndexEndpoint{}
	for _, ep := range index.Endpoints {
		key := ep.Method + " " + ep.Path
		listed[key] = ep
		if !strings.HasPrefix(ep.Path, "/api/agent/v1") {
			t.Errorf("endpoint %s is outside /api/agent/v1", key)
		}
		if strings.Contains(ep.Path, "/agents") || strings.Contains(ep.Path, "rotate") {
			t.Errorf("endpoint %s manages agents or keys; agents must not manage agents", key)
		}
		if ep.Auth != "public" && ep.Auth != "key" {
			t.Errorf("endpoint %s auth=%q, want public or key", key, ep.Auth)
		}
		if !routes[ep.Method+" "+agtEchoPath(ep.Path)] {
			t.Errorf("endpoint %s is documented but no such route is registered", key)
		}
	}
	for perm, eps := range byPerm {
		for _, ep := range eps {
			if got, ok := listed[ep.Method+" "+ep.Path]; !ok || got.Permission != perm || got.Auth != "key" {
				t.Errorf("permission %s endpoint %s %s is missing from endpoints or disagrees (%+v)", perm, ep.Method, ep.Path, got)
			}
		}
	}
	for key, auth := range map[string]string{"GET /api/agent/v1": "public", "GET /api/agent/v1/openapi.json": "public", "GET /api/agent/v1/me": "key"} {
		if ep, ok := listed[key]; !ok || ep.Auth != auth || ep.Permission != "" {
			t.Errorf("endpoints entry %s = %+v, want auth %s and no permission", key, ep, auth)
		}
	}

	codes := map[string]int{}
	for _, c := range index.Errors.Codes {
		codes[c.Code] = c.Status
	}
	for code, status := range map[string]int{
		"key_missing": 401, "key_invalid": 401, "key_revoked": 401, "key_expired": 401, "agent_disabled": 401, "owner_unavailable": 401,
		"permission_missing": 403, "owner_role_insufficient": 403, "version_required": 400, "version_invalid": 400,
		"version_conflict": 409, "type_mismatch": 400, "logs_filter_invalid": 400, "not_found": 404, "method_not_allowed": 405,
	} {
		if codes[code] != status {
			t.Errorf("errors.codes[%s]=%d, want %d", code, codes[code], status)
		}
	}
}

func agtResolveRef(doc map[string]any, ref string) (any, bool) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	var node any = doc
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		m, ok := node.(map[string]any)
		if !ok {
			return nil, false
		}
		if node, ok = m[part]; !ok {
			return nil, false
		}
	}
	return node, true
}

func agtWalkRefs(node any, visit func(string)) {
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "$ref" {
				if s, ok := child.(string); ok {
					visit(s)
				}
				continue
			}
			agtWalkRefs(child, visit)
		}
	case []any:
		for _, child := range v {
			agtWalkRefs(child, visit)
		}
	}
}

func agtDeref(doc map[string]any, node any) any {
	for i := 0; i < 8; i++ {
		m, ok := node.(map[string]any)
		if !ok {
			return node
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return node
		}
		if node, ok = agtResolveRef(doc, ref); !ok {
			return nil
		}
	}
	return node
}

var agtStatusKey = regexp.MustCompile(`^([1-5][0-9X]{2}|default)$`)

func TestAgentOpenAPIDocumentIsValid31AndMatchesRoutes(t *testing.T) {
	h := newAccessHarness(t)
	r := agtDo(h, http.MethodGet, "/api/agent/v1/openapi.json", "", "", "")
	if r.code != http.StatusOK {
		t.Fatalf("GET /api/agent/v1/openapi.json without a key returned %d, want 200; body=%q", r.code, r.body)
	}
	var doc map[string]any
	agtDecode(t, r, &doc, "openapi.json")

	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi=%v, want 3.1.0", doc["openapi"])
	}
	allowedTop := map[string]bool{"openapi": true, "info": true, "jsonSchemaDialect": true, "servers": true, "paths": true, "webhooks": true, "components": true, "security": true, "tags": true, "externalDocs": true}
	for key := range doc {
		if !allowedTop[key] && !strings.HasPrefix(key, "x-") {
			t.Errorf("top-level key %q is not allowed by OpenAPI 3.1", key)
		}
	}
	info, _ := doc["info"].(map[string]any)
	if s, _ := info["title"].(string); s == "" {
		t.Error("info.title is missing")
	}
	if s, _ := info["version"].(string); s == "" {
		t.Error("info.version is missing")
	}
	components, _ := doc["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	scheme, _ := schemes["AgentKey"].(map[string]any)
	if scheme["type"] != "http" || !strings.EqualFold(fmt.Sprint(scheme["scheme"]), "bearer") {
		t.Errorf("components.securitySchemes.AgentKey=%v, want type http, scheme bearer", scheme)
	}
	schemas, _ := components["schemas"].(map[string]any)
	if _, ok := schemas["Error"]; !ok {
		t.Error("components.schemas.Error is missing")
	}
	globalSecurity, _ := doc["security"].([]any)

	agtWalkRefs(doc, func(ref string) {
		if _, ok := agtResolveRef(doc, ref); !ok {
			t.Errorf("$ref %q does not resolve inside the document", ref)
		}
	})

	paths, _ := doc["paths"].(map[string]any)
	if len(paths) == 0 {
		t.Fatal("paths is empty")
	}
	routes := agtRouteSet(h)
	methods := map[string]bool{"get": true, "put": true, "post": true, "delete": true, "options": true, "head": true, "patch": true, "trace": true}
	pathItemKeys := map[string]bool{"summary": true, "description": true, "servers": true, "parameters": true, "$ref": true}
	operationIDs := map[string]bool{}
	documented := map[string]string{}
	for path, rawItem := range paths {
		if !strings.HasPrefix(path, "/api/agent/v1") {
			t.Errorf("path %q is outside /api/agent/v1", path)
		}
		item, ok := rawItem.(map[string]any)
		if !ok {
			t.Errorf("path %q is not an object", path)
			continue
		}
		pathParams := map[string]bool{}
		collect := func(list any) {
			params, _ := list.([]any)
			for _, p := range params {
				pm, _ := agtDeref(doc, p).(map[string]any)
				if pm["in"] == "path" {
					if pm["required"] != true {
						t.Errorf("path parameter %v of %s is not required: true", pm["name"], path)
					}
					pathParams[fmt.Sprint(pm["name"])] = true
				}
			}
		}
		collect(item["parameters"])
		for key, rawOp := range item {
			if pathItemKeys[key] || strings.HasPrefix(key, "x-") {
				continue
			}
			if !methods[key] {
				t.Errorf("path %q has key %q, which is not an HTTP method or path item field", path, key)
				continue
			}
			op, _ := rawOp.(map[string]any)
			where := strings.ToUpper(key) + " " + path
			if !routes[strings.ToUpper(key)+" "+agtEchoPath(path)] {
				t.Errorf("%s is documented but no such route is registered", where)
			}
			id, _ := op["operationId"].(string)
			if id == "" || operationIDs[id] {
				t.Errorf("%s operationId=%q, want a unique non-empty id", where, id)
			}
			operationIDs[id] = true
			responses, _ := op["responses"].(map[string]any)
			if len(responses) == 0 {
				t.Errorf("%s has no responses", where)
			}
			for status := range responses {
				if !agtStatusKey.MatchString(status) {
					t.Errorf("%s response key %q is not a status code", where, status)
				}
			}
			saved := map[string]bool{}
			for k, v := range pathParams {
				saved[k] = v
			}
			collect(op["parameters"])
			for _, m := range agtBraceParam.FindAllStringSubmatch(path, -1) {
				if !pathParams[m[1]] {
					t.Errorf("%s does not declare path parameter %s", where, m[1])
				}
			}
			pathParams = saved

			security, hasSecurity := op["security"].([]any)
			perm, _ := op["x-jelly-permission"].(string)
			documented[strings.ToUpper(key)+" "+path] = perm
			switch path {
			case "/api/agent/v1", "/api/agent/v1/openapi.json":
				if !hasSecurity || len(security) != 0 {
					t.Errorf("%s security=%v, want [] (public)", where, op["security"])
				}
			default:
				effective := globalSecurity
				if hasSecurity {
					effective = security
				}
				usesKey := false
				for _, req := range effective {
					if m, ok := req.(map[string]any); ok {
						if _, ok := m["AgentKey"]; ok {
							usesKey = true
						}
					}
				}
				if !usesKey {
					t.Errorf("%s does not require the AgentKey bearer scheme", where)
				}
				if path != "/api/agent/v1/me" && !agtContains(agtAllPermissions, perm) {
					t.Errorf("%s x-jelly-permission=%q, want one of the catalogue permissions", where, perm)
				}
			}
		}
	}
	for _, guarded := range []string{"/api/agent/v1/posts/{id}", "/api/agent/v1/pages/{id}", "/api/agent/v1/products/{id}"} {
		item, _ := paths[guarded].(map[string]any)
		put, _ := item["put"].(map[string]any)
		body, _ := agtDeref(doc, put["requestBody"]).(map[string]any)
		content, _ := body["content"].(map[string]any)
		media, _ := content["application/json"].(map[string]any)
		schema, _ := agtDeref(doc, media["schema"]).(map[string]any)
		required, _ := schema["required"].([]any)
		hasBase := false
		for _, r := range required {
			hasBase = hasBase || r == "base_updated_at"
		}
		if !hasBase {
			t.Errorf("PUT %s request body does not require base_updated_at", guarded)
		}
	}

	// The index and the OpenAPI document come from the same table.
	var index agtIndex
	agtDecode(t, agtDo(h, http.MethodGet, "/api/agent/v1", "", "", ""), &index, "discovery index")
	fromIndex := map[string]string{}
	for _, ep := range index.Endpoints {
		fromIndex[ep.Method+" "+ep.Path] = ep.Permission
	}
	var a, b []string
	for k := range fromIndex {
		a = append(a, k)
	}
	for k := range documented {
		b = append(b, k)
	}
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Errorf("index endpoints and OpenAPI operations differ:\nindex:   %v\nopenapi: %v", a, b)
	}
	for k, perm := range fromIndex {
		if got, ok := documented[k]; ok && got != perm {
			t.Errorf("%s permission is %q in the index and %q in OpenAPI", k, perm, got)
		}
	}
}

func TestAgentMeShape(t *testing.T) {
	h := newAccessHarness(t)
	_, _, created := agtSetup(t, h, "Me Shape", "posts.read", "logs.read")
	r := agtMe(h, created.APIKey)
	agtExpectOK(t, r, "GET /api/agent/v1/me")
	var raw map[string]json.RawMessage
	agtDecode(t, r, &raw, "/me")
	for _, key := range []string{"id", "name", "description", "permissions", "effective_permissions", "owner_role", "key"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("/me is missing %q; body=%q", key, r.body)
		}
	}
	var me struct {
		ID                   uint                       `json:"id"`
		Name                 string                     `json:"name"`
		Permissions          []string                   `json:"permissions"`
		EffectivePermissions []string                   `json:"effective_permissions"`
		OwnerRole            string                     `json:"owner_role"`
		Key                  map[string]json.RawMessage `json:"key"`
	}
	agtDecode(t, r, &me, "/me")
	if me.ID != created.Agent.ID || me.Name != "Me Shape" || me.OwnerRole != "admin" {
		t.Errorf("/me id=%d name=%q owner_role=%q, want %d, Me Shape, admin", me.ID, me.Name, me.OwnerRole, created.Agent.ID)
	}
	if !agtSameStrings(me.Permissions, []string{"posts.read", "logs.read"}) || !agtSameStrings(me.EffectivePermissions, []string{"posts.read", "logs.read"}) {
		t.Errorf("/me permissions=%v effective=%v, want posts.read and logs.read for both", me.Permissions, me.EffectivePermissions)
	}
	for _, key := range []string{"id", "prefix", "expires_at"} {
		if _, ok := me.Key[key]; !ok {
			t.Errorf("/me key is missing %q", key)
		}
	}
	if string(me.Key["prefix"]) != fmt.Sprintf("%q", created.APIKey[:12]) || string(me.Key["expires_at"]) != "null" {
		t.Errorf("/me key=%s, want prefix %s and expires_at null", r.body, created.APIKey[:12])
	}
	if strings.Contains(r.body, created.APIKey[4:]) {
		t.Error("/me echoes the plaintext key")
	}
	if id := r.header.Get(echo.HeaderXRequestID); !agtRequestIDPattern.MatchString(id) {
		t.Errorf("/me X-Request-ID=%q, want a req_ id on successful responses too", id)
	}
}

func TestAgentPostsWriteCreatesPostAndReadOnlyGets403(t *testing.T) {
	h := newAccessHarness(t)
	admin, token, writer := agtSetup(t, h, "Writer", "posts.read", "posts.write")
	reader := agtCreate(t, h, token, "Reader", "posts.read")

	created := agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"Agent 草稿","content":"<p>agent body</p>","status":"draft"}`, writer.APIKey, "")
	if created.code < 200 || created.code > 299 {
		t.Fatalf("posts.write POST /api/agent/v1/posts returned %d, want 2xx; body=%q", created.code, created.body)
	}
	var post core.Post
	agtDecode(t, created, &post, "created post")
	if post.ID == 0 || post.Type != "post" || post.AuthorID != admin.ID || post.Title != "Agent 草稿" {
		t.Errorf("created post = id %d type %q author %d title %q, want a post by the owner %d", post.ID, post.Type, post.AuthorID, post.Title, admin.ID)
	}

	denied := agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"Reader 草稿","content":"x","status":"draft"}`, reader.APIKey, "")
	env := agtExpectError(t, denied, http.StatusForbidden, "permission_missing", "posts.read-only agent creating a post")
	missing, _ := env.Details["missing_permissions"].([]any)
	if len(missing) != 1 || missing[0] != "posts.write" {
		t.Errorf("details.missing_permissions=%v, want [posts.write]", env.Details["missing_permissions"])
	}
	var count int64
	h.db.Model(&core.Post{}).Where("title = ?", "Reader 草稿").Count(&count)
	if count != 0 {
		t.Errorf("a post was created by the read-only agent")
	}

	list := agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", reader.APIKey, "")
	agtExpectOK(t, list, "posts.read GET /api/agent/v1/posts")
	if !strings.Contains(list.body, "Agent 草稿") {
		t.Errorf("posts.read list does not include the writer's post; body=%q", list.body)
	}
	update := agtDo(h, http.MethodPut, fmt.Sprintf("/api/agent/v1/posts/%d", post.ID), fmt.Sprintf(`{"title":"改","base_updated_at":%q}`, post.UpdatedAt.Format(time.RFC3339Nano)), reader.APIKey, "")
	agtExpectError(t, update, http.StatusForbidden, "permission_missing", "posts.read-only agent updating a post")
	del := agtDo(h, http.MethodDelete, fmt.Sprintf("/api/agent/v1/posts/%d", post.ID), "", reader.APIKey, "")
	agtExpectError(t, del, http.StatusForbidden, "permission_missing", "posts.read-only agent deleting a post")
}

func TestAgentPostEndpointsAreTypeScoped(t *testing.T) {
	h := newAccessHarness(t)
	admin, _, agent := agtSetup(t, h, "Type Scope", "posts.read", "posts.write", "pages.read")
	page := core.Post{Title: "關於我們頁", Slug: "agt-about", Content: "page", Type: "page", Status: "published", AuthorID: admin.ID, Visibility: "public"}
	article := core.Post{Title: "一般文章", Slug: "agt-article", Content: "post", Type: "post", Status: "published", AuthorID: admin.ID, Visibility: "public"}
	for _, p := range []*core.Post{&page, &article} {
		if err := h.db.Create(p).Error; err != nil {
			t.Fatalf("seed %s: %v", p.Slug, err)
		}
	}
	key := agent.APIKey

	posts := agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", key, "")
	agtExpectOK(t, posts, "GET /posts")
	if strings.Contains(posts.body, "agt-about") || !strings.Contains(posts.body, "agt-article") {
		t.Errorf("GET /posts must list posts only; body=%q", posts.body)
	}
	pages := agtDo(h, http.MethodGet, "/api/agent/v1/pages", "", key, "")
	agtExpectOK(t, pages, "GET /pages")
	if !strings.Contains(pages.body, "agt-about") || strings.Contains(pages.body, "agt-article") {
		t.Errorf("GET /pages must list pages only; body=%q", pages.body)
	}
	agtExpectError(t, agtDo(h, http.MethodGet, fmt.Sprintf("/api/agent/v1/posts/%d", page.ID), "", key, ""), http.StatusNotFound, "not_found", "GET /posts/:id of a page")
	agtExpectError(t, agtDo(h, http.MethodGet, fmt.Sprintf("/api/agent/v1/pages/%d", article.ID), "", key, ""), http.StatusNotFound, "not_found", "GET /pages/:id of a post")
	agtExpectError(t, agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"偷渡頁面","content":"x","type":"page"}`, key, ""), http.StatusBadRequest, "type_mismatch", "POST /posts with type page")
	agtExpectError(t, agtDo(h, http.MethodPost, "/api/agent/v1/pages", `{"title":"無權限頁面","content":"x"}`, key, ""), http.StatusForbidden, "permission_missing", "POST /pages without pages.write")
}

func agtUpdatedAt(t *testing.T, r agtResult, where string) string {
	t.Helper()
	var body struct {
		UpdatedAt string `json:"updated_at"`
	}
	agtDecode(t, r, &body, where)
	if body.UpdatedAt == "" {
		t.Fatalf("%s has no updated_at; body=%q", where, r.body)
	}
	return body.UpdatedAt
}

func TestAgentUpdateRequiresBaseUpdatedAt(t *testing.T) {
	h := newAccessHarness(t)
	admin, token, agent := agtSetup(t, h, "Version Guard", "posts.read", "posts.write", "pages.write", "products.read", "products.write")
	key := agent.APIKey

	created := agtDo(h, http.MethodPost, "/api/agent/v1/posts", `{"title":"原始標題","content":"v0","status":"draft"}`, key, "")
	if created.code < 200 || created.code > 299 {
		t.Fatalf("create post returned %d; body=%q", created.code, created.body)
	}
	var post core.Post
	agtDecode(t, created, &post, "created post")
	path := fmt.Sprintf("/api/agent/v1/posts/%d", post.ID)
	base0 := agtUpdatedAt(t, agtDo(h, http.MethodGet, path, "", key, ""), "GET post")

	agtExpectError(t, agtDo(h, http.MethodPut, path, `{"title":"沒帶版本"}`, key, ""), http.StatusBadRequest, "version_required", "PUT without base_updated_at")
	agtExpectError(t, agtDo(h, http.MethodPut, path, `{"title":"壞版本","base_updated_at":"yesterday"}`, key, ""), http.StatusBadRequest, "version_invalid", "PUT with an unparsable base_updated_at")

	first := agtDo(h, http.MethodPut, path, fmt.Sprintf(`{"title":"第一次","base_updated_at":%q}`, base0), key, "")
	agtExpectOK(t, first, "PUT with the current base_updated_at")
	base1 := agtUpdatedAt(t, first, "first update")
	if base1 == base0 {
		t.Fatalf("updated_at did not change after an update (%s)", base1)
	}
	stale := agtDo(h, http.MethodPut, path, fmt.Sprintf(`{"title":"第二次","base_updated_at":%q}`, base0), key, "")
	env := agtExpectError(t, stale, http.StatusConflict, "version_conflict", "second PUT with the same old base_updated_at")
	current, _ := env.Details["current_updated_at"].(string)
	want, _ := time.Parse(time.RFC3339Nano, base1)
	if got, err := time.Parse(time.RFC3339Nano, current); err != nil || !got.Equal(want) {
		t.Errorf("details.current_updated_at=%q, want the stored updated_at %s", current, base1)
	}
	var stored core.Post
	h.db.First(&stored, post.ID)
	if stored.Title != "第一次" {
		t.Errorf("title after the conflict = %q, want 第一次", stored.Title)
	}

	// The comparison and the write are one statement: of several requests carrying the same base,
	// exactly one wins.
	const racers = 6
	results := make([]agtResult, racers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = agtDo(h, http.MethodPut, path, fmt.Sprintf(`{"title":"並行 %d","base_updated_at":%q}`, i, base1), key, "")
		}(i)
	}
	close(start)
	wg.Wait()
	winners, conflicts := 0, 0
	winner := ""
	for i, r := range results {
		switch r.code {
		case http.StatusOK:
			winners++
			winner = fmt.Sprintf("並行 %d", i)
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("racer %d returned %d; body=%q", i, r.code, r.body)
		}
	}
	if winners != 1 || conflicts != racers-1 {
		t.Errorf("concurrent updates with one base: %d succeeded and %d conflicted, want 1 and %d", winners, conflicts, racers-1)
	}
	h.db.First(&stored, post.ID)
	if winners == 1 && stored.Title != winner {
		t.Errorf("stored title %q, want the winner's %q", stored.Title, winner)
	}

	// /api/admin is unchanged: no base_updated_at needed.
	adminPut := h.request(http.MethodPut, fmt.Sprintf("/api/admin/posts/%d", post.ID), `{"title":"管理員直接改"}`, token, echo.MIMEApplicationJSON, echo.MIMEApplicationJSON)
	if adminPut.code != http.StatusOK {
		t.Errorf("admin PUT without base_updated_at returned %d, want 200; body=%q", adminPut.code, adminPut.body)
	}

	// Pages and products are guarded the same way.
	page := core.Post{Title: "頁面", Slug: "agt-guard-page", Type: "page", Status: "draft", AuthorID: admin.ID, Visibility: "public"}
	if err := h.db.Create(&page).Error; err != nil {
		t.Fatalf("seed page: %v", err)
	}
	agtExpectError(t, agtDo(h, http.MethodPut, fmt.Sprintf("/api/agent/v1/pages/%d", page.ID), `{"title":"x"}`, key, ""), http.StatusBadRequest, "version_required", "PUT /pages/:id without base_updated_at")
	product := core.Product{Title: "果凍禮盒", Slug: "agt-guard-product", Price: 880, Status: "active"}
	if err := h.db.Create(&product).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	productPath := fmt.Sprintf("/api/agent/v1/products/%d", product.ID)
	agtExpectError(t, agtDo(h, http.MethodPut, productPath, `{"title":"x","price":900}`, key, ""), http.StatusBadRequest, "version_required", "PUT /products/:id without base_updated_at")
	pbase := agtUpdatedAt(t, agtDo(h, http.MethodGet, productPath, "", key, ""), "GET product")
	agtExpectOK(t, agtDo(h, http.MethodPut, productPath, fmt.Sprintf(`{"title":"果凍禮盒 新版","price":900,"base_updated_at":%q}`, pbase), key, ""), "PUT product with the current base")
	agtExpectError(t, agtDo(h, http.MethodPut, productPath, fmt.Sprintf(`{"title":"果凍禮盒 舊版","price":800,"base_updated_at":%q}`, pbase), key, ""), http.StatusConflict, "version_conflict", "PUT product with the old base")
}

func TestAgentKeyCannotReachAdminAPI(t *testing.T) {
	h := newAccessHarness(t)
	admin, token, agent := agtSetup(t, h, "Boundary", agtAllPermissions...)
	key := agent.APIKey
	before := accessDatabaseSnapshot(t, h.db)
	for _, probe := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/admin/posts", ""},
		{http.MethodPost, "/api/admin/posts", `{"title":"偷渡","content":"x","type":"post","status":"draft"}`},
		{http.MethodGet, "/api/admin/settings", ""},
		{http.MethodPost, "/api/admin/settings", `{"site_title":"被 agent 改掉"}`},
		{http.MethodGet, "/api/admin/products", ""},
		{http.MethodGet, "/api/admin/orders", ""},
		{http.MethodGet, "/api/admin/media", ""},
		{http.MethodGet, "/api/admin/comments", ""},
		{http.MethodGet, "/api/admin/agents", ""},
		{http.MethodPost, fmt.Sprintf("/api/admin/agents/%d/rotate", agent.Agent.ID), `{"mode":"immediate"}`},
	} {
		if r := agtDo(h, probe.method, probe.path, probe.body, key, ""); r.code != http.StatusUnauthorized {
			t.Errorf("agent key %s %s returned %d, want 401", probe.method, probe.path, r.code)
		}
	}
	if after := accessDatabaseSnapshot(t, h.db); after != before {
		t.Error("agent key requests to /api/admin changed the database")
	}
	if d := agtDetail(t, h, token, agent.Agent.ID); len(d.Keys) != 1 {
		t.Errorf("the agent rotated its own key through /api/admin: keys=%+v", d.Keys)
	}

	// Human credentials do not work on the agent API.
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/me", "", h.token(admin), ""), http.StatusUnauthorized, "key_invalid", "admin JWT as bearer on /api/agent/v1/me")
	req := httptest.NewRequest(http.MethodGet, "/api/agent/v1/posts", nil)
	req.Header.Set(echo.HeaderAccept, echo.MIMEApplicationJSON)
	req.AddCookie(&http.Cookie{Name: "jwt_token", Value: h.token(admin)})
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, req)
	agtExpectError(t, agtResult{code: rec.Code, body: rec.Body.String(), header: rec.Header()}, http.StatusUnauthorized, "key_missing", "admin jwt_token cookie on /api/agent/v1/posts")
}

func TestAgentErrorsUseEnvelopeWithCorrelationID(t *testing.T) {
	h := newAccessHarness(t)
	_, _, agent := agtSetup(t, h, "Envelope", "posts.read")
	key := agent.APIKey

	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/me", "", "", ""), http.StatusUnauthorized, "key_missing", "/me without a key")
	basic := httptest.NewRequest(http.MethodGet, "/api/agent/v1/me", nil)
	basic.Header.Set(echo.HeaderAuthorization, "Basic YWRtaW46YWRtaW4=")
	rec := httptest.NewRecorder()
	h.echo.ServeHTTP(rec, basic)
	agtExpectError(t, agtResult{code: rec.Code, body: rec.Body.String(), header: rec.Header()}, http.StatusUnauthorized, "key_invalid", "/me with Basic auth")
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/me", "", "jla_"+strings.Repeat("A", 43), ""), http.StatusUnauthorized, "key_invalid", "/me with an unknown key")

	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/no-such-thing", "", key, ""), http.StatusNotFound, "not_found", "unknown agent route")
	agtExpectError(t, agtDo(h, http.MethodDelete, "/api/agent/v1/me", "", key, ""), http.StatusMethodNotAllowed, "method_not_allowed", "DELETE /me")
	missing := agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts/987654321", "", key, ""), http.StatusNotFound, "not_found", "reused handler 404 for a missing post")
	if missing.Message == "" {
		t.Error("rewritten handler error lost its message")
	}

	first, second := agtMe(h, key), agtMe(h, key)
	agtExpectOK(t, first, "/me")
	a, b := first.header.Get(echo.HeaderXRequestID), second.header.Get(echo.HeaderXRequestID)
	if !agtRequestIDPattern.MatchString(a) || !agtRequestIDPattern.MatchString(b) || a == b {
		t.Errorf("X-Request-ID on two successful requests = %q, %q; want two different req_ ids", a, b)
	}
	spoof := httptest.NewRequest(http.MethodGet, "/api/agent/v1/me", nil)
	spoof.Header.Set(echo.HeaderAuthorization, "Bearer "+key)
	spoof.Header.Set(echo.HeaderXRequestID, "req_chosen_by_client")
	rec = httptest.NewRecorder()
	h.echo.ServeHTTP(rec, spoof)
	if got := rec.Header().Get(echo.HeaderXRequestID); got == "req_chosen_by_client" || !agtRequestIDPattern.MatchString(got) {
		t.Errorf("X-Request-ID=%q, want a server-generated id that ignores the client's", got)
	}
}

func TestAgentLogsListOwnEventsWithCursor(t *testing.T) {
	h := newAccessHarness(t)
	started := time.Now().UTC()
	_, token, a := agtSetup(t, h, "Logger", "posts.read", "logs.read")
	b := agtCreate(t, h, token, "Other Logger", "posts.read", "logs.read")
	c := agtCreate(t, h, token, "No Logs", "posts.read")
	const ip = "203.0.113.9:1000"

	for i := 0; i < 3; i++ {
		agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", a.APIKey, ip), "A GET /posts")
	}
	marked := agtDo(h, http.MethodGet, "/api/agent/v1/posts?status=draft", "", a.APIKey, ip)
	agtExpectOK(t, marked, "A GET /posts?status=draft")
	markedID := marked.header.Get(echo.HeaderXRequestID)
	for i := 0; i < 2; i++ {
		agtDo(h, http.MethodGet, "/api/agent/v1/posts/987654321", "", a.APIKey, ip)
	}
	agtExpectOK(t, agtMe(h, a.APIKey), "A /me")
	for i := 0; i < 2; i++ {
		agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", b.APIKey, "192.0.2.50:1"), "B GET /posts")
	}

	fetch := func(query url.Values) (agtEventPage, agtResult) {
		t.Helper()
		r := agtDo(h, http.MethodGet, "/api/agent/v1/logs?"+query.Encode(), "", a.APIKey, ip)
		if r.code != http.StatusOK {
			t.Fatalf("GET /logs?%s returned %d, want 200; body=%q", query.Encode(), r.code, r.body)
		}
		var page agtEventPage
		agtDecode(t, r, &page, "agent logs")
		return page, r
	}

	// Cursor pagination over the six posts events, newest first.
	seen := map[int64]bool{}
	var ids []int64
	cursor := ""
	for pageNo := 0; pageNo < 5; pageNo++ {
		q := url.Values{"module": {"posts"}, "limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		page, raw := fetch(q)
		if page.Total != 6 {
			t.Errorf("page %d total=%d, want 6 posts events", pageNo, page.Total)
		}
		if strings.Contains(raw.body, "203.0.113.9") {
			t.Error("agent logs contain the raw source IP")
		}
		for _, ev := range page.Items {
			if seen[ev.ID] {
				t.Errorf("event %d returned twice across pages", ev.ID)
			}
			seen[ev.ID] = true
			ids = append(ids, ev.ID)
			if ev.AgentID != a.Agent.ID || ev.ActorType != "agent" || ev.Module != "posts" || ev.KeyPrefix != a.APIKey[:12] || ev.IPMasked != "203.0.113.0" || ev.Method != http.MethodGet {
				t.Errorf("event %+v, want agent %d, actor agent, module posts, key_prefix %s, ip_masked 203.0.113.0", ev, a.Agent.ID, a.APIKey[:12])
			}
			if ev.Route != "/api/agent/v1/posts" && ev.Route != "/api/agent/v1/posts/:id" {
				t.Errorf("event route=%q, want the Echo route template", ev.Route)
			}
			if !agtRequestIDPattern.MatchString(ev.CorrelationID) {
				t.Errorf("event correlation_id=%q, want a req_ id", ev.CorrelationID)
			}
			for _, key := range []string{"params", "query_keys", "body_bytes"} {
				if _, ok := ev.Summary[key]; !ok {
					t.Errorf("event summary %v lacks %s", ev.Summary, key)
				}
			}
		}
		if page.NextCursor == nil {
			if len(page.Items) != 2 || len(ids) != 6 {
				t.Errorf("last page has %d items after %d total, want the sixth event to end on a full page with next_cursor null", len(page.Items), len(ids))
			}
			break
		}
		if len(page.Items) != 2 {
			t.Errorf("page %d has %d items, want 2", pageNo, len(page.Items))
		}
		cursor = *page.NextCursor
	}
	if len(ids) != 6 {
		t.Fatalf("pagination returned %d events, want 6", len(ids))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] >= ids[i-1] {
			t.Errorf("event ids %v are not newest first", ids)
			break
		}
	}

	all, _ := fetch(url.Values{"module": {"posts"}, "limit": {"100"}})
	foundMarked, foundMissing := false, false
	for _, ev := range all.Items {
		if ev.CorrelationID == markedID {
			foundMarked = true
			keys, _ := ev.Summary["query_keys"].([]any)
			if len(keys) != 1 || keys[0] != "status" {
				t.Errorf("summary.query_keys=%v, want [status]", ev.Summary["query_keys"])
			}
			if strings.Contains(fmt.Sprint(ev.Summary), "draft") {
				t.Error("summary stores a query value")
			}
		}
		if ev.Status == http.StatusNotFound {
			foundMissing = true
			params, _ := ev.Summary["params"].(map[string]any)
			if params["id"] != "987654321" {
				t.Errorf("404 event summary.params=%v, want id 987654321", ev.Summary["params"])
			}
		}
	}
	if !foundMarked {
		t.Errorf("no event carries the correlation_id %q of the marked request", markedID)
	}
	if !foundMissing {
		t.Error("the 404 requests were not recorded")
	}

	for query, want := range map[string]int{
		"module=posts&status=404": 2,
		"module=posts&status=4xx": 2,
		"module=posts&status=2xx": 4,
		"module=me":               1,
		"module=posts&since=" + url.QueryEscape(started.Add(-time.Minute).Format(time.RFC3339)): 6,
		"module=posts&since=" + url.QueryEscape(started.Add(time.Hour).Format(time.RFC3339)):    0,
		"module=posts&until=" + url.QueryEscape(started.Add(-time.Hour).Format(time.RFC3339)):   0,
	} {
		q, _ := url.ParseQuery(query)
		if page, _ := fetch(q); page.Total != want {
			t.Errorf("GET /logs?%s total=%d, want %d", query, page.Total, want)
		}
	}
	for _, bad := range []string{"limit=0", "limit=101", "status=abc", "since=yesterday", "cursor=%21%21not-a-cursor"} {
		agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/logs?"+bad, "", a.APIKey, ip), http.StatusBadRequest, "logs_filter_invalid", "GET /logs?"+bad)
	}
	env := agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/logs", "", c.APIKey, ""), http.StatusForbidden, "permission_missing", "GET /logs without logs.read")
	if missing, _ := env.Details["missing_permissions"].([]any); len(missing) != 1 || missing[0] != "logs.read" {
		t.Errorf("details.missing_permissions=%v, want [logs.read]", env.Details["missing_permissions"])
	}
}

func TestAgentSettingsReadMasksSecrets(t *testing.T) {
	h := newAccessHarness(t)
	_, token, reader := agtSetup(t, h, "Settings Reader", "settings.read")
	other := agtCreate(t, h, token, "No Settings", "posts.read")
	secrets := map[string]string{
		"payment_ecpay_hash_key":     "hk-live-0123456789",
		"payment_ecpay_hash_iv":      "iv-zyxw9876",
		"oauth_google_client_secret": "gsecret-abcdef123456",
		"oauth_github_client_secret": "abc",
	}
	for k, v := range secrets {
		maintenanceSetOption(t, h, k, v)
	}
	maintenanceSetOption(t, h, "site_title", "Agent 測試站")

	r := agtDo(h, http.MethodGet, "/api/agent/v1/settings", "", reader.APIKey, "")
	agtExpectOK(t, r, "settings.read GET /settings")
	for k, v := range secrets {
		if strings.Contains(r.body, v) {
			t.Errorf("GET /settings exposes %s in clear text", k)
		}
	}
	var options []core.Option
	agtDecode(t, r, &options, "settings")
	values := map[string]string{}
	for _, o := range options {
		values[o.Key] = o.Value
	}
	for k, want := range map[string]string{
		"payment_ecpay_hash_key":     "****6789",
		"payment_ecpay_hash_iv":      "****9876",
		"oauth_google_client_secret": "****3456",
		"oauth_github_client_secret": "****",
		"site_title":                 "Agent 測試站",
	} {
		if values[k] != want {
			t.Errorf("settings[%s]=%q, want %q", k, values[k], want)
		}
	}
	agtExpectError(t, agtDo(h, http.MethodGet, "/api/agent/v1/settings", "", other.APIKey, ""), http.StatusForbidden, "permission_missing", "GET /settings without settings.read")
}

func TestAgentAPIOpenDuringMaintenance(t *testing.T) {
	h := newAccessHarness(t)
	_, _, agent := agtSetup(t, h, "Maintenance", "posts.read")
	maintenanceEnable(t, h, 1)
	if r := agtDo(h, http.MethodGet, "/api/search?q=x", "", "", ""); r.code != http.StatusServiceUnavailable {
		t.Fatalf("maintenance is not on: /api/search returned %d", r.code)
	}
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1", "", "", ""), "discovery index during maintenance")
	agtExpectOK(t, agtMe(h, agent.APIKey), "/me during maintenance")
	agtExpectOK(t, agtDo(h, http.MethodGet, "/api/agent/v1/posts", "", agent.APIKey, ""), "GET /posts during maintenance")
}

func TestAgentSkillFileDescribesDiscovery(t *testing.T) {
	path := filepath.Join(repoRoot(t), "skills", "jelly-cms", "SKILL.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(content)
	for _, want := range []string{"/api/agent/v1", "openapi.json"} {
		if !strings.Contains(text, want) {
			t.Errorf("skills/jelly-cms/SKILL.md does not mention %s", want)
		}
	}
}

package server

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/agent"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type agentKeyView struct {
	ID         uint       `json:"id"`
	Prefix     string     `json:"prefix"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}
type agentOwnerView struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}
type agentView struct {
	ID                   uint            `json:"id"`
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	Permissions          []string        `json:"permissions"`
	EffectivePermissions []string        `json:"effective_permissions"`
	Status               string          `json:"status"`
	Owner                *agentOwnerView `json:"owner"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
	DisabledAt           *time.Time      `json:"disabled_at"`
	LastUsedAt           *time.Time      `json:"last_used_at"`
	Keys                 []agentKeyView  `json:"keys"`
}

var adminRoles = []string{"admin"}
var contentRoles = []string{"admin", "editor", "author"}
var editorRoles = []string{"admin", "editor"}

func permissionRoles(p string) []string {
	for _, route := range agentRouteTable(nil, nil) {
		if string(route.Permission) == p {
			return route.RequiredRoles
		}
	}
	return nil
}
func roleListContains(role string, roles []string) bool {
	for _, r := range roles {
		if role == r {
			return true
		}
	}
	return false
}
func buildAgentView(a core.Agent) (agentView, error) {
	views, err := buildAgentViews([]core.Agent{a})
	if err != nil {
		return agentView{}, err
	}
	return views[0], nil
}

// buildAgentViews batches the owner and key lookups used by the admin list.
func buildAgentViews(agents []core.Agent) ([]agentView, error) {
	views := make([]agentView, len(agents))
	ownerIDs := make([]uint, 0, len(agents))
	agentIDs := make([]uint, 0, len(agents))
	for i, a := range agents {
		views[i] = agentView{ID: a.ID, Name: a.Name, Description: a.Description, Permissions: append([]string(nil), a.Permissions...), EffectivePermissions: []string{}, Status: a.Status, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, DisabledAt: a.DisabledAt, Keys: []agentKeyView{}}
		agentIDs = append(agentIDs, a.ID)
		if a.OwnerID != nil {
			ownerIDs = append(ownerIDs, *a.OwnerID)
		}
	}
	owners := map[uint]core.User{}
	if len(ownerIDs) > 0 {
		var rows []core.User
		if err := data.DB.Where("id IN ?", ownerIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, u := range rows {
			owners[u.ID] = u
		}
	}
	keysByAgent := map[uint][]core.AgentKey{}
	if len(agentIDs) > 0 {
		var keys []core.AgentKey
		if err := data.DB.Where("agent_id IN ?", agentIDs).Order("id DESC").Find(&keys).Error; err != nil {
			return nil, err
		}
		for _, k := range keys {
			keysByAgent[k.AgentID] = append(keysByAgent[k.AgentID], k)
		}
	}
	for i, a := range agents {
		role := ""
		if a.OwnerID != nil {
			if owner, ok := owners[*a.OwnerID]; ok {
				views[i].Owner = &agentOwnerView{owner.ID, owner.Username, owner.Role}
				role = owner.Role
			}
		}
		for _, p := range a.Permissions {
			if roleListContains(role, permissionRoles(p)) {
				views[i].EffectivePermissions = append(views[i].EffectivePermissions, p)
			}
		}
		for _, k := range keysByAgent[a.ID] {
			status := "active"
			if k.RevokedAt != nil {
				status = "revoked"
			} else if k.ExpiresAt != nil {
				if !k.ExpiresAt.After(time.Now()) {
					status = "expired"
				} else {
					status = "grace"
				}
			}
			views[i].Keys = append(views[i].Keys, agentKeyView{k.ID, k.Prefix, status, k.CreatedAt, k.LastUsedAt, k.ExpiresAt, k.RevokedAt})
			if k.LastUsedAt != nil && (views[i].LastUsedAt == nil || k.LastUsedAt.After(*views[i].LastUsedAt)) {
				t := *k.LastUsedAt
				views[i].LastUsedAt = &t
			}
		}
	}
	return views, nil
}

func agentKeyViewFor(k core.AgentKey) agentKeyView {
	status := "active"
	if k.RevokedAt != nil {
		status = "revoked"
	} else if k.ExpiresAt != nil {
		if !k.ExpiresAt.After(time.Now()) {
			status = "expired"
		} else {
			status = "grace"
		}
	}
	return agentKeyView{k.ID, k.Prefix, status, k.CreatedAt, k.LastUsedAt, k.ExpiresAt, k.RevokedAt}
}
func agentID(c echo.Context) (uint, bool) {
	n, e := strconv.ParseUint(c.Param("id"), 10, 64)
	return uint(n), e == nil && n > 0 && uint64(uint(n)) == n
}
func loadAgent(id uint) (core.Agent, error) {
	var a core.Agent
	err := data.DB.First(&a, id).Error
	return a, err
}
func agentMissing(c echo.Context) error {
	return writeAPIError(c, 404, "agent_not_found", "Agent not found", nil)
}
func listAgents(c echo.Context) error {
	var rows []core.Agent
	var total int64
	q := data.DB.Model(&core.Agent{})
	if err := q.Count(&total).Error; err != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to list agents", nil)
	}
	if err := q.Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to list agents", nil)
	}
	items, err := buildAgentViews(rows)
	if err != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to list agents", nil)
	}
	return c.JSON(200, map[string]any{"items": items, "total": total})
}
func getAgent(c echo.Context) error {
	id, ok := agentID(c)
	if !ok {
		return agentMissing(c)
	}
	a, e := loadAgent(id)
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return agentMissing(c)
	}
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	v, e := buildAgentView(a)
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	return c.JSON(200, v)
}

type agentMutation struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Permissions *[]string `json:"permissions"`
}

func validateAgentFields(name, description string, permissions []string) (string, string, []string, string) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return "", "", nil, "name_invalid"
	}
	if utf8.RuneCountInString(description) > 500 {
		return "", "", nil, "description_too_long"
	}
	if len(permissions) == 0 {
		return "", "", nil, "permissions_required"
	}
	seen := map[string]bool{}
	for _, p := range permissions {
		if !agent.ValidPermission(p) {
			return "", "", nil, "permission_unknown"
		}
		if !seen[p] {
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for _, p := range agent.AllPermissions() {
		if seen[string(p)] {
			out = append(out, string(p))
		}
	}
	return name, description, out, ""
}
func unknownAgentPermissions(values []string) []string {
	unknown := []string{}
	seen := map[string]bool{}
	for _, p := range values {
		if !agent.ValidPermission(p) && !seen[p] {
			seen[p] = true
			unknown = append(unknown, p)
		}
	}
	return unknown
}
func userID(c echo.Context) *uint {
	u, _ := c.Get("user").(*core.User)
	if u == nil {
		return nil
	}
	id := u.ID
	return &id
}
func createAgent(c echo.Context) error {
	var in struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Permissions []string `json:"permissions"`
	}
	if e := c.Bind(&in); e != nil {
		return writeAPIError(c, 400, "bad_request", "Invalid request", nil)
	}
	name, desc, perms, code := validateAgentFields(in.Name, in.Description, in.Permissions)
	if code != "" {
		details := map[string]any(nil)
		if code == "permission_unknown" {
			details = map[string]any{"unknown": unknownAgentPermissions(in.Permissions)}
		}
		return writeAPIError(c, 400, code, "Invalid agent fields", details)
	}
	plain, e := agent.GenerateKey()
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to create key", nil)
	}
	now := time.Now().UTC()
	owner := userID(c)
	if owner == nil {
		return writeAPIError(c, 401, "unauthorized", "Authentication required", nil)
	}
	a := core.Agent{Name: name, Description: desc, Permissions: perms, OwnerID: owner, CreatedBy: owner, Status: "active", CreatedAt: now, UpdatedAt: now}
	k := core.AgentKey{KeyHash: agent.HashKey(plain), Prefix: agent.PrefixOf(plain), CreatedBy: owner, CreatedAt: now}
	e = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var n int64
		if e := tx.Model(&core.Agent{}).Where("lower(name)=lower(?)", name).Count(&n).Error; e != nil {
			return e
		}
		if n > 0 {
			return errAgentNameTaken
		}
		if e := tx.Create(&a).Error; e != nil {
			return e
		}
		k.AgentID = a.ID
		if e := tx.Create(&k).Error; e != nil {
			return e
		}
		sum, _ := json.Marshal(map[string]any{"permissions": perms})
		ev := core.AgentEvent{At: now, ActorType: "user", ActorUserID: owner, AgentID: a.ID, AgentName: a.Name, KeyID: &k.ID, KeyPrefix: k.Prefix, Action: "agent.create", Module: "agents", Method: "POST", Route: "/api/admin/agents", Status: 201, IPMasked: maskedRealIP(c), Summary: sum, RequestID: requestIDFor(c)}
		return writeAgentEvent(tx, ev)
	})
	if errors.Is(e, errAgentNameTaken) || isAgentNameUniqueViolation(e) {
		return writeAPIError(c, 409, "agent_name_taken", "An agent with this name already exists", nil)
	}
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to create agent", nil)
	}
	v, e := buildAgentView(a)
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	return c.JSON(201, map[string]any{"agent": v, "key": agentKeyViewFor(k), "api_key": plain})
}

var errAgentNameTaken = errors.New("agent name taken")

func isAgentNameUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "idx_agents_active_lower_name"
}

func patchAgent(c echo.Context) error {
	id, ok := agentID(c)
	if !ok {
		return agentMissing(c)
	}
	var in agentMutation
	if e := c.Bind(&in); e != nil {
		return writeAPIError(c, 400, "bad_request", "Invalid request", nil)
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
			return writeAPIError(c, 400, "name_invalid", "Invalid agent name", nil)
		}
		in.Name = &name
	}
	if in.Description != nil && utf8.RuneCountInString(*in.Description) > 500 {
		return writeAPIError(c, 400, "description_too_long", "Description is too long", nil)
	}
	if in.Permissions != nil {
		if len(*in.Permissions) == 0 {
			return writeAPIError(c, 400, "permissions_required", "At least one permission is required", nil)
		}
		unknown := unknownAgentPermissions(*in.Permissions)
		if len(unknown) > 0 {
			return writeAPIError(c, 400, "permission_unknown", "Unknown permission", map[string]any{"unknown": unknown})
		}
		seen := map[string]bool{}
		for _, p := range *in.Permissions {
			seen[p] = true
		}
		ordered := []string{}
		for _, p := range agent.AllPermissions() {
			if seen[string(p)] {
				ordered = append(ordered, string(p))
			}
		}
		in.Permissions = &ordered
	}
	var a core.Agent
	old := []string{}
	e := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = lockAgent(tx, id)
		if err != nil {
			return err
		}
		old = append([]string(nil), a.Permissions...)
		updates := map[string]any{}
		if in.Name != nil {
			a.Name = *in.Name
			updates["name"] = a.Name
		}
		if in.Description != nil {
			a.Description = *in.Description
			updates["description"] = a.Description
		}
		if in.Permissions != nil {
			a.Permissions = append([]string(nil), (*in.Permissions)...)
			updates["permissions"] = a.Permissions
		}
		if in.Name != nil {
			var n int64
			if err := tx.Model(&core.Agent{}).Where("id <> ? AND lower(name)=lower(?)", id, a.Name).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return errAgentNameTaken
			}
		}
		if len(updates) > 0 {
			result := tx.Model(&core.Agent{}).Where("id = ? AND deleted_at IS NULL", id).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return gorm.ErrRecordNotFound
			}
		}
		sum, _ := json.Marshal(map[string]any{"old_permissions": old, "permissions": a.Permissions})
		ev := core.AgentEvent{At: time.Now().UTC(), ActorType: "user", ActorUserID: userID(c), AgentID: a.ID, AgentName: a.Name, Action: "agent.update", Module: "agents", Method: "PATCH", Route: "/api/admin/agents/:id", Status: 200, IPMasked: maskedRealIP(c), Summary: sum, RequestID: requestIDFor(c)}
		return writeAgentEvent(tx, ev)
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return agentMissing(c)
	}
	if errors.Is(e, errAgentNameTaken) || isAgentNameUniqueViolation(e) {
		return writeAPIError(c, 409, "agent_name_taken", "An agent with this name already exists", nil)
	}
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to update agent", nil)
	}
	a, e = loadAgent(id)
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	v, e := buildAgentView(a)
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	return c.JSON(200, v)
}

func lockAgent(tx *gorm.DB, id uint) (core.Agent, error) {
	var a core.Agent
	e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, id).Error
	return a, e
}
func rotateAgentKey(c echo.Context) error {
	id, ok := agentID(c)
	if !ok {
		return agentMissing(c)
	}
	var in struct {
		Mode         string `json:"mode"`
		GraceMinutes *int   `json:"grace_minutes"`
	}
	if c.Request().ContentLength != 0 {
		if e := c.Bind(&in); e != nil {
			if !errors.Is(e, io.EOF) {
				return writeAPIError(c, 400, "bad_request", "Invalid request", nil)
			}
		}
	}
	if in.Mode == "" {
		in.Mode = "immediate"
	}
	if in.Mode != "immediate" && in.Mode != "grace" {
		return writeAPIError(c, 400, "rotation_mode_invalid", "Mode must be immediate or grace", nil)
	}
	if in.Mode == "grace" && (in.GraceMinutes == nil || !agent.ValidGraceMinutes(*in.GraceMinutes)) {
		return writeAPIError(c, 400, "grace_out_of_range", "過渡期必須是 1 到 1440 分鐘（最長 24 小時）", map[string]any{"min": 1, "max": 1440})
	}
	plain, e := agent.GenerateKey()
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to create key", nil)
	}
	now := time.Now().UTC()
	uid := userID(c)
	var a core.Agent
	var old *core.AgentKey
	var newKey core.AgentKey
	var revoked []string
	e = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = lockAgent(tx, id)
		if err != nil {
			return err
		}
		var keys []core.AgentKey
		if err = tx.Where("agent_id = ?", id).Order("id DESC").Find(&keys).Error; err != nil {
			return err
		}
		for i := range keys {
			k := keys[i]
			if k.RevokedAt != nil {
				continue
			}
			if k.ExpiresAt != nil && !k.ExpiresAt.After(now) {
				continue
			}
			if k.ExpiresAt == nil {
				cp := k
				old = &cp
				break
			}
		}
		newKey = core.AgentKey{AgentID: id, KeyHash: agent.HashKey(plain), Prefix: agent.PrefixOf(plain), CreatedBy: uid, CreatedAt: now}
		if err = tx.Create(&newKey).Error; err != nil {
			return err
		}
		for _, k := range keys {
			if k.RevokedAt != nil || k.ID == func() uint {
				if old != nil {
					return old.ID
				}
				return 0
			}() {
				continue
			}
			if k.ExpiresAt != nil && k.ExpiresAt.After(now) {
				revoked = append(revoked, k.Prefix)
				if err = tx.Model(&core.AgentKey{}).Where("id = ? AND revoked_at IS NULL", k.ID).Updates(map[string]any{"revoked_at": now, "revoked_by": uid}).Error; err != nil {
					return err
				}
			}
		}
		if old != nil {
			if in.Mode == "immediate" {
				err = tx.Model(&core.AgentKey{}).Where("id = ?", old.ID).Updates(map[string]any{"revoked_at": now, "revoked_by": uid}).Error
			} else {
				expiry := now.Add(time.Duration(*in.GraceMinutes) * time.Minute)
				err = tx.Model(&core.AgentKey{}).Where("id = ?", old.ID).Update("expires_at", expiry).Error
				old.ExpiresAt = &expiry
			}
			if err != nil {
				return err
			}
		}
		sum, _ := json.Marshal(map[string]any{"mode": in.Mode, "grace_minutes": func() any {
			if in.Mode == "immediate" {
				return nil
			}
			return *in.GraceMinutes
		}(), "old_key_id": func() any {
			if old != nil {
				return old.ID
			}
			return nil
		}(), "old_key_prefix": func() any {
			if old != nil {
				return old.Prefix
			}
			return nil
		}(), "new_key_id": newKey.ID, "new_key_prefix": newKey.Prefix, "old_key_expires_at": func() any {
			if old != nil {
				return old.ExpiresAt
			}
			return nil
		}(), "revoked_key_prefixes": revoked})
		ev := core.AgentEvent{At: now, ActorType: "user", ActorUserID: uid, AgentID: a.ID, AgentName: a.Name, KeyID: &newKey.ID, KeyPrefix: newKey.Prefix, Action: "key.rotate", Module: "agents", Method: "POST", Route: "/api/admin/agents/:id/rotate", Status: 200, IPMasked: maskedRealIP(c), Summary: sum, RequestID: requestIDFor(c)}
		return writeAgentEvent(tx, ev)
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return agentMissing(c)
	}
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to rotate key", nil)
	}
	v, e := buildAgentView(a)
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	var previous any
	if old != nil {
		for _, k := range v.Keys {
			if k.ID == old.ID {
				previous = k
				break
			}
		}
	}
	return c.JSON(200, map[string]any{"agent": v, "key": agentKeyViewFor(newKey), "api_key": plain, "previous_key": previous})
}
func revokeAgentKey(c echo.Context) error {
	id, ok := agentID(c)
	if !ok {
		return writeAPIError(c, 404, "key_not_found", "Key not found", nil)
	}
	kid, e := strconv.ParseUint(c.Param("key_id"), 10, 64)
	if e != nil || kid == 0 {
		return writeAPIError(c, 404, "key_not_found", "Key not found", nil)
	}
	var k core.AgentKey
	e = data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		a, err := lockAgent(tx, id)
		if err != nil {
			return err
		}
		if err = tx.Where("id = ? AND agent_id = ?", kid, id).First(&k).Error; err != nil {
			return err
		}
		if k.RevokedAt == nil {
			now := time.Now().UTC()
			if err = tx.Model(&core.AgentKey{}).Where("id = ? AND revoked_at IS NULL", k.ID).Updates(map[string]any{"revoked_at": now, "revoked_by": userID(c)}).Error; err != nil {
				return err
			}
			k.RevokedAt = &now
		}
		sum, _ := json.Marshal(map[string]any{"key_id": k.ID, "key_prefix": k.Prefix})
		ev := core.AgentEvent{At: time.Now().UTC(), ActorType: "user", ActorUserID: userID(c), AgentID: a.ID, AgentName: a.Name, KeyID: &k.ID, KeyPrefix: k.Prefix, Action: "key.revoke", Module: "agents", Method: "POST", Route: "/api/admin/agents/:id/keys/:key_id/revoke", Status: 200, IPMasked: maskedRealIP(c), Summary: sum, RequestID: requestIDFor(c)}
		return writeAgentEvent(tx, ev)
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return writeAPIError(c, 404, "key_not_found", "Key not found", nil)
	}
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to revoke key", nil)
	}
	status := "revoked"
	return c.JSON(200, agentKeyView{k.ID, k.Prefix, status, k.CreatedAt, k.LastUsedAt, k.ExpiresAt, k.RevokedAt})
}
func setAgentEnabled(c echo.Context, enabled bool) error {
	id, ok := agentID(c)
	if !ok {
		return agentMissing(c)
	}
	var a core.Agent
	action := "agent.disable"
	route := "/api/admin/agents/:id/disable"
	changes := map[string]any{"status": "disabled", "disabled_at": time.Now().UTC()}
	if enabled {
		action = "agent.enable"
		route = "/api/admin/agents/:id/enable"
		changes = map[string]any{"status": "active", "disabled_at": nil}
	}
	e := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		a, err = lockAgent(tx, id)
		if err != nil {
			return err
		}
		if err = tx.Model(&core.Agent{}).Where("id = ?", id).Updates(changes).Error; err != nil {
			return err
		}
		sum, _ := json.Marshal(map[string]any{})
		ev := core.AgentEvent{At: time.Now().UTC(), ActorType: "user", ActorUserID: userID(c), AgentID: id, AgentName: a.Name, Action: action, Module: "agents", Method: "POST", Route: route, Status: 200, IPMasked: maskedRealIP(c), Summary: sum, RequestID: requestIDFor(c)}
		return writeAgentEvent(tx, ev)
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return agentMissing(c)
	}
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to update agent", nil)
	}
	a, e = loadAgent(id)
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	v, e := buildAgentView(a)
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to read agent", nil)
	}
	return c.JSON(200, v)
}
func disableAgent(c echo.Context) error { return setAgentEnabled(c, false) }
func enableAgent(c echo.Context) error  { return setAgentEnabled(c, true) }
func deleteAgent(c echo.Context) error {
	id, ok := agentID(c)
	if !ok {
		return agentMissing(c)
	}
	e := data.DB.WithContext(c.Request().Context()).Transaction(func(tx *gorm.DB) error {
		a, err := lockAgent(tx, id)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err = tx.Model(&core.AgentKey{}).Where("agent_id = ? AND revoked_at IS NULL", id).Updates(map[string]any{"revoked_at": now, "revoked_by": userID(c)}).Error; err != nil {
			return err
		}
		if err = tx.Delete(&a).Error; err != nil {
			return err
		}
		sum, _ := json.Marshal(map[string]any{})
		ev := core.AgentEvent{At: now, ActorType: "user", ActorUserID: userID(c), AgentID: id, AgentName: a.Name, Action: "agent.delete", Module: "agents", Method: "DELETE", Route: "/api/admin/agents/:id", Status: 204, IPMasked: maskedRealIP(c), Summary: sum, RequestID: requestIDFor(c)}
		return writeAgentEvent(tx, ev)
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return agentMissing(c)
	}
	if e != nil {
		return writeAPIError(c, 500, "internal_error", "Unable to delete agent", nil)
	}
	return c.NoContent(204)
}

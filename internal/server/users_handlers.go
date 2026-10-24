package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	appI18n "github.com/HazelnutParadise/jelly-cms/internal/i18n"
	"github.com/HazelnutParadise/jelly-cms/internal/notify"
	"github.com/labstack/echo/v4"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type userRequestError string

func (e userRequestError) Error() string { return string(e) }

func noStoreUserLink(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderCacheControl, "no-store")
		return next(c)
	}
}

func userDB(c echo.Context) (*gorm.DB, error) {
	if data.DB == nil {
		return nil, errors.New("database not initialized")
	}
	return data.DB.WithContext(c.Request().Context()), nil
}

func decodeUserRequest(c echo.Context, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 1<<20))
	if err := decoder.Decode(dst); err != nil {
		return userRequestError("invalid JSON body")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return userRequestError("request body must contain one JSON value")
	}
	return nil
}

func userError(c echo.Context, err error, operation string) error {
	var transfer userContentReassignRequiredError
	switch {
	case errors.As(err, &transfer):
		return c.JSON(http.StatusConflict, transfer.counts)
	case errors.Is(err, errUserNotFound), errors.Is(err, gorm.ErrRecordNotFound),
		errors.Is(err, errUserInviteMissing):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, errUserSelfChange), errors.Is(err, errUserSelfDelete),
		errors.Is(err, errUserLastAdmin), errors.Is(err, errUserEmailTaken),
		errors.Is(err, errUserNotInvited), errors.Is(err, errUserInviteNeedsAccept):
		return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, errUserRoleInvalid), errors.Is(err, errUserReassignInvalid),
		errors.Is(err, errUserPasswordInvalid), errors.Is(err, errUserNameTaken):
		status := http.StatusBadRequest
		if errors.Is(err, errUserNameTaken) {
			status = http.StatusConflict
		}
		return c.JSON(status, map[string]string{"error": err.Error()})
	case errors.Is(err, errUserLinkGone):
		return c.JSON(http.StatusGone, map[string]string{"error": "link is invalid, expired, or already used"})
	}
	var requestErr userRequestError
	if errors.As(err, &requestErr) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": requestErr.Error()})
	}
	log.Printf("users: %s: %v", operation, err)
	return c.JSON(http.StatusInternalServerError, map[string]string{"error": operation})
}

func parseUsersPage(value string, defaultValue int) (int, error) {
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return 0, userRequestError("page and per_page must be positive integers")
	}
	return parsed, nil
}

func listAdminUsers(c echo.Context) error {
	q := strings.TrimSpace(c.QueryParam("q"))
	role, status := c.QueryParam("role"), c.QueryParam("status")
	if role != "" && !validUserRole(role) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown role"})
	}
	if status != "" && status != "active" && status != "invited" && status != "disabled" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown status"})
	}
	page, err := parseUsersPage(c.QueryParam("page"), 1)
	if err != nil {
		return userError(c, err, "could not list users")
	}
	perPage, err := parseUsersPage(c.QueryParam("per_page"), 20)
	if err != nil {
		return userError(c, err, "could not list users")
	}
	if perPage > 100 {
		perPage = 100
	}
	if page-1 > int(^uint(0)>>1)/perPage {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "page is too large"})
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not list users")
	}
	result, err := listUsers(db, q, role, status, page, perPage)
	if err != nil {
		return userError(c, err, "could not list users")
	}
	return c.JSON(http.StatusOK, result)
}

func parseUserID(value string) (uint, error) {
	id, err := strconv.ParseUint(value, 10, 64)
	if err != nil || id == 0 {
		return 0, userRequestError("user id must be a positive integer")
	}
	return uint(id), nil
}

func adminActor(c echo.Context) (*core.User, error) {
	return currentUser(c)
}

func createAdminInvite(c echo.Context) error {
	var input struct {
		Email string `json:"email"`
		Role  string `json:"role"`
		Note  string `json:"note"`
	}
	if err := decodeUserRequest(c, &input); err != nil {
		return userError(c, err, "invalid invitation")
	}
	email, err := validUserEmail(input.Email)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if !validUserRole(input.Role) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errUserRoleInvalid.Error()})
	}
	if utf8.RuneCountInString(input.Note) > 500 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "note must be at most 500 characters"})
	}
	actor, err := adminActor(c)
	if err != nil {
		return err
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not create invitation")
	}
	link, err := createUserInvitation(db, actor.ID, email, input.Role, input.Note)
	if err != nil {
		return userError(c, err, "could not create invitation")
	}
	return c.JSON(http.StatusCreated, map[string]any{
		"id": link.UserID, "invite_url": "/invite/" + url.PathEscape(link.Token),
		"expires_at": link.ExpiresAt, "emailed": false,
	})
}

func resendAdminInvite(c echo.Context) error {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		return userError(c, err, "could not resend invitation")
	}
	actor, err := adminActor(c)
	if err != nil {
		return err
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not resend invitation")
	}
	link, err := rotateUserInvitation(db, id, actor.ID)
	if err != nil {
		return userError(c, err, "could not resend invitation")
	}
	return c.JSON(http.StatusOK, map[string]any{
		"id": link.UserID, "invite_url": "/invite/" + url.PathEscape(link.Token),
		"expires_at": link.ExpiresAt, "emailed": false,
	})
}

func revokeAdminInvite(c echo.Context) error {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		return userError(c, err, "could not revoke invitation")
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not revoke invitation")
	}
	if err := revokeUserInvitation(db, id); err != nil {
		return userError(c, err, "could not revoke invitation")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "invitation revoked"})
}

func patchAdminUser(c echo.Context) error {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		return userError(c, err, "could not update user")
	}
	var patch adminUserPatch
	if err := decodeUserRequest(c, &patch); err != nil {
		return userError(c, err, "invalid user update")
	}
	if patch.Role != nil && !validUserRole(*patch.Role) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errUserRoleInvalid.Error()})
	}
	if patch.Status != nil && *patch.Status != "active" && *patch.Status != "disabled" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "status must be active or disabled"})
	}
	if patch.DisplayName != nil {
		name := strings.TrimSpace(*patch.DisplayName)
		if name == "" || utf8.RuneCountInString(name) > 120 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "display_name must contain 1 to 120 characters"})
		}
		patch.DisplayName = &name
	}
	actor, err := adminActor(c)
	if err != nil {
		return err
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not update user")
	}
	if err := updateAdminUser(db, actor.ID, id, patch); err != nil {
		return userError(c, err, "could not update user")
	}
	var user core.User
	if err := db.First(&user, id).Error; err != nil {
		return userError(c, err, "could not load updated user")
	}
	return c.JSON(http.StatusOK, map[string]any{
		"id": user.ID, "username": user.Username, "display_name": user.DisplayName,
		"email": user.Email, "avatar_url": user.AvatarURL, "role": user.Role, "status": user.Status,
	})
}

func createAdminPasswordReset(c echo.Context) error {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		return userError(c, err, "could not create password reset")
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not create password reset")
	}
	link, err := createPasswordReset(db, id)
	if err != nil {
		return userError(c, err, "could not create password reset")
	}
	return c.JSON(http.StatusOK, map[string]any{
		"reset_url": "/password-reset/" + url.PathEscape(link.Token), "emailed": false,
	})
}

func revokeAdminUserSessions(c echo.Context) error {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		return userError(c, err, "could not revoke sessions")
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not revoke sessions")
	}
	var count int64
	if err := db.Model(&core.User{}).Where("id = ?", id).Count(&count).Error; err != nil {
		return userError(c, err, "could not revoke sessions")
	}
	if count == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
	}
	if err := auth.RevokeUserLogins(db, id, ""); err != nil {
		return userError(c, err, "could not revoke sessions")
	}
	return c.JSON(http.StatusOK, map[string]bool{"revoked": true})
}

func deleteAdminUser(c echo.Context) error {
	id, err := parseUserID(c.Param("id"))
	if err != nil {
		return userError(c, err, "could not delete user")
	}
	var heirID uint
	if value := c.QueryParam("reassign_to"); value != "" {
		parsed, err := parseUserID(value)
		if err != nil {
			return userError(c, err, "could not delete user")
		}
		heirID = parsed
	}
	actor, err := adminActor(c)
	if err != nil {
		return err
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not delete user")
	}
	if _, err := deleteUser(db, actor.ID, id, heirID); err != nil {
		return userError(c, err, "could not delete user")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "user deleted"})
}

func bulkAdminUsers(c echo.Context) error {
	var input struct {
		IDs    []uint `json:"ids"`
		Action string `json:"action"`
		Role   string `json:"role"`
	}
	if err := decodeUserRequest(c, &input); err != nil {
		return userError(c, err, "invalid bulk request")
	}
	if len(input.IDs) > 100 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "ids must contain at most 100 users"})
	}
	for _, id := range input.IDs {
		if id == 0 {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "ids must be positive integers"})
		}
	}
	switch input.Action {
	case "disable", "enable", "delete":
	case "set_role":
		if !validUserRole(input.Role) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": errUserRoleInvalid.Error()})
		}
	default:
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "unknown bulk action"})
	}
	actor, err := adminActor(c)
	if err != nil {
		return err
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not process bulk request")
	}
	result, err := bulkUpdateUsers(db, actor.ID, input.IDs, input.Action, input.Role)
	if err != nil {
		return userError(c, err, "could not process bulk request")
	}
	return c.JSON(http.StatusOK, result)
}

type rolePermission struct {
	Key string `json:"key"`
}

type roleMatrixRole struct {
	Key         string          `json:"key"`
	Permissions map[string]bool `json:"permissions"`
}

func listAdminRoles(c echo.Context) error {
	permissions := []rolePermission{
		{Key: "admin_access"},
		{Key: "users_manage"},
		{Key: "settings_manage"},
		{Key: "themes_manage"},
		{Key: "plugins_manage"},
		{Key: "content_manage"},
		{Key: "own_content_manage"},
		{Key: "comments_manage"},
		{Key: "orders_manage"},
		{Key: "products_manage"},
	}
	grants := map[string][]string{
		"admin_access":       {"admin", "editor", "author"},
		"users_manage":       {"admin"},
		"settings_manage":    {"admin"},
		"themes_manage":      {"admin"},
		"plugins_manage":     {"admin"},
		"content_manage":     {"admin", "editor"},
		"own_content_manage": {"author"},
		"comments_manage":    {"admin", "editor"},
		"orders_manage":      {"admin"},
		"products_manage":    {"admin"},
	}
	roles := make([]roleMatrixRole, 0, len(userRoles))
	for _, role := range userRoles {
		item := roleMatrixRole{Key: role, Permissions: make(map[string]bool, len(permissions))}
		for _, permission := range permissions {
			item.Permissions[permission.Key] = containsUserRole(grants[permission.Key], role)
		}
		roles = append(roles, item)
	}
	return c.JSON(http.StatusOK, map[string]any{"roles": roles, "permissions": permissions})
}

func containsUserRole(roles []string, role string) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}

func showInvitePage(c echo.Context) error {
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not load invitation")
	}
	token := c.Param("token")
	_, user, err := lookupUserLink(db, token, "invite")
	if err != nil {
		if errors.Is(err, errUserLinkGone) {
			if renderErr := renderUserLinkPage(c, "invite.html", "", core.User{}, true); renderErr != nil {
				return userError(c, renderErr, "could not render invitation page")
			}
			return nil
		}
		return userError(c, err, "could not load invitation")
	}
	return renderUserLinkPage(c, "invite.html", token, user, false)
}

func showPasswordResetPage(c echo.Context) error {
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not load password reset")
	}
	token := c.Param("token")
	_, user, err := lookupUserLink(db, token, "reset")
	if err != nil {
		if errors.Is(err, errUserLinkGone) {
			if renderErr := renderUserLinkPage(c, "password_reset.html", "", core.User{}, true); renderErr != nil {
				return userError(c, renderErr, "could not render password reset page")
			}
			return nil
		}
		return userError(c, err, "could not load password reset")
	}
	return renderUserLinkPage(c, "password_reset.html", token, user, false)
}

func renderUserLinkPage(c echo.Context, page, token string, user core.User, linkExpired bool) error {
	lang := "en"
	if requested, ok := c.Get("lang").(string); ok && strings.HasPrefix(strings.ToLower(requested), "zh") {
		lang = "zh-TW"
	}
	translations := make(map[string]string, len(accountPageMessageIDs))
	localizer := appI18n.GetLocalizer(lang)
	for _, key := range accountPageMessageIDs {
		value, err := localizer.Localize(&i18n.LocalizeConfig{MessageID: key})
		if err != nil {
			translations[key] = key
		} else {
			translations[key] = value
		}
	}
	title := "Accept invitation"
	if page == "password_reset.html" {
		title = "Reset password"
	}
	if lang == "zh-TW" {
		title = "接受邀請"
		if page == "password_reset.html" {
			title = "重設密碼"
		}
	}
	switchURL, switchLabel := "?lang=zh-TW", translations["switchToChinese"]
	if lang == "zh-TW" {
		switchURL, switchLabel = "?lang=en", translations["switchToEnglish"]
	}
	backURL, backLabel := "/login?lang="+url.QueryEscape(lang), translations["backToLogin"]
	tmpl, err := template.New("account_layout.html").ParseFiles(
		"web/admin/account_layout.html", "web/admin/"+page,
	)
	if err != nil {
		return fmt.Errorf("parse account link template %s: %w", page, err)
	}
	var body bytes.Buffer
	if err := tmpl.ExecuteTemplate(&body, "account_layout", map[string]any{
		"Lang": lang, "T": translations, "PageTitle": title,
		"SwitchURL": switchURL, "SwitchLabel": switchLabel,
		"BackURL": backURL, "BackLabel": backLabel,
		"Token": token, "InviteToken": token, "Email": user.Email, "Username": user.Username,
		"LinkExpired": linkExpired,
	}); err != nil {
		return err
	}
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTMLCharsetUTF8)
	if linkExpired {
		c.Response().WriteHeader(http.StatusGone)
	}
	_, err = body.WriteTo(c.Response().Writer)
	return err
}

func acceptInvite(c echo.Context) error {
	var input struct {
		Token       string `json:"token"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeUserRequest(c, &input); err != nil {
		return userError(c, err, "invalid invitation acceptance")
	}
	if utf8.RuneCountInString(input.Password) < 8 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errUserPasswordInvalid.Error()})
	}
	if len(input.Password) > 72 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at most 72 bytes"})
	}
	if input.Username != "" && !validUsername(input.Username) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "username must be 1 to 50 letters, numbers, dots, dashes, or underscores"})
	}
	if utf8.RuneCountInString(input.DisplayName) > 120 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "display_name must be at most 120 characters"})
	}
	if input.Token == "" {
		return c.JSON(http.StatusGone, map[string]string{"error": "link is invalid, expired, or already used"})
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not accept invitation")
	}
	if _, user, err := lookupUserLink(db, input.Token, "invite"); err != nil {
		return userError(c, err, "could not accept invitation")
	} else if input.Username == "" {
		input.Username = user.Username
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return userError(c, err, "could not accept invitation")
	}
	if err := consumeInvitation(db, input.Token, input.Username, strings.TrimSpace(input.DisplayName), string(passwordHash)); err != nil {
		return userError(c, err, "could not accept invitation")
	}
	if err := notify.Emit(c.Request().Context(), db, notify.Event{
		Kind: notify.KindUserInviteAccepted, Category: notify.CategorySystem,
		Params:     map[string]any{"username": input.Username},
		Recipients: notify.Recipients{Roles: []string{"admin"}},
	}); err != nil {
		log.Printf("users: emit invitation accepted notification: %v", err)
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "invitation accepted"})
}

func acceptPasswordReset(c echo.Context) error {
	var input struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeUserRequest(c, &input); err != nil {
		return userError(c, err, "invalid password reset")
	}
	if utf8.RuneCountInString(input.Password) < 8 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": errUserPasswordInvalid.Error()})
	}
	if len(input.Password) > 72 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at most 72 bytes"})
	}
	if input.Token == "" {
		return c.JSON(http.StatusGone, map[string]string{"error": "link is invalid, expired, or already used"})
	}
	db, err := userDB(c)
	if err != nil {
		return userError(c, err, "could not reset password")
	}
	if _, _, err := lookupUserLink(db, input.Token, "reset"); err != nil {
		return userError(c, err, "could not reset password")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return userError(c, err, "could not reset password")
	}
	if err := consumePasswordReset(db, input.Token, string(passwordHash)); err != nil {
		return userError(c, err, "could not reset password")
	}
	return c.JSON(http.StatusOK, map[string]string{"message": "password reset"})
}

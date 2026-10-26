package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/auth"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	errUserNotFound          = errors.New("user not found")
	errUserSelfChange        = errors.New("you cannot change your own role or status")
	errUserSelfDelete        = errors.New("you cannot delete your own account")
	errUserLastAdmin         = errors.New("at least one active administrator must remain")
	errUserInviteMissing     = errors.New("active invitation not found")
	errUserNotInvited        = errors.New("user is not awaiting an invitation")
	errUserEmailTaken        = errors.New("email is already in use")
	errUserRoleInvalid       = errors.New("unknown role")
	errUserLinkGone          = errors.New("link is invalid, expired, or already used")
	errUserPasswordInvalid   = errors.New("password must be at least 8 characters")
	errUserNameTaken         = errors.New("username is already in use")
	errUserInviteNeedsAccept = errors.New("invited users must accept their invitation")
	errUserReassignInvalid   = errors.New("reassignment target must be an active writer")
	errUserReassignRequired  = errors.New("content must be reassigned before deleting this user")
	errUserHasOrders         = errors.New("this account has orders; disable it to preserve order history")
)

var userRoles = []string{"admin", "editor", "author", "customer"}

func validUserRole(role string) bool {
	for _, known := range userRoles {
		if role == known {
			return true
		}
	}
	return false
}

type adminUserListItem struct {
	ID              uint       `json:"id"`
	Username        string     `json:"username"`
	DisplayName     string     `json:"display_name"`
	Email           string     `json:"email"`
	AvatarURL       string     `json:"avatar_url"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	LastLoginDevice string     `json:"last_login_device"`
	InviteExpiresAt *time.Time `json:"invite_expires_at"`
	PendingEmail    string     `json:"pending_email"`
}

type adminUserCounts struct {
	All    int64            `json:"all"`
	Role   map[string]int64 `json:"role"`
	Status map[string]int64 `json:"status"`
}

type adminUsersList struct {
	Items  []adminUserListItem `json:"items"`
	Total  int64               `json:"total"`
	Counts adminUserCounts     `json:"counts"`
}

func listUsers(db *gorm.DB, q, role, status string, page, perPage int) (adminUsersList, error) {
	var result adminUsersList
	result.Items = []adminUserListItem{}
	result.Counts.Role = make(map[string]int64)
	result.Counts.Status = make(map[string]int64)

	if err := db.Model(&core.User{}).Count(&result.Counts.All).Error; err != nil {
		return result, err
	}
	var roleCounts []struct {
		Role  string
		Count int64
	}
	if err := db.Model(&core.User{}).Select("role, COUNT(*) AS count").Group("role").Scan(&roleCounts).Error; err != nil {
		return result, err
	}
	for _, item := range roleCounts {
		result.Counts.Role[item.Role] = item.Count
	}
	var statusCounts []struct {
		Status string
		Count  int64
	}
	if err := db.Model(&core.User{}).Select("status, COUNT(*) AS count").Group("status").Scan(&statusCounts).Error; err != nil {
		return result, err
	}
	for _, item := range statusCounts {
		result.Counts.Status[item.Status] = item.Count
	}

	query := db.Model(&core.User{})
	if q != "" {
		pattern := "%" + q + "%"
		query = query.Where("(username ILIKE ? OR display_name ILIKE ? OR email ILIKE ?)", pattern, pattern, pattern)
	}
	if role != "" {
		query = query.Where("role = ?", role)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&result.Total).Error; err != nil {
		return result, err
	}

	offset := (page - 1) * perPage
	err := query.
		Select(`users.id, users.username, users.display_name, users.email, users.avatar_url, users.role, users.status,
			users.last_login_at, users.pending_email,
			COALESCE((SELECT ls.device_label FROM login_sessions AS ls WHERE ls.user_id = users.id ORDER BY ls.created_at DESC LIMIT 1), '') AS last_login_device,
			(SELECT ui.expires_at FROM user_invites AS ui
			 WHERE ui.user_id = users.id AND ui.kind = 'invite' AND ui.accepted_at IS NULL AND ui.revoked_at IS NULL
			 ORDER BY ui.created_at DESC LIMIT 1) AS invite_expires_at`).
		Order("users.id ASC").
		Limit(perPage).
		Offset(offset).
		Scan(&result.Items).Error
	return result, err
}

type userContentCounts struct {
	Posts    int64 `json:"posts"`
	Pages    int64 `json:"pages"`
	Comments int64 `json:"comments"`
}

func (counts userContentCounts) total() int64 {
	return counts.Posts + counts.Pages + counts.Comments
}

func activeAdminsForUpdate(tx *gorm.DB) ([]core.User, error) {
	var admins []core.User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("role = ? AND status = ?", "admin", "active").
		Order("id ASC").Find(&admins).Error
	return admins, err
}

func userForUpdate(tx *gorm.DB, id uint) (core.User, error) {
	var user core.User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return user, errUserNotFound
	}
	return user, err
}

type adminUserPatch struct {
	Role        *string `json:"role"`
	Status      *string `json:"status"`
	DisplayName *string `json:"display_name"`
}

func updateAdminUser(db *gorm.DB, actorID, targetID uint, patch adminUserPatch) error {
	return db.Transaction(func(tx *gorm.DB) error {
		admins, err := activeAdminsForUpdate(tx)
		if err != nil {
			return err
		}
		user, err := userForUpdate(tx, targetID)
		if err != nil {
			return err
		}
		return updateAdminUserLocked(tx, admins, actorID, &user, patch)
	})
}

func updateAdminUserLocked(tx *gorm.DB, activeAdmins []core.User, actorID uint, user *core.User, patch adminUserPatch) error {
	if (patch.Role != nil || patch.Status != nil) && user.ID == actorID {
		return errUserSelfChange
	}
	if patch.Role != nil && !validUserRole(*patch.Role) {
		return errUserRoleInvalid
	}
	if patch.Status != nil && *patch.Status != "active" && *patch.Status != "disabled" {
		return errors.New("status must be active or disabled")
	}
	if patch.DisplayName != nil {
		name := strings.TrimSpace(*patch.DisplayName)
		if name == "" || utf8.RuneCountInString(name) > 120 {
			return errors.New("display_name must contain 1 to 120 characters")
		}
		patch.DisplayName = &name
	}
	if user.Status == "invited" && patch.Status != nil && *patch.Status == "active" {
		return errUserInviteNeedsAccept
	}

	nextRole, nextStatus := user.Role, user.Status
	if patch.Role != nil {
		nextRole = *patch.Role
	}
	if patch.Status != nil {
		nextStatus = *patch.Status
	}
	if user.Role == "admin" && user.Status == "active" && (nextRole != "admin" || nextStatus != "active") && len(activeAdmins) <= 1 {
		return errUserLastAdmin
	}

	updates := make(map[string]any)
	if patch.Role != nil {
		updates["role"] = *patch.Role
	}
	if patch.DisplayName != nil {
		updates["display_name"] = *patch.DisplayName
	}
	if patch.Status != nil {
		updates["status"] = *patch.Status
		if *patch.Status == "disabled" {
			now := time.Now().UTC()
			updates["disabled_at"] = &now
			if err := auth.RevokeUserLogins(tx, user.ID, ""); err != nil {
				return err
			}
		} else {
			updates["disabled_at"] = nil
		}
		if *patch.Status != "invited" {
			if err := tx.Where("user_id = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL", user.ID).
				Delete(&core.UserInvite{}).Error; err != nil {
				return err
			}
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return tx.Model(&core.User{}).Where("id = ?", user.ID).Updates(updates).Error
}

func countUserContent(tx *gorm.DB, userID uint) (userContentCounts, error) {
	var counts userContentCounts
	if err := tx.Unscoped().Model(&core.Post{}).Where("author_id = ? AND type = ?", userID, "post").Count(&counts.Posts).Error; err != nil {
		return counts, err
	}
	if err := tx.Unscoped().Model(&core.Post{}).Where("author_id = ? AND type = ?", userID, "page").Count(&counts.Pages).Error; err != nil {
		return counts, err
	}
	if err := tx.Unscoped().Model(&core.Comment{}).Where("author_id = ?", userID).Count(&counts.Comments).Error; err != nil {
		return counts, err
	}
	return counts, nil
}

type userContentReassignRequiredError struct {
	counts userContentCounts
}

func (e userContentReassignRequiredError) Error() string { return errUserReassignRequired.Error() }
func (e userContentReassignRequiredError) Unwrap() error { return errUserReassignRequired }

func deleteUser(db *gorm.DB, actorID, targetID, reassignTo uint) (userContentCounts, error) {
	var counts userContentCounts
	err := db.Transaction(func(tx *gorm.DB) error {
		admins, err := activeAdminsForUpdate(tx)
		if err != nil {
			return err
		}
		user, err := userForUpdate(tx, targetID)
		if err != nil {
			return err
		}
		return deleteUserLocked(tx, admins, actorID, user, reassignTo, &counts)
	})
	return counts, err
}

func deleteUserLocked(tx *gorm.DB, activeAdmins []core.User, actorID uint, user core.User, reassignTo uint, counts *userContentCounts) error {
	if user.ID == actorID {
		return errUserSelfDelete
	}
	if user.Role == "admin" && user.Status == "active" && len(activeAdmins) <= 1 {
		return errUserLastAdmin
	}
	var order core.Order
	if err := tx.Select("id").Where("user_id = ?", user.ID).Limit(1).Take(&order).Error; err == nil {
		return errUserHasOrders
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	found, err := countUserContent(tx, user.ID)
	if err != nil {
		return err
	}
	*counts = found
	if found.total() > 0 && reassignTo == 0 {
		return userContentReassignRequiredError{counts: found}
	}
	if reassignTo != 0 {
		if reassignTo == user.ID {
			return errUserReassignInvalid
		}
		var heir core.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&heir, reassignTo).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserReassignInvalid
			}
			return err
		}
		if heir.Status != "active" || (heir.Role != "admin" && heir.Role != "editor" && heir.Role != "author") {
			return errUserReassignInvalid
		}
		if found.Posts+found.Pages > 0 {
			if err := tx.Unscoped().Model(&core.Post{}).Where("author_id = ?", user.ID).Update("author_id", heir.ID).Error; err != nil {
				return err
			}
		}
		if found.Comments > 0 {
			if err := tx.Unscoped().Model(&core.Comment{}).Where("author_id = ?", user.ID).Update("author_id", heir.ID).Error; err != nil {
				return err
			}
		}
	}

	if err := auth.RevokeUserLogins(tx, user.ID, ""); err != nil {
		return err
	}
	if err := tx.Exec("SELECT set_config('jelly.actor_id', ?, true)", strconv.FormatUint(uint64(actorID), 10)).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("user_id = ?", user.ID).Delete(&core.OAuthIdentity{}).Error; err != nil {
		return err
	}
	if err := tx.Model(&core.Revision{}).Where("user_id = ?", user.ID).Update("user_id", nil).Error; err != nil {
		return err
	}
	return tx.Delete(&core.User{}, user.ID).Error
}

type bulkUserSkipped struct {
	ID     uint   `json:"id"`
	Reason string `json:"reason"`
}

type bulkUserResult struct {
	Updated int               `json:"updated"`
	Skipped []bulkUserSkipped `json:"skipped"`
}

func bulkUpdateUsers(db *gorm.DB, actorID uint, ids []uint, action, role string) (bulkUserResult, error) {
	result := bulkUserResult{Skipped: []bulkUserSkipped{}}
	seen := make(map[uint]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			result.Skipped = append(result.Skipped, bulkUserSkipped{ID: id, Reason: "duplicate id"})
			continue
		}
		seen[id] = true

		err := db.Transaction(func(tx *gorm.DB) error {
			admins, err := activeAdminsForUpdate(tx)
			if err != nil {
				return err
			}
			user, err := userForUpdate(tx, id)
			if err != nil {
				return err
			}
			switch action {
			case "disable":
				status := "disabled"
				return updateAdminUserLocked(tx, admins, actorID, &user, adminUserPatch{Status: &status})
			case "enable":
				status := "active"
				return updateAdminUserLocked(tx, admins, actorID, &user, adminUserPatch{Status: &status})
			case "set_role":
				return updateAdminUserLocked(tx, admins, actorID, &user, adminUserPatch{Role: &role})
			case "delete":
				var counts userContentCounts
				return deleteUserLocked(tx, admins, actorID, user, 0, &counts)
			default:
				return fmt.Errorf("unsupported bulk action %q", action)
			}
		})
		if err == nil {
			result.Updated++
			continue
		}
		if errors.Is(err, errUserNotFound) || errors.Is(err, errUserSelfChange) || errors.Is(err, errUserSelfDelete) ||
			errors.Is(err, errUserLastAdmin) || errors.Is(err, errUserReassignRequired) || errors.Is(err, errUserInviteNeedsAccept) ||
			errors.Is(err, errUserHasOrders) {
			result.Skipped = append(result.Skipped, bulkUserSkipped{ID: id, Reason: err.Error()})
			continue
		}
		return result, err
	}
	return result, nil
}

func newUserLinkToken() (raw, hash string, err error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(token[:])
	digest := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(digest[:]), nil
}

func userLinkHash(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func validUserEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	if len(email) > 255 {
		return "", errors.New("email must be at most 255 characters")
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || strings.ContainsAny(email, "\r\n") {
		return "", errors.New("email address is invalid")
	}
	return email, nil
}

func inviteUsernameBase(email string) string {
	local := strings.SplitN(email, "@", 2)[0]
	var base strings.Builder
	for _, r := range strings.ToLower(local) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			base.WriteRune(r)
		}
	}
	name := strings.Trim(base.String(), "._-")
	if name == "" {
		name = "member"
	}
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

func uniqueInviteUsername(tx *gorm.DB, email string) (string, error) {
	base := inviteUsernameBase(email)
	for suffix := 1; suffix <= 1000; suffix++ {
		candidate := base
		if suffix > 1 {
			tail := "-" + strconv.Itoa(suffix)
			if len(candidate)+len(tail) > 50 {
				candidate = candidate[:50-len(tail)]
			}
			candidate += tail
		}
		var count int64
		if err := tx.Model(&core.User{}).Where("LOWER(username) = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", errors.New("could not allocate a unique username")
}

type issuedUserLink struct {
	UserID    uint
	Token     string
	ExpiresAt time.Time
}

func createUserInvitation(db *gorm.DB, actorID uint, email, role, note string) (issuedUserLink, error) {
	var link issuedUserLink
	raw, tokenHash, err := newUserLinkToken()
	if err != nil {
		return link, err
	}
	expires := time.Now().UTC().Add(7 * 24 * time.Hour)
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "user-invite-email:"+email).Error; err != nil {
			return err
		}
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "user-invite-name:"+inviteUsernameBase(email)).Error; err != nil {
			return err
		}
		var existing int64
		if err := tx.Model(&core.User{}).Where("LOWER(email) = ?", email).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			return errUserEmailTaken
		}
		username, err := uniqueInviteUsername(tx, email)
		if err != nil {
			return err
		}
		user := core.User{Username: username, Email: email, Role: role, Provider: "local", Status: "invited"}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		invitedBy := actorID
		invite := core.UserInvite{
			UserID: user.ID, Kind: "invite", TokenHash: tokenHash, Role: role,
			Note: note, InvitedBy: &invitedBy, ExpiresAt: expires,
		}
		if err := tx.Create(&invite).Error; err != nil {
			return err
		}
		link.UserID = user.ID
		return nil
	})
	if err != nil {
		return issuedUserLink{}, err
	}
	link.Token, link.ExpiresAt = raw, expires
	return link, nil
}

func rotateUserInvitation(db *gorm.DB, userID, actorID uint) (issuedUserLink, error) {
	var link issuedUserLink
	raw, tokenHash, err := newUserLinkToken()
	if err != nil {
		return link, err
	}
	expires := time.Now().UTC().Add(7 * 24 * time.Hour)
	err = db.Transaction(func(tx *gorm.DB) error {
		user, err := userForUpdate(tx, userID)
		if err != nil {
			return err
		}
		if user.Status != "invited" {
			return errUserNotInvited
		}
		var previous core.UserInvite
		if err := tx.Where("user_id = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL", userID).
			Order("created_at DESC").First(&previous).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserInviteMissing
			}
			return err
		}
		if err := tx.Where("user_id = ? AND kind = 'invite'", userID).Delete(&core.UserInvite{}).Error; err != nil {
			return err
		}
		invitedBy := actorID
		invite := core.UserInvite{UserID: userID, Kind: "invite", TokenHash: tokenHash, Role: user.Role, Note: previous.Note, InvitedBy: &invitedBy, ExpiresAt: expires}
		if err := tx.Create(&invite).Error; err != nil {
			return err
		}
		link.UserID = userID
		return nil
	})
	if err != nil {
		return issuedUserLink{}, err
	}
	link.Token, link.ExpiresAt = raw, expires
	return link, nil
}

func revokeUserInvitation(db *gorm.DB, userID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		user, err := userForUpdate(tx, userID)
		if err != nil {
			return err
		}
		if user.Status != "invited" {
			return errUserNotInvited
		}
		var count int64
		if err := tx.Model(&core.UserInvite{}).
			Where("user_id = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL", userID).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return errUserInviteMissing
		}
		return tx.Delete(&core.User{}, userID).Error
	})
}

func createPasswordReset(db *gorm.DB, userID uint) (issuedUserLink, error) {
	var link issuedUserLink
	raw, tokenHash, err := newUserLinkToken()
	if err != nil {
		return link, err
	}
	expires := time.Now().UTC().Add(time.Hour)
	err = db.Transaction(func(tx *gorm.DB) error {
		user, err := userForUpdate(tx, userID)
		if err != nil {
			return err
		}
		if user.Status == "invited" {
			return errUserNotInvited
		}
		if err := tx.Where("user_id = ? AND kind = 'reset'", userID).Delete(&core.UserInvite{}).Error; err != nil {
			return err
		}
		invite := core.UserInvite{UserID: userID, Kind: "reset", TokenHash: tokenHash, ExpiresAt: expires}
		if err := tx.Create(&invite).Error; err != nil {
			return err
		}
		link.UserID = userID
		return nil
	})
	if err != nil {
		return issuedUserLink{}, err
	}
	link.Token, link.ExpiresAt = raw, expires
	return link, nil
}

func lookupUserLink(db *gorm.DB, raw, kind string) (core.UserInvite, core.User, error) {
	var invite core.UserInvite
	if len(raw) < 32 || len(raw) > 128 {
		return invite, core.User{}, errUserLinkGone
	}
	err := db.Where("token_hash = ? AND kind = ? AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?",
		userLinkHash(raw), kind, time.Now().UTC()).First(&invite).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return invite, core.User{}, errUserLinkGone
	}
	if err != nil {
		return invite, core.User{}, err
	}
	var user core.User
	if err := db.First(&user, invite.UserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return invite, user, errUserLinkGone
		}
		return invite, user, err
	}
	if kind == "invite" && user.Status != "invited" {
		return invite, user, errUserLinkGone
	}
	if kind == "reset" && user.Status == "invited" {
		return invite, user, errUserLinkGone
	}
	return invite, user, nil
}

func validUsername(username string) bool {
	if username == "" || len(username) > 50 {
		return false
	}
	for _, r := range username {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func consumeInvitation(db *gorm.DB, raw, username, displayName, passwordHash string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var candidate core.UserInvite
		err := tx.
			Where("token_hash = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?",
				userLinkHash(raw), time.Now().UTC()).
			First(&candidate).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errUserLinkGone
		}
		if err != nil {
			return err
		}
		user, err := userForUpdate(tx, candidate.UserID)
		if err != nil {
			return errUserLinkGone
		}
		if user.Status != "invited" {
			return errUserLinkGone
		}
		var invite core.UserInvite
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND token_hash = ? AND kind = 'invite' AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?",
				candidate.ID, userLinkHash(raw), time.Now().UTC()).
			First(&invite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserLinkGone
			}
			return err
		}
		updates := map[string]any{
			"password": passwordHash, "status": "active", "email_verified_at": time.Now().UTC(), "disabled_at": nil,
		}
		if username != "" {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "user-accept-name:"+strings.ToLower(username)).Error; err != nil {
				return err
			}
			var count int64
			if err := tx.Model(&core.User{}).Where("LOWER(username) = ? AND id <> ?", strings.ToLower(username), user.ID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return errUserNameTaken
			}
			updates["username"] = username
		}
		if displayName != "" {
			updates["display_name"] = displayName
		}
		if err := tx.Model(&core.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Model(&core.UserInvite{}).Where("id = ?", invite.ID).Update("accepted_at", now).Error
	})
}

func consumePasswordReset(db *gorm.DB, raw, passwordHash string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var candidate core.UserInvite
		err := tx.
			Where("token_hash = ? AND kind = 'reset' AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?",
				userLinkHash(raw), time.Now().UTC()).
			First(&candidate).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errUserLinkGone
		}
		if err != nil {
			return err
		}
		user, err := userForUpdate(tx, candidate.UserID)
		if err != nil {
			return errUserLinkGone
		}
		if user.Status == "invited" {
			return errUserLinkGone
		}
		var invite core.UserInvite
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND token_hash = ? AND kind = 'reset' AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?",
				candidate.ID, userLinkHash(raw), time.Now().UTC()).
			First(&invite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserLinkGone
			}
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&core.User{}).Where("id = ?", user.ID).
			Updates(map[string]any{"password": passwordHash, "password_changed_at": now}).Error; err != nil {
			return err
		}
		if err := auth.RevokeUserLogins(tx, user.ID, ""); err != nil {
			return err
		}
		return tx.Model(&core.UserInvite{}).Where("id = ?", invite.ID).Update("accepted_at", now).Error
	})
}

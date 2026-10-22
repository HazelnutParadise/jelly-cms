package install

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	configPath  = "data/config.json"
	pendingPath = "data/install-pending.json"

	siteTitleKey      = "site_title"
	installationIDKey = "installation_id"

	defaultTimezone = "Asia/Taipei"

	// advisoryLockKey serializes installations that target the same database
	// from different processes. The value is just a stable namespace.
	advisoryLockKey int64 = 0x4a454c4c59494e53
)

var (
	// ErrAlreadyInstalled is returned when an installation must not run because
	// the site is already installed or an installation is still pending.
	ErrAlreadyInstalled = errors.New("jelly-cms is already installed")

	// installMutex serializes every installation entry point inside this
	// process. The functions that are called while it is held do not lock it
	// again, so a public call can safely call into the same package.
	installMutex sync.Mutex
)

// ValidationError reports an invalid installation input. It is returned before
// any persistent state is changed and can be inspected with errors.As.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func validationError(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

// IsInstalled reports whether the site must be treated as installed. It only
// looks at the loaded configuration and the installation journals; it never
// counts users, so a database outage cannot reopen installation.
func IsInstalled() bool {
	installMutex.Lock()
	defer installMutex.Unlock()
	return isInstalledLocked()
}

func isInstalledLocked() bool {
	if config.GlobalConfig != nil {
		return true
	}
	if regularFileExists(configPath) {
		return true
	}
	return regularFileExists(pendingPath)
}

func regularFileExists(name string) bool {
	info, err := os.Stat(name)
	if err == nil {
		return info.Mode().IsRegular()
	}
	if errors.Is(err, os.ErrNotExist) {
		return false
	}

	// A parent that is a regular file means the path cannot exist (ENOTDIR).
	// For any other unexpected error, stay conservative and treat the path as
	// present so installation is not reopened.
	parent, parentErr := os.Stat(filepath.Dir(name))
	if parentErr == nil && parent.Mode().IsRegular() {
		return false
	}
	return true
}

// InstallRequest defines the data needed for installation.
type InstallRequest struct {
	// DB Config
	DBHost     string `json:"db_host" form:"db_host"`
	DBPort     string `json:"db_port" form:"db_port"`
	DBUser     string `json:"db_user" form:"db_user"`
	DBPass     string `json:"db_pass" form:"db_pass"`
	DBName     string `json:"db_name" form:"db_name"`
	DBTimezone string `json:"db_timezone" form:"db_timezone"`

	// Site Config
	SiteTitle  string `json:"site_title" form:"site_title"`
	AdminUser  string `json:"admin_user" form:"admin_user"`
	AdminEmail string `json:"admin_email" form:"admin_email"`
	AdminPass  string `json:"admin_pass" form:"admin_pass"`
}

// TestConnection validates the database fields and opens a candidate
// connection. It never replaces the globals used by the running site.
func TestConnection(req InstallRequest) error {
	installMutex.Lock()
	defer installMutex.Unlock()

	req, err := normalizeDatabase(req)
	if err != nil {
		return err
	}

	db, err := data.Open(databaseConfig(req))
	if err != nil {
		return err
	}
	closeDatabase(db)
	return nil
}

// PerformInstallation validates the request, creates the site database
// transaction, saves the configuration and finally publishes the globals.
func PerformInstallation(req InstallRequest) (err error) {
	installMutex.Lock()
	defer installMutex.Unlock()

	req, err = normalizeDatabase(req)
	if err != nil {
		return err
	}
	if err = validateSite(&req); err != nil {
		return err
	}

	if isInstalledLocked() {
		return ErrAlreadyInstalled
	}

	release, err := acquireDataDirLock()
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil && err == nil {
			err = releaseErr
		}
	}()

	// Another process may have written a journal or config while this call was
	// waiting for the data directory lock.
	if isInstalledLocked() {
		return ErrAlreadyInstalled
	}

	cfg := &config.Config{Database: databaseConfig(req)}

	db, err := data.Open(cfg.Database)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			closeDatabase(db)
		}
	}()

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.AdminPass), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash the administrator password: %w", err)
	}

	installationID, err := newInstallationID()
	if err != nil {
		return fmt.Errorf("generate the installation id: %w", err)
	}

	pending := pendingInstallation{
		Database:       cfg.Database,
		InstallationID: installationID,
	}
	if err = runInstallationTransaction(db, pending, req, passwordHash); err != nil {
		return err
	}

	// The transaction is committed. The journal stays until the configuration
	// file is safely in place, so a crash here can be recovered from the
	// committed installation marker.
	if err = config.Save(cfg); err != nil {
		return fmt.Errorf("save the installation configuration: %w", err)
	}

	config.GlobalConfig = cfg
	data.DB = db
	published = true

	if err = removePendingInstallation(); err != nil {
		return err
	}
	return nil
}

func runInstallationTransaction(db *gorm.DB, pending pendingInstallation, req InstallRequest, passwordHash []byte) error {
	tx := db.Begin()
	if tx.Error != nil {
		return fmt.Errorf("begin the installation transaction: %w", tx.Error)
	}

	pendingWritten := false
	abort := func(cause error) error {
		if rollbackErr := tx.Rollback().Error; rollbackErr != nil {
			return fmt.Errorf("%w (rollback failed, pending installation kept: %v)", cause, rollbackErr)
		}
		if pendingWritten {
			if removeErr := removePendingInstallation(); removeErr != nil {
				return fmt.Errorf("%w (remove pending installation: %v)", cause, removeErr)
			}
		}
		return cause
	}

	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", advisoryLockKey).Error; err != nil {
		return abort(fmt.Errorf("lock the database for installation: %w", err))
	}

	if err := data.MigrateDB(tx); err != nil {
		return abort(fmt.Errorf("migrate the installation database: %w", err))
	}

	var userCount int64
	if err := tx.Model(&core.User{}).Count(&userCount).Error; err != nil {
		return abort(fmt.Errorf("check for existing users: %w", err))
	}
	var optionCount int64
	if err := tx.Model(&core.Option{}).Count(&optionCount).Error; err != nil {
		return abort(fmt.Errorf("check for existing options: %w", err))
	}
	if userCount > 0 || optionCount > 0 {
		return abort(ErrAlreadyInstalled)
	}

	// Store the journal before the administrator exists so that an unwritable
	// data directory is detected before anything is committed.
	if err := writePendingInstallation(pending); err != nil {
		return abort(err)
	}
	pendingWritten = true

	admin := core.User{
		Username: req.AdminUser,
		Email:    req.AdminEmail,
		Password: string(passwordHash),
		Role:     "admin",
		Provider: "local",
	}
	if err := tx.Create(&admin).Error; err != nil {
		return abort(fmt.Errorf("create the administrator: %w", err))
	}
	if err := tx.Create(&core.Option{Key: siteTitleKey, Value: req.SiteTitle}).Error; err != nil {
		return abort(fmt.Errorf("save the site title: %w", err))
	}
	if err := tx.Create(&core.Option{Key: installationIDKey, Value: pending.InstallationID}).Error; err != nil {
		return abort(fmt.Errorf("save the installation marker: %w", err))
	}

	// A commit error means the result is unknown: the transaction may have
	// reached the server. Keep the journal so RecoverInstallation can verify
	// the marker instead of guessing.
	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("commit the installation transaction: %w", err)
	}
	return nil
}

func normalizeDatabase(req InstallRequest) (InstallRequest, error) {
	req.DBHost = strings.TrimSpace(req.DBHost)
	req.DBPort = strings.TrimSpace(req.DBPort)
	req.DBUser = strings.TrimSpace(req.DBUser)
	req.DBName = strings.TrimSpace(req.DBName)
	req.DBTimezone = strings.TrimSpace(req.DBTimezone)

	switch {
	case req.DBHost == "":
		return req, validationError("db_host", "database host is required")
	case len(req.DBHost) > 255:
		return req, validationError("db_host", "database host is too long")
	case req.DBUser == "":
		return req, validationError("db_user", "database user is required")
	case len(req.DBUser) > 63:
		return req, validationError("db_user", "database user is too long")
	case req.DBName == "":
		return req, validationError("db_name", "database name is required")
	case len(req.DBName) > 63:
		return req, validationError("db_name", "database name is too long")
	}

	port, err := strconv.Atoi(req.DBPort)
	if err != nil || port < 1 || port > 65535 {
		return req, validationError("db_port", "database port must be a number between 1 and 65535")
	}
	req.DBPort = strconv.Itoa(port)

	if req.DBTimezone == "" {
		req.DBTimezone = defaultTimezone
	}
	if _, err := time.LoadLocation(req.DBTimezone); err != nil {
		return req, validationError("db_timezone", "unknown timezone")
	}

	return req, nil
}

func validateSite(req *InstallRequest) error {
	req.AdminUser = strings.TrimSpace(req.AdminUser)
	req.AdminEmail = strings.TrimSpace(req.AdminEmail)
	req.SiteTitle = strings.TrimSpace(req.SiteTitle)

	switch {
	case req.AdminUser == "":
		return validationError("admin_user", "administrator username is required")
	case utf8.RuneCountInString(req.AdminUser) > 50:
		return validationError("admin_user", "administrator username is too long")
	case req.SiteTitle == "":
		return validationError("site_title", "site title is required")
	case utf8.RuneCountInString(req.SiteTitle) > 200:
		return validationError("site_title", "site title is too long")
	}

	if req.AdminEmail == "" || len(req.AdminEmail) > 255 {
		return validationError("admin_email", "administrator email is invalid")
	}
	address, err := mail.ParseAddress(req.AdminEmail)
	if err != nil || address.Name != "" || address.Address != req.AdminEmail {
		return validationError("admin_email", "administrator email must be a plain address")
	}

	// The password is deliberately not trimmed: spaces are part of the secret.
	// TrimSpace decides only whether the secret is blank, without altering the
	// value that is hashed.
	if strings.TrimSpace(req.AdminPass) == "" {
		return validationError("admin_pass", "administrator password cannot be empty or only whitespace")
	}
	if len([]byte(req.AdminPass)) < 12 || len([]byte(req.AdminPass)) > 72 {
		return validationError("admin_pass", "administrator password must be between 12 and 72 bytes")
	}

	return nil
}

func databaseConfig(req InstallRequest) config.DatabaseConfig {
	return config.DatabaseConfig{
		Host:     req.DBHost,
		Port:     req.DBPort,
		User:     req.DBUser,
		Password: req.DBPass,
		Name:     req.DBName,
		Timezone: req.DBTimezone,
	}
}

func newInstallationID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func closeDatabase(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB == nil {
		return
	}
	_ = sqlDB.Close()
}

package install

import (
	"errors"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"golang.org/x/crypto/bcrypt"
)

// IsInstalled checks if the system is already installed.
// It checks if config.json exists and DB is connected.
func IsInstalled() bool {
	if config.GlobalConfig == nil {
		return false
	}
	if data.DB == nil {
		return false
	}
	var count int64
	data.DB.Model(&core.User{}).Count(&count)
	return count > 0
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

// TestConnection tests the database connection with provided details.
func TestConnection(req InstallRequest) error {
	cfg := config.DatabaseConfig{
		Host:     req.DBHost,
		Port:     req.DBPort,
		User:     req.DBUser,
		Password: req.DBPass,
		Name:     req.DBName,
		Timezone: req.DBTimezone,
	}
	return data.Connect(cfg)
}

// PerformInstallation executes the installation process.
func PerformInstallation(req InstallRequest) error {
	if IsInstalled() {
		return errors.New("already installed")
	}

	// 1. Create Config
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Host:     req.DBHost,
			Port:     req.DBPort,
			User:     req.DBUser,
			Password: req.DBPass,
			Name:     req.DBName,
			Timezone: req.DBTimezone,
		},
	}

	// 2. Save Config
	if err := config.Save(cfg); err != nil {
		return err
	}
	config.GlobalConfig = cfg

	// 3. Connect DB
	if err := data.Connect(cfg.Database); err != nil {
		return err
	}

	// 4. Migrate
	if err := data.Migrate(); err != nil {
		return err
	}

	// 5. Save Site Title
	data.DB.Create(&core.Option{Key: "site_title", Value: req.SiteTitle})

	// 6. Create Admin User
	hashedPass, err := bcrypt.GenerateFromPassword([]byte(req.AdminPass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	admin := core.User{
		Username: req.AdminUser,
		Email:    req.AdminEmail,
		Password: string(hashedPass),
		Role:     "admin",
		Provider: "local",
	}

	if err := data.DB.Create(&admin).Error; err != nil {
		return err
	}

	return nil
}

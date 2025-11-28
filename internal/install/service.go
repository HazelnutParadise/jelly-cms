package install

import (
	"errors"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"golang.org/x/crypto/bcrypt"
)

// IsInstalled checks if the system is already installed.
// For now, we consider it installed if there is at least one user in the database.
func IsInstalled() bool {
	if data.DB == nil {
		return false
	}
	var count int64
	data.DB.Model(&core.User{}).Count(&count)
	return count > 0
}

// InstallRequest defines the data needed for installation.
type InstallRequest struct {
	SiteTitle  string `json:"site_title" form:"site_title"`
	AdminUser  string `json:"admin_user" form:"admin_user"`
	AdminEmail string `json:"admin_email" form:"admin_email"`
	AdminPass  string `json:"admin_pass" form:"admin_pass"`
}

// PerformInstallation executes the installation process.
func PerformInstallation(req InstallRequest) error {
	if IsInstalled() {
		return errors.New("already installed")
	}

	// 1. Save Site Title
	data.DB.Create(&core.Option{Key: "site_title", Value: req.SiteTitle})

	// 2. Create Admin User
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

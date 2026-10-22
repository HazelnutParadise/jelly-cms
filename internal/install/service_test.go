package install

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/data"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const configFile = "data/config.json"

// validRequest is an installation request that passes every input rule, so a
// failure can only come from the database.
func validRequest() InstallRequest {
	return InstallRequest{
		DBHost:     "127.0.0.1",
		DBPort:     "1",
		DBUser:     "jelly",
		DBPass:     "s3cret",
		DBName:     "jelly",
		DBTimezone: "UTC",

		SiteTitle:  "Jelly Site",
		AdminUser:  "admin",
		AdminEmail: "admin@example.com",
		AdminPass:  "Str0ng-Passw0rd!",
	}
}

// isolateGlobals detaches the installation from the running site and returns
// the database handle that is in use. The handle is never connected, so no
// test can reach a real database.
func isolateGlobals(t *testing.T) *gorm.DB {
	t.Helper()

	previousConfig := config.GlobalConfig
	previousDB := data.DB

	db, err := gorm.Open(
		postgres.Open("host=127.0.0.1 user=jelly password=s3cret dbname=jelly port=1 sslmode=disable"),
		&gorm.Config{DisableAutomaticPing: true},
	)
	if err != nil {
		t.Fatalf("build the idle database handle: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get the sql.DB behind the idle handle: %v", err)
	}

	// An empty GlobalConfig makes IsInstalled report "not installed" without
	// querying the database.
	config.GlobalConfig = nil
	data.DB = db

	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		data.DB = previousDB
		_ = sqlDB.Close()
	})

	return db
}

func TestTestConnectionFailureKeepsTheDatabaseHandle(t *testing.T) {
	existing := isolateGlobals(t)

	err := TestConnection(validRequest())
	if err == nil {
		t.Fatalf("TestConnection() = nil, want an error for an unreachable database")
	}
	if !strings.Contains(err.Error(), "failed to connect to database") {
		t.Errorf("TestConnection() error = %v, want it to report the database connection failure", err)
	}
	if data.DB != existing {
		t.Errorf("TestConnection() replaced the database handle that is in use: data.DB = %p, want the previous handle %p", data.DB, existing)
	}
}

func TestPerformInstallationRejectsInvalidInputBeforeAnythingIsWritten(t *testing.T) {
	cases := []struct {
		name      string
		wantField string
		mutate    func(*InstallRequest)
	}{
		{
			name:   "empty admin username",
			mutate: func(r *InstallRequest) { r.AdminUser = "" },
		},
		{
			name:   "blank admin username",
			mutate: func(r *InstallRequest) { r.AdminUser = "   " },
		},
		{
			name:   "invalid admin email",
			mutate: func(r *InstallRequest) { r.AdminEmail = "not-an-email" },
		},
		{
			name:   "blank admin email",
			mutate: func(r *InstallRequest) { r.AdminEmail = "   " },
		},
		{
			name:   "empty site title",
			mutate: func(r *InstallRequest) { r.SiteTitle = "" },
		},
		{
			name:   "blank site title",
			mutate: func(r *InstallRequest) { r.SiteTitle = "  \t " },
		},
		{
			name:   "password shorter than 12 characters",
			mutate: func(r *InstallRequest) { r.AdminPass = "Short1!" },
		},
		{
			name:   "password longer than the bcrypt limit",
			mutate: func(r *InstallRequest) { r.AdminPass = strings.Repeat("a", 73) },
		},
		{
			name:      "password made only of ASCII spaces",
			wantField: "admin_pass",
			mutate:    func(r *InstallRequest) { r.AdminPass = strings.Repeat(" ", 12) },
		},
		{
			name:      "password made only of ASCII whitespace characters",
			wantField: "admin_pass",
			mutate:    func(r *InstallRequest) { r.AdminPass = "\t\n\v\f\r \t\n\v\f\r " },
		},
		{
			name:      "password made only of non-breaking spaces",
			wantField: "admin_pass",
			mutate:    func(r *InstallRequest) { r.AdminPass = strings.Repeat("\u00a0", 6) },
		},
		{
			name:      "password made only of ideographic spaces",
			wantField: "admin_pass",
			mutate:    func(r *InstallRequest) { r.AdminPass = strings.Repeat("\u3000", 4) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			existing := isolateGlobals(t)

			req := validRequest()
			tc.mutate(&req)

			err := PerformInstallation(req)
			if err == nil {
				t.Fatalf("PerformInstallation() = nil, want an error for %s", tc.name)
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Errorf("PerformInstallation() error = %v, want a ValidationError for %s", err, tc.name)
			} else if tc.wantField != "" && validationErr.Field != tc.wantField {
				t.Errorf("PerformInstallation() rejected field %q, want %q for %s", validationErr.Field, tc.wantField, tc.name)
			}
			if strings.Contains(err.Error(), "failed to connect to database") {
				t.Errorf("PerformInstallation() failed on the database connection (%v); %s must be rejected before the database is touched", err, tc.name)
			}
			if strings.Contains(err.Error(), "already installed") {
				t.Errorf("PerformInstallation() error = %v, want the error that reports %s", err, tc.name)
			}

			if _, statErr := os.Stat(configFile); !os.IsNotExist(statErr) {
				t.Errorf("PerformInstallation() wrote %s for %s, want no config file before the input is accepted", configFile, tc.name)
			}
			if config.GlobalConfig != nil {
				t.Errorf("PerformInstallation() changed config.GlobalConfig to %+v for %s, want it untouched", config.GlobalConfig, tc.name)
			}
			if data.DB != existing {
				t.Errorf("PerformInstallation() replaced the database handle that is in use for %s: data.DB = %p, want the previous handle %p", tc.name, data.DB, existing)
			}
		})
	}
}

func TestPerformInstallationWithValidInputLeavesNoHalfInstallWhenTheDatabaseIsUnreachable(t *testing.T) {
	cases := []struct {
		name     string
		password string
	}{
		{name: "ordinary password", password: "Str0ng-Passw0rd!"},
		{name: "password at the bcrypt length limit", password: strings.Repeat("a", 72)},
		{name: "password with leading and trailing spaces", password: "  Str0ng-Passw0rd!  "},
		{name: "password with leading and trailing tabs", password: "\t Str0ng-Passw0rd! \t"},
		{name: "password with leading and trailing ideographic spaces", password: "\u3000Str0ng-Passw0rd!\u3000"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			existing := isolateGlobals(t)

			req := validRequest()
			req.AdminPass = tc.password

			err := PerformInstallation(req)
			if err == nil {
				t.Fatalf("PerformInstallation() = nil, want the database connection error")
			}
			if !strings.Contains(err.Error(), "failed to connect to database") {
				t.Errorf("PerformInstallation() error = %v, want it to reach the database connection stage", err)
			}

			if _, statErr := os.Stat(configFile); !os.IsNotExist(statErr) {
				t.Errorf("PerformInstallation() wrote %s although the database is unreachable, want no config after a failed install", configFile)
			}
			if config.GlobalConfig != nil {
				t.Errorf("PerformInstallation() changed config.GlobalConfig to %+v after the database failed, want it untouched", config.GlobalConfig)
			}
			if data.DB != existing {
				t.Errorf("PerformInstallation() replaced the database handle that is in use after the database failed: data.DB = %p, want the previous handle %p", data.DB, existing)
			}
		})
	}
}

func TestIsInstalledWithoutConfigIsFalse(t *testing.T) {
	isolateGlobals(t)

	if IsInstalled() {
		t.Error("IsInstalled() = true, want false when no config is loaded")
	}
}

// TestIsInstalledWithSavedConfigAndDownDatabaseIsTrue covers the state after a
// restart while the database is down: main.go loads data/config.json into
// config.GlobalConfig and the connection fails, so data.DB is nil. The site
// was installed, so IsInstalled must stay true; otherwise the installation
// would be reopened and could reset the site.
func TestIsInstalledWithSavedConfigAndDownDatabaseIsTrue(t *testing.T) {
	t.Chdir(t.TempDir())

	previousConfig := config.GlobalConfig
	previousDB := data.DB
	t.Cleanup(func() {
		config.GlobalConfig = previousConfig
		data.DB = previousDB
	})

	installed := &config.Config{Database: config.DatabaseConfig{
		Host:     "127.0.0.1",
		Port:     "1",
		User:     "jelly",
		Password: "s3cret",
		Name:     "jelly",
		Timezone: "UTC",
	}}
	if err := config.Save(installed); err != nil {
		t.Fatalf("save the config of the installed site: %v", err)
	}
	if _, err := config.Load(); err != nil {
		t.Fatalf("load the config of the installed site: %v", err)
	}

	data.DB = nil

	if !IsInstalled() {
		t.Error("IsInstalled() = false although the site has a saved config and the database is down, want true so that a database outage cannot reopen installation")
	}
}

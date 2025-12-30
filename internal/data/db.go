package data

import (
	"fmt"
	"log"

	"github.com/HazelnutParadise/jelly-cms/internal/config"
	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DB is the global database instance.
var DB *gorm.DB

// Connect initializes the database connection using the provided config.
func Connect(cfg config.DatabaseConfig) error {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=%s",
		cfg.Host, cfg.User, cfg.Password, cfg.Name, cfg.Port, cfg.Timezone)

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	log.Println("Database connected successfully")
	return nil
}

// Migrate runs auto-migration for core models.
func Migrate() error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}

	return DB.AutoMigrate(
		&core.User{},
		&core.Post{},
		&core.Option{},
		&core.Product{},
		&core.Order{},
		&core.OrderItem{},
		&core.ThemeSettings{},
	)
}

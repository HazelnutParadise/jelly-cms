package data

import (
	"fmt"
	"log"

	"github.com/HazelnutParadise/jelly-cms/internal/core"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// DB is the global database instance.
var DB *gorm.DB

// Connect initializes the database connection.
func Connect(host, user, password, dbName, port string) error {
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=Asia/Shanghai",
		host, user, password, dbName, port)

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
	)
}

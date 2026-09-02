package database

import (
	"fmt"
	"log"
	"os"

	"order-service/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func InitDB() {
	dsn := os.Getenv("DB_URL")
	if dsn == "" {
		log.Fatal("DB_URL environment variable is not set")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}

	// Auto-migrate the schema
	err = db.AutoMigrate(&models.Order{})
	if err != nil {
		log.Fatal("Failed to migrate database:", err)
	}

	DB = db
	fmt.Println("Successfully connected to PostgreSQL")
}

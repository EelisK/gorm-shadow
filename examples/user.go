package main

import (
	"context"
	"time"

	gormshadow "github.com/EelisK/gorm-shadow"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type User struct {
	ID        uint   `gorm:"primaryKey"`
	Name      string `gorm:"column:name"`
	Email     string `gorm:"column:email"`
	DeletedAt gorm.DeletedAt
}

func (User) ShadowTable() string {
	return "shadow_users"
}

type TimeMachine struct {
	Time time.Time
}

func (t *TimeMachine) GetTime(_ context.Context) (time.Time, error) {
	return t.Time, nil
}

func main() {
	// Run this DB with docker:
	// docker run --rm -P -p 127.0.0.1:5432:5432 -e POSTGRES_HOST_AUTH_METHOD="trust" postgres:alpine
	dsn := "host=localhost user=postgres"
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn}))
	if err != nil {
		panic(err)
	}

	// Migrate the User model and its shadow model
	if err := db.AutoMigrate(&User{}, &gormshadow.Model[User]{}); err != nil {
		panic(err)
	}

	// Initialize our mock time machine with a zero time
	timeMachine := &TimeMachine{}

	if err := db.Use(&gormshadow.Plugin{TimeMachine: timeMachine}); err != nil {
		panic(err)
	}

	// Create a user
	user := User{Name: "John Doe", Email: "john.doe@example.com"}
	if err := db.Create(&user).Error; err != nil {
		panic(err)
	}
	userID := user.ID
	createdBefore := time.Now()

	// Update the user
	user.Name = "Jane Doe"
	if err := db.Save(&user).Error; err != nil {
		panic(err)
	}
	updatedBefore := time.Now()

	// Query the user from the shadow table
	historicalUser := User{}
	timeMachine.Time = createdBefore
	if err := db.Model(&historicalUser).Where("id = ?", userID).Find(&historicalUser).Error; err != nil {
		panic(err)
	}

	if historicalUser.Name != "John Doe" {
		panic("expected John Doe")
	}
	println("Found user: ", historicalUser.Name)

	timeMachine.Time = updatedBefore
	if err := db.Model(&historicalUser).Where("id = ?", userID).Find(&historicalUser).Error; err != nil {
		panic(err)
	}
	println("Found user: ", historicalUser.Name)

	if historicalUser.Name != "Jane Doe" {
		panic("expected Jane Doe")
	}
	println("Found user: ", historicalUser.Name)
}

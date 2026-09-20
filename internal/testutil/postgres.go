// Package testutil provides disposable infrastructure for lobby package tests.
package testutil

import (
	"context"
	"testing"
	"time"

	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/testcontainers/testcontainers-go"
	containerpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// NewPostgresFixture starts an isolated PostgreSQL database, creates only the
// generic lobby schema, and registers both pool and container cleanup.
func NewPostgresFixture(t *testing.T) *gorm.DB {
	t.Helper()
	ctx := context.Background()

	container, err := containerpostgres.Run(ctx, "postgres:18-alpine",
		containerpostgres.WithDatabase("lobby"),
		containerpostgres.WithUsername("lobby"),
		containerpostgres.WithPassword("lobby"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start disposable lobby PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("terminate disposable lobby PostgreSQL: %v", err)
		}
	})

	connectionString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get disposable lobby PostgreSQL connection string: %v", err)
	}
	db, err := gorm.Open(postgres.Open(connectionString), &gorm.Config{})
	if err != nil {
		t.Fatalf("open disposable lobby PostgreSQL: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get disposable lobby PostgreSQL pool: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close disposable lobby PostgreSQL pool: %v", err)
		}
	})

	if err := lobbypostgres.InitializeSchema(ctx, db); err != nil {
		t.Fatalf("initialize disposable lobby PostgreSQL schema: %v", err)
	}
	return db
}

package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/centrifugal/gocent/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// SlowQueryThreshold is how long a query may take before it is logged as slow.
const SlowQueryThreshold = 200 * time.Millisecond

// ConnectTimeout bounds OpenDatabase's first connection, well under Lambda's
// 10-second limit on a cold start's init.
const ConnectTimeout = 5 * time.Second

// OpenDatabase connects to the lobby's database with a bounded pool. It runs
// no schema changes. Failed and slow queries are logged through logger as
// warnings, with placeholders instead of their values.
func OpenDatabase(config DatabaseConfig, logger *slog.Logger) (*gorm.DB, error) {
	queryLogger := gormlogger.NewSlogLogger(logger, gormlogger.Config{
		SlowThreshold:             SlowQueryThreshold,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
	})
	// GORM's own ping has no timeout.
	database, err := gorm.Open(postgres.Open(config.URL), &gorm.Config{Logger: queryLogger, DisableAutomaticPing: true})
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	pool, err := database.DB()
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	pool.SetMaxOpenConns(config.MaxOpenConnections)
	pool.SetMaxIdleConns(config.MaxIdleConnections)
	pool.SetConnMaxIdleTime(config.ConnectionMaxIdleTime)
	ctx, cancel := context.WithTimeout(context.Background(), ConnectTimeout)
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return database, nil
}

// NewCentrifugoClient is the client of Centrifugo's HTTP API that config
// names.
func NewCentrifugoClient(config Config) *gocent.Client {
	return gocent.New(gocent.Config{Addr: config.CentrifugoAPIURL, Key: config.CentrifugoAPIKey})
}

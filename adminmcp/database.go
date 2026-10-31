// Package adminmcp answers an operator's questions about lobbies and players
// through Model Context Protocol tools. It reads only the lobby_* tables,
// through the GORM records in lobby/postgres, so any game that uses the lobby
// can register the tools on its own admin server and add its own beside them.
// Nothing in it writes: every tool runs in a read-only transaction on a
// connection whose sessions are read-only too.
package adminmcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// StatementTimeout bounds every statement the tools run, including the
// statements an operator writes.
const StatementTimeout = 5 * time.Second

// ToolTimeout bounds one tool call, which may run several statements. It
// backs up the statement timeout from the client side.
const ToolTimeout = 30 * time.Second

// connectTimeout bounds OpenDatabase's first connection.
const connectTimeout = 10 * time.Second

// applicationName names the server's connections in pg_stat_activity, unless
// the URL names them itself.
const applicationName = "admin-mcp"

// OpenDatabase connects to url with a single connection, so the server never
// holds more than one of the database's connection slots. Every session it
// opens starts read-only, with default_transaction_read_only on and a
// statement_timeout of StatementTimeout, whatever the role may do. GORM logs
// failed and slow queries through logger, with placeholders instead of
// values; logger must not write to stdout, which carries the MCP stream.
//
// Statements always use PostgreSQL's extended protocol, which runs exactly
// one statement per query, even when url asks for the simple protocol.
func OpenDatabase(ctx context.Context, url string, logger *slog.Logger) (*gorm.DB, error) {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse the database URL: %w", err)
	}
	config.RuntimeParams["default_transaction_read_only"] = "on"
	config.RuntimeParams["statement_timeout"] = strconv.FormatInt(StatementTimeout.Milliseconds(), 10)
	if config.RuntimeParams["application_name"] == "" {
		config.RuntimeParams["application_name"] = applicationName
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	sqlDatabase := stdlib.OpenDB(*config)
	sqlDatabase.SetMaxOpenConns(1)
	sqlDatabase.SetMaxIdleConns(1)
	// A NAT or firewall may drop a connection that idles for long, so an idle
	// one is replaced rather than found broken.
	sqlDatabase.SetConnMaxIdleTime(5 * time.Minute)
	pingContext, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := sqlDatabase.PingContext(pingContext); err != nil {
		_ = sqlDatabase.Close()
		return nil, fmt.Errorf("connect to the database: %w", err)
	}
	queryLogger := gormlogger.NewSlogLogger(logger, gormlogger.Config{
		SlowThreshold:             time.Second,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
	})
	database, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDatabase}), &gorm.Config{Logger: queryLogger, DisableAutomaticPing: true})
	if err != nil {
		_ = sqlDatabase.Close()
		return nil, fmt.Errorf("connect to the database: %w", err)
	}
	return database, nil
}

// ReadOnly runs read in a read-only transaction within ToolTimeout. The
// transaction is always rolled back, never committed, so a SET that a
// statement ran ends with it and the next tool call starts from the
// session's read-only defaults again.
func ReadOnly(ctx context.Context, database *gorm.DB, read func(transaction *gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(ctx, ToolTimeout)
	defer cancel()
	transaction := database.WithContext(ctx).Begin(&sql.TxOptions{ReadOnly: true})
	if err := transaction.Error; err != nil {
		return fmt.Errorf("begin a read-only transaction: %w", err)
	}
	defer transaction.Rollback()
	return read(transaction)
}

// SQLTransaction returns the database/sql transaction behind a transaction
// that ReadOnly began, for a statement that GORM must not rewrite, such as
// one an operator wrote. Run such a statement only in a form that can't be a
// command, such as a cursor's query: SET TRANSACTION READ WRITE as the
// transaction's first statement would make it read-write.
func SQLTransaction(transaction *gorm.DB) (*sql.Tx, error) {
	sqlTransaction, ok := transaction.Statement.ConnPool.(*sql.Tx)
	if !ok {
		return nil, errors.New("not inside a database transaction")
	}
	return sqlTransaction, nil
}

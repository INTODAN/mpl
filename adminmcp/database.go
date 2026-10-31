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
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
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
//
// A superuser, such as a local development role, can do much that a
// read-only transaction doesn't stop, such as reading server files or ending
// other sessions. Its sessions therefore SET ROLE pg_read_all_data, which
// reads every table and nothing more, and logger warns once. That is
// best-effort: a statement may switch the role back with set_config for its
// own duration. Only a role that is not a superuser, such as the production
// read-only role, makes the server fully read-only.
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
	var superuserWarning sync.Once
	sqlDatabase := stdlib.OpenDB(*config, stdlib.OptionAfterConnect(func(ctx context.Context, connection *pgx.Conn) error {
		var superuser bool
		if err := connection.QueryRow(ctx, "SELECT rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&superuser); err != nil {
			return fmt.Errorf("read the role's attributes: %w", err)
		}
		if !superuser {
			return nil
		}
		superuserWarning.Do(func() {
			logger.Warn("The database role is a superuser, so the admin server reads as pg_read_all_data instead. " +
				"That is best-effort: use a role that is not a superuser, such as the production read-only role, to be sure nothing changes")
		})
		if _, err := connection.Exec(ctx, "SET ROLE pg_read_all_data"); err != nil {
			return fmt.Errorf("give up the superuser's privileges: %w", err)
		}
		return nil
	}))
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
//
// Session-level advisory locks outlive the rollback, and the server keeps
// its one connection, so a pg_advisory_lock that a statement took would be
// held until the server exits, blocking whoever waits for the same lock,
// such as the migrations. ReadOnly therefore holds the connection for the
// whole call and releases every advisory lock on it afterwards. A connection
// where that fails is closed, which releases them too.
func ReadOnly(ctx context.Context, database *gorm.DB, read func(transaction *gorm.DB) error) error {
	ctx, cancel := context.WithTimeout(ctx, ToolTimeout)
	defer cancel()
	sqlDatabase, err := database.DB()
	if err != nil {
		return err
	}
	connection, err := sqlDatabase.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect to the database: %w", err)
	}
	defer releaseAdvisoryLocks(connection)
	sqlTransaction, err := connection.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin a read-only transaction: %w", err)
	}
	defer func() { _ = sqlTransaction.Rollback() }()
	// What GORM's Begin does, on the connection held.
	transaction := database.Session(&gorm.Session{Context: ctx, NewDB: true})
	transaction.Statement.ConnPool = sqlTransaction
	return read(transaction)
}

// releaseAdvisoryLocks releases the session-level advisory locks that a
// statement took on connection, and returns connection to the pool. It runs
// after the rollback, also when the call's context has ended.
func releaseAdvisoryLocks(connection *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), StatementTimeout)
	defer cancel()
	if _, err := connection.ExecContext(ctx, "SELECT pg_advisory_unlock_all()"); err != nil {
		// The pool closes a connection that reports itself bad.
		_ = connection.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = connection.Close()
}

// ErrCredentialsReadable refuses operator SQL on a role that can read the
// sessions' bearer hashes although it should not.
var ErrCredentialsReadable = errors.New("the database role can read lobby_sessions.bearer_hash although it is neither a superuser " +
	"nor the table's owner, so its grants are out of date, for example after a migration recreated the table; " +
	"operator SQL is refused until the role's grants are applied again")

// ErrCredentialsColumnChanged refuses operator SQL when lobby_sessions has
// no bearer_hash column any more, so the column grants that hide its
// credentials may no longer match it.
var ErrCredentialsColumnChanged = errors.New("lobby_sessions has no bearer_hash column any more, so the read-only role's column grants " +
	"may no longer hide its credentials; operator SQL is refused until the grants are updated for the new columns and applied again")

// credentialsState says whether the session's role can read the sessions'
// bearer hashes. It is "readable" when the role may read bearer_hash without
// being a superuser or the owner of lobby_sessions: a read-only role whose
// grants a migration has outrun. The superuser's pg_read_all_data and the
// owner may read it anyway. It is "changed" when the column is gone, and
// "absent" before the table exists. CASE checks the column before
// has_column_privilege, which fails on a missing one.
const credentialsState = `SELECT CASE
	WHEN to_regclass('public.lobby_sessions') IS NULL THEN 'absent'
	WHEN NOT EXISTS (SELECT FROM pg_attribute
		WHERE attrelid = to_regclass('public.lobby_sessions') AND attname = 'bearer_hash' AND NOT attisdropped) THEN 'changed'
	WHEN has_column_privilege(to_regclass('public.lobby_sessions'), 'bearer_hash', 'SELECT')
		AND NOT (SELECT rolsuper FROM pg_roles WHERE rolname = session_user)
		AND NOT pg_has_role(session_user, (SELECT relowner FROM pg_class WHERE oid = to_regclass('public.lobby_sessions')), 'USAGE')
		THEN 'readable'
	ELSE 'hidden'
END`

// CheckCredentialsHidden returns ErrCredentialsReadable when a role that
// should not read bearer hashes can, such as a read-only role whose grants a
// migration has outrun, and ErrCredentialsColumnChanged when bearer_hash was
// renamed or dropped, for every role, so a change to the table is noticed.
// Run it before any SQL an operator writes. It reads catalogs only, so it
// runs as plain SQL.
func CheckCredentialsHidden(transaction *gorm.DB) error {
	var state string
	if err := transaction.Raw(credentialsState).Scan(&state).Error; err != nil {
		return fmt.Errorf("check the role's grants: %w", err)
	}
	switch state {
	case "readable":
		return ErrCredentialsReadable
	case "changed":
		return ErrCredentialsColumnChanged
	}
	return nil
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

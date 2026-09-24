package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// BindOnce runs game-specific lookup and creation in one transaction. Load must
// return nil only when no game exists, and reject incomplete persisted bindings.
// Create must use the supplied transaction for all writes. Both callbacks must
// be free of external side effects. The database carries the request context.
//
// A concurrent creation is replayed only for the named unique lobby-link
// constraint. Unrelated database errors are returned without retrying.
func BindOnce[Result any](database *gorm.DB, constraint string, load func(*gorm.DB) (*Result, error), create func(*gorm.DB) (Result, error)) (Result, error) {
	var result Result
	err := database.Transaction(func(transaction *gorm.DB) error {
		existing, err := load(transaction)
		if err != nil {
			return err
		}
		if existing != nil {
			result = *existing
			return nil
		}
		result, err = create(transaction)
		return err
	})
	if err == nil {
		return result, nil
	}
	var zero Result
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23505" || postgresError.ConstraintName != constraint {
		return zero, err
	}
	// The competing transaction has committed by the time its unique conflict
	// is reported. Load still validates that all of its game state is present.
	existing, lookupErr := load(database)
	if lookupErr != nil {
		return zero, lookupErr
	}
	if existing == nil {
		return zero, err
	}
	return *existing, nil
}

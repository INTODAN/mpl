package postgres

import (
	"context"
	"errors"
	"fmt"

	"islandBattles/lobby/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RetentionStore deletes lobby data past its retention period. It touches
// only lobby tables.
type RetentionStore struct {
	db *gorm.DB
}

var _ domain.RetentionStore = (*RetentionStore)(nil)

func NewRetentionStore(db *gorm.DB) (*RetentionStore, error) {
	if db == nil {
		return nil, errors.New("lobby postgres: nil database")
	}
	return &RetentionStore{db: db}, nil
}

// skipLocked leaves rows that a request is changing to a later pass, so a
// pass never waits on a lobby in use and two passes never wait on each other.
var skipLocked = clause.Locking{Strength: clause.LockingStrengthUpdate, Options: clause.LockingOptionsSkipLocked}

// DeleteExpired deletes create requests, then sessions, then lobbies, then
// the players that nothing references any more, so a player freed by an
// earlier step goes in the same pass. Each step is one transaction that locks
// and deletes at most cutoffs.BatchSize rows of its kind. The steps are
// independent: a step that fails is reported in the joined error, and the
// later steps still run, so a row that can't be deleted never blocks the rest.
func (store *RetentionStore) DeleteExpired(ctx context.Context, cutoffs domain.RetentionCutoffs) (domain.RetentionResult, error) {
	if cutoffs.BatchSize < 1 || cutoffs.BatchSize > domain.MaxRetentionBatchSize {
		return domain.RetentionResult{}, domain.ErrInvalidInput
	}
	database := store.db.WithContext(ctx)
	var result domain.RetentionResult
	var failures []error
	for _, step := range []struct {
		name    string
		deleted *int
		run     func(*gorm.DB, domain.RetentionCutoffs) (int, error)
	}{
		{"create requests", &result.CreateRequests, deleteCreateRequests},
		{"sessions", &result.Sessions, deleteSessions},
		{"lobbies", &result.Lobbies, deleteLobbies},
		{"players", &result.Players, deletePlayers},
	} {
		deleted, err := step.run(database, cutoffs)
		if err != nil {
			failures = append(failures, fmt.Errorf("delete expired %s: %w", step.name, err))
			continue
		}
		*step.deleted = deleted
	}
	// The error stays unwrapped, so the log names each failing step and why.
	return result, errors.Join(failures...)
}

func deleteCreateRequests(database *gorm.DB, cutoffs domain.RetentionCutoffs) (int, error) {
	return deleteBatch[CreateRequestRecord](database, cutoffs.BatchSize, []string{"player_id", "request_id"},
		clause.Lt{Column: clause.Column{Name: "created_at"}, Value: cutoffs.CreateRequestsBefore}, nil)
}

func deleteSessions(database *gorm.DB, cutoffs domain.RetentionCutoffs) (int, error) {
	return deleteBatch[SessionRecord](database, cutoffs.BatchSize, []string{"id"},
		clause.Lt{Column: clause.Column{Name: "expires_at"}, Value: cutoffs.SessionsExpiredBefore}, nil)
}

// deleteLobbies deletes rooms with their leases, start roster, memberships,
// watchers and teams, children first for the foreign keys, and the create requests
// that made them, so a later retry creates a new lobby instead of replaying
// a deleted one.
func deleteLobbies(database *gorm.DB, cutoffs domain.RetentionCutoffs) (int, error) {
	return deleteBatch[RoomRecord](database, cutoffs.BatchSize, []string{"id"}, lobbiesPastRetention(cutoffs), func(transaction *gorm.DB, rooms []RoomRecord) error {
		ids := make([]any, len(rooms))
		for index, room := range rooms {
			ids[index] = room.ID
		}
		inRooms := clause.IN{Column: clause.Column{Name: "lobby_id"}, Values: ids}
		for _, child := range []any{&LeaseRecord{}, &StartRosterRecord{}, &MembershipRecord{}, &WatcherRecord{}, &TeamRecord{}, &CreateRequestRecord{}} {
			if err := transaction.Where(inRooms).Delete(child).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// lobbiesPastRetention matches each registered game's rooms against that
// game's cutoffs, and every other room against the defaults.
func lobbiesPastRetention(cutoffs domain.RetentionCutoffs) clause.Expression {
	gameKey := clause.Column{Name: "game_key"}
	pastCutoffs := func(lobby domain.LobbyCutoffs) clause.Expression {
		return clause.Or(
			clause.Lt{Column: clause.Column{Name: "last_connected_at"}, Value: lobby.IdleBefore},
			clause.Lt{Column: clause.Column{Name: "created_at"}, Value: lobby.CreatedBefore},
		)
	}
	matches := make([]clause.Expression, 0, len(cutoffs.Games)+1)
	registered := make([]any, 0, len(cutoffs.Games))
	for _, game := range cutoffs.Games {
		matches = append(matches, clause.And(clause.Eq{Column: gameKey, Value: game.GameKey}, pastCutoffs(game)))
		registered = append(registered, game.GameKey)
	}
	if len(registered) == 0 {
		return clause.Or(append(matches, pastCutoffs(cutoffs.OtherGames))...)
	}
	matches = append(matches, clause.And(clause.Not(clause.IN{Column: gameKey, Values: registered}), pastCutoffs(cutoffs.OtherGames)))
	return clause.Or(matches...)
}

// deletePlayers deletes players that no session, room host, membership,
// watcher, start roster or create request references. Players hold no other
// lobby data.
func deletePlayers(database *gorm.DB, cutoffs domain.RetentionCutoffs) (int, error) {
	player := clause.Column{Table: PlayerRecord{}.TableName(), Name: "id"}
	unreferencedBy := func(table, column string) clause.Expression {
		references := database.Session(&gorm.Session{NewDB: true}).Table(table).Select("1").
			Where(clause.Eq{Column: clause.Column{Table: table, Name: column}, Value: player})
		return clause.Expr{SQL: "NOT EXISTS (?)", Vars: []any{references}}
	}
	return deleteBatch[PlayerRecord](database, cutoffs.BatchSize, []string{"id"}, clause.And(
		clause.Lt{Column: clause.Column{Table: PlayerRecord{}.TableName(), Name: "created_at"}, Value: cutoffs.PlayersCreatedBefore},
		unreferencedBy(SessionRecord{}.TableName(), "player_id"),
		unreferencedBy(RoomRecord{}.TableName(), "host_player_id"),
		unreferencedBy(MembershipRecord{}.TableName(), "player_id"),
		unreferencedBy(WatcherRecord{}.TableName(), "player_id"),
		unreferencedBy(StartRosterRecord{}.TableName(), "player_id"),
		unreferencedBy(CreateRequestRecord{}.TableName(), "player_id"),
	), nil)
}

// deleteBatch locks up to limit rows that match condition, skipping rows
// another transaction holds, runs before on them, and deletes them by their
// primary key columns, all in one transaction.
func deleteBatch[Record any](database *gorm.DB, limit int, primaryKey []string, condition clause.Expression, before func(*gorm.DB, []Record) error) (int, error) {
	deleted := 0
	err := database.Transaction(func(transaction *gorm.DB) error {
		var records []Record
		if err := transaction.Select(primaryKey).Where(condition).Limit(limit).Clauses(skipLocked).Find(&records).Error; err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}
		if before != nil {
			if err := before(transaction, records); err != nil {
				return err
			}
		}
		result := transaction.Delete(&records)
		deleted = int(result.RowsAffected)
		return result.Error
	})
	return deleted, err
}

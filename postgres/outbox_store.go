package postgres

import (
	"context"
	"time"
	"unicode/utf8"

	"islandBattles/lobby"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) Claim(ctx context.Context, request lobby.ClaimRequest) ([]lobby.DeliveryJob, error) {
	if request.Limit < 1 || request.Limit > 1000 || request.ClaimDuration <= 0 {
		return nil, lobby.NewError(lobby.CodeInvalidInput, "invalid delivery claim bounds")
	}
	jobs := make([]lobby.DeliveryJob, 0, request.Limit)
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var records []OutboxRecord
		query := tx.Where(clause.Eq{Column: "acknowledged_at", Value: nil}).Where(clause.Lte{Column: "next_attempt_at", Value: request.Now.UTC()}).Where(clause.Or(clause.Eq{Column: "claim_deadline", Value: nil}, clause.Lte{Column: "claim_deadline", Value: request.Now.UTC()})).Order(clause.OrderByColumn{Column: clause.Column{Name: "next_attempt_at"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Limit(request.Limit)
		if err := query.Find(&records).Error; err != nil {
			return err
		}
		for _, record := range records {
			token, err := store.identifiers.UUID()
			if err != nil {
				return err
			}
			deadline := request.Now.UTC().Add(request.ClaimDuration)
			record.ClaimToken = &token
			record.ClaimDeadline = &deadline
			record.Attempts++
			if err := tx.Model(&OutboxRecord{}).Where(&OutboxRecord{ID: record.ID}).Select("claim_token", "claim_deadline", "attempts").Updates(&record).Error; err != nil {
				return err
			}
			jobs = append(jobs, deliveryJob(record))
		}
		return nil
	})
	if err != nil {
		return nil, storageError(err)
	}
	return jobs, nil
}
func deliveryJob(record OutboxRecord) lobby.DeliveryJob {
	job := lobby.DeliveryJob{ID: record.ID, Kind: lobby.JobKind(record.Kind), Attempts: record.Attempts, NextAttemptAt: record.NextAttemptAt, LastError: record.LastError, CreatedAt: record.CreatedAt}
	if record.ClaimToken != nil {
		job.ClaimToken = *record.ClaimToken
	}
	if record.ClaimDeadline != nil {
		job.ClaimDeadline = *record.ClaimDeadline
	}
	if record.Kind == string(lobby.JobInvalidation) && record.LobbyID != nil && record.Version != nil {
		job.Invalidation = &lobby.Invalidation{SchemaVersion: lobby.InvalidationSchemaVersion, EventID: record.EventID, GameKey: record.GameKey, LobbyID: *record.LobbyID, Version: *record.Version, Type: lobby.InvalidationType}
	}
	if record.Kind == string(lobby.JobRevocation) && record.LobbyID != nil && record.PlayerID != nil && record.Generation != nil {
		job.Revocation = &lobby.Revocation{EventID: record.EventID, PlayerID: *record.PlayerID, Scope: lobby.Scope{GameKey: record.GameKey, LobbyID: *record.LobbyID}, Generation: *record.Generation}
	}
	return job
}
func (store *Store) ownedClaim(ctx context.Context, jobID, token string, now time.Time) *gorm.DB {
	return store.db.WithContext(ctx).Model(&OutboxRecord{}).Where(&OutboxRecord{ID: jobID, ClaimToken: &token}).Where(clause.Eq{Column: "acknowledged_at", Value: nil}).Where(clause.Gt{Column: "claim_deadline", Value: now.UTC()})
}
func (store *Store) Ack(ctx context.Context, jobID, token string, now time.Time) (bool, error) {
	if lobby.ValidateUUID(jobID) != nil || lobby.ValidateUUID(token) != nil {
		return false, lobby.ErrInvalidInput
	}
	acknowledged := now.UTC()
	result := store.ownedClaim(ctx, jobID, token, now).Select("acknowledged_at", "claim_token", "claim_deadline").Updates(&OutboxRecord{AcknowledgedAt: &acknowledged})
	return result.RowsAffected == 1, storageError(result.Error)
}
func (store *Store) Retry(ctx context.Context, request lobby.RetryRequest) (bool, error) {
	if lobby.ValidateUUID(request.JobID) != nil || lobby.ValidateUUID(request.ClaimToken) != nil || request.NextAttemptAt.Before(request.Now) || !utf8.ValidString(request.ErrorSummary) {
		return false, lobby.ErrInvalidInput
	}
	// Workers supply a safe diagnostic category. Bound persisted text so failed
	// dependencies cannot turn one retry record into an unbounded payload.
	summary := []rune(request.ErrorSummary)
	if len(summary) > 512 {
		summary = summary[:512]
	}
	record := OutboxRecord{NextAttemptAt: request.NextAttemptAt.UTC(), LastError: string(summary)}
	result := store.ownedClaim(ctx, request.JobID, request.ClaimToken, request.Now).Select("next_attempt_at", "last_error", "claim_token", "claim_deadline").Updates(&record)
	return result.RowsAffected == 1, storageError(result.Error)
}
func (store *Store) CurrentMembership(ctx context.Context, scope lobby.Scope, playerID string) (*lobby.MembershipState, error) {
	if err := validScope(scope); err != nil {
		return nil, err
	}
	if err := lobby.ValidateUUID(playerID); err != nil {
		return nil, err
	}
	var records []MembershipRecord
	roomIDs := store.db.Model(&RoomRecord{}).Select("id").Where(&RoomRecord{ID: scope.LobbyID, GameKey: scope.GameKey})
	err := store.db.WithContext(ctx).Where(&MembershipRecord{LobbyID: scope.LobbyID, PlayerID: playerID}).Where(clause.Expr{SQL: "lobby_id IN (?)", Vars: []any{roomIDs}}).Limit(1).Find(&records).Error
	if err != nil {
		return nil, storageError(err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	return &lobby.MembershipState{Generation: records[0].Generation, Active: records[0].Active}, nil
}

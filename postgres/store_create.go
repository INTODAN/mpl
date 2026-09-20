package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"

	"islandBattles/lobby"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func canonicalCreate(request lobby.CreateRequest) (lobby.CreateRequest, error) {
	if lobby.ValidateUUID(request.RequestID) != nil || lobby.ValidateUUID(request.ClientInstanceID) != nil || lobby.ValidateGameKey(request.GameKey) != nil {
		return request, lobby.ErrInvalidInput
	}
	var err error
	request.Name, err = lobby.NormalizeName(request.Name, 80)
	if err != nil {
		return request, err
	}
	request.Teams, err = lobby.ValidateTeamDefinitions(request.Teams, request.Capacity)
	return request, err
}
func (store *Store) Create(ctx context.Context, principal lobby.Principal, request lobby.CreateRequest, aggregate lobby.Aggregate) (lobby.Snapshot, error) {
	canonical, err := canonicalCreate(request)
	if err != nil {
		return lobby.Snapshot{}, err
	}
	if lobby.ValidateUUID(principal.PlayerID) != nil {
		return lobby.Snapshot{}, lobby.ErrUnauthenticated
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return lobby.Snapshot{}, storageError(err)
	}
	fingerprint := sha256.Sum256(payload)
	var response lobby.Snapshot
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Insert-first uniqueness serializes requests without a check/write gap.
		// The temporary response cannot escape this transaction; rollback removes it.
		record := CreateRequestRecord{PlayerID: principal.PlayerID, RequestID: request.RequestID, PayloadHash: fingerprint[:], ResponseJSON: datatypes.JSON(`{}`), CreatedAt: store.clock.Now().UTC()}
		inserted := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "player_id"}, {Name: "request_id"}}, DoNothing: true}).Create(&record)
		if inserted.Error != nil {
			return inserted.Error
		}
		if inserted.RowsAffected == 0 {
			var records []CreateRequestRecord
			if err := tx.Where(&CreateRequestRecord{PlayerID: principal.PlayerID, RequestID: request.RequestID}).Limit(1).Find(&records).Error; err != nil {
				return err
			}
			if len(records) != 1 {
				return lobby.ErrUnavailable
			}
			if !bytes.Equal(records[0].PayloadHash, fingerprint[:]) {
				return lobby.ErrIdempotencyConflict
			}
			return json.Unmarshal(records[0].ResponseJSON, &response)
		}
		aggregate = aggregate.DeepCopy()
		if aggregate.Lobby.Version != 1 || aggregate.Lobby.GameKey != canonical.GameKey {
			return lobby.ErrInvalidInput
		}
		room := roomRecord(aggregate.Lobby)
		if err := tx.Create(&room).Error; err != nil {
			if isCodeCollision(err) {
				return lobby.ErrJoinCodeCollision
			}
			return err
		}
		if err := persistChildren(tx, aggregate, true); err != nil {
			return err
		}
		if err := store.insertJobs(tx, aggregate.Lobby, lobby.Change{PublicChanged: true}); err != nil {
			return err
		}
		stored, err := loadAggregate(tx, aggregate.Lobby.Scope(), false)
		if err != nil {
			return err
		}
		response, err = lobby.BuildSnapshot(stored, principal.PlayerID)
		if err != nil {
			return err
		}
		body, err := json.Marshal(response)
		if err != nil {
			return err
		}
		record.ResponseJSON = body
		if err := tx.Model(&CreateRequestRecord{}).Where(&CreateRequestRecord{PlayerID: principal.PlayerID, RequestID: request.RequestID}).Select("response_json").Updates(&record).Error; err != nil {
			return err
		}
		// First response and replay have the same public representation, including
		// UTC/timestamp precision and exclusion of internal-only room fields.
		response = lobby.Snapshot{}
		return json.Unmarshal(body, &response)
	})
	if err != nil {
		return lobby.Snapshot{}, storageError(err)
	}
	return response, nil
}

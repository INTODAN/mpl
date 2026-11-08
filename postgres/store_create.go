package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"

	"islandBattles/lobby/domain"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func canonicalCreate(request domain.CreateRequest) (domain.CreateRequest, error) {
	if domain.ValidateUUID(request.RequestID) != nil || domain.ValidateUUID(request.ClientInstanceID) != nil || domain.ValidateGameKey(request.GameKey) != nil {
		return request, domain.ErrInvalidInput
	}
	var err error
	request.Name, err = domain.NormalizeName(request.Name, 80)
	if err != nil {
		return request, err
	}
	request.Teams, err = domain.ValidateTeamDefinitions(request.Teams, request.Capacity)
	if err != nil {
		return request, err
	}
	// The fingerprint covers the canonical settings, so a retry that only
	// reorders keys or changes whitespace still matches.
	request.GameSettings, err = domain.NormalizeGameSettings(request.GameSettings)
	return request, err
}
func (store *Store) Create(ctx context.Context, principal domain.Principal, request domain.CreateRequest, aggregate domain.Aggregate) (domain.Snapshot, error) {
	canonical, err := canonicalCreate(request)
	if err != nil {
		return domain.Snapshot{}, err
	}
	if domain.ValidateUUID(principal.PlayerID) != nil {
		return domain.Snapshot{}, domain.ErrUnauthenticated
	}
	fingerprinted := canonical
	if string(fingerprinted.GameSettings) == domain.EmptyGameSettings {
		// Create requests stored before game settings existed still replay.
		fingerprinted.GameSettings = nil
	}
	payload, err := json.Marshal(fingerprinted)
	if err != nil {
		return domain.Snapshot{}, storageError(err)
	}
	fingerprint := sha256.Sum256(payload)
	var response domain.Snapshot
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Insert-first uniqueness serializes requests without a check/write gap.
		// The temporary response cannot escape this transaction; rollback removes it.
		lobbyID := aggregate.Lobby.ID
		record := CreateRequestRecord{PlayerID: principal.PlayerID, RequestID: request.RequestID, LobbyID: &lobbyID, PayloadHash: fingerprint[:], ResponseJSON: datatypes.JSON(`{}`), CreatedAt: store.clock.Now().UTC()}
		existing, err := insertCreateRequest(tx, record)
		if err != nil {
			return err
		}
		if existing != nil {
			if !bytes.Equal(existing.PayloadHash, fingerprint[:]) {
				return domain.ErrIdempotencyConflict
			}
			if err := json.Unmarshal(existing.ResponseJSON, &response); err != nil {
				return err
			}
			// jsonb reformats the stored settings. The matching fingerprint
			// means they equal this request's canonical settings.
			response.Lobby.GameSettings = canonical.GameSettings
			return nil
		}
		aggregate = aggregate.DeepCopy()
		// Its creator is connected, so a new room is never idle.
		aggregate.Lobby.MarkConnected(aggregate.Lobby.CreatedAt)
		if aggregate.Lobby.Version != 1 || aggregate.Lobby.GameKey != canonical.GameKey || !bytes.Equal(aggregate.Lobby.GameSettings, canonical.GameSettings) {
			return domain.ErrInvalidInput
		}
		room := roomRecord(aggregate.Lobby)
		if err := tx.Create(&room).Error; err != nil {
			if isCodeCollision(err) {
				return domain.ErrJoinCodeCollision
			}
			return err
		}
		if err := persistChildren(tx, aggregate, true, nil); err != nil {
			return err
		}
		stored, err := loadAggregate(tx, aggregate.Lobby.Scope(), false)
		if err != nil {
			return err
		}
		response, err = domain.BuildSnapshot(stored, principal.PlayerID)
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
		response = domain.Snapshot{}
		return json.Unmarshal(body, &response)
	})
	if err != nil {
		return domain.Snapshot{}, storageError(err)
	}
	return response, nil
}

// insertCreateRequest inserts record, or returns the stored request with the
// same key. When the retention pass deletes that request between the
// conflicting insert and the read, because its window just ended, it inserts
// once more.
func insertCreateRequest(tx *gorm.DB, record CreateRequestRecord) (*CreateRequestRecord, error) {
	for range 2 {
		inserted := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "player_id"}, {Name: "request_id"}}, DoNothing: true}).Create(&record)
		if inserted.Error != nil {
			return nil, inserted.Error
		}
		if inserted.RowsAffected == 1 {
			return nil, nil
		}
		var records []CreateRequestRecord
		if err := tx.Where(&CreateRequestRecord{PlayerID: record.PlayerID, RequestID: record.RequestID}).Limit(1).Find(&records).Error; err != nil {
			return nil, err
		}
		if len(records) == 1 {
			return &records[0], nil
		}
	}
	return nil, domain.ErrUnavailable
}

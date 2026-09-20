package postgres

import (
	"context"
	"errors"

	"islandBattles/lobby"

	"gorm.io/gorm"
)

// IdentityStore persists guest players and their single v1 session. It has no
// connection or migration side effects; callers own database setup.
type IdentityStore struct {
	db *gorm.DB
}

func NewIdentityStore(db *gorm.DB) (*IdentityStore, error) {
	if db == nil {
		return nil, errors.New("lobby postgres: nil database")
	}
	return &IdentityStore{db: db}, nil
}

// CreateGuest commits the player and its session together. The session unique
// constraint is still retained at the database layer for direct callers.
func (store *IdentityStore) CreateGuest(ctx context.Context, player lobby.Player, session lobby.Session) error {
	playerRecord := PlayerRecord{ID: player.ID, DisplayName: player.DisplayName, CreatedAt: player.CreatedAt.UTC()}
	sessionRecord := SessionRecord{
		ID:         session.ID,
		PlayerID:   session.PlayerID,
		BearerHash: append([]byte(nil), session.BearerHash[:]...),
		CreatedAt:  session.CreatedAt.UTC(),
		ExpiresAt:  session.ExpiresAt.UTC(),
	}
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&playerRecord).Error; err != nil {
			return err
		}
		return tx.Create(&sessionRecord).Error
	})
}

func (store *IdentityStore) LookupByBearerHash(ctx context.Context, hash lobby.CredentialHash) (*lobby.SessionVerification, error) {
	var sessions []SessionRecord
	if err := store.db.WithContext(ctx).Where(&SessionRecord{BearerHash: hash[:]}).Limit(1).Find(&sessions).Error; err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	return store.verificationForSession(ctx, sessions[0])
}

func (store *IdentityStore) LookupByPlayer(ctx context.Context, playerID string) (*lobby.SessionVerification, error) {
	var sessions []SessionRecord
	if err := store.db.WithContext(ctx).Where(&SessionRecord{PlayerID: playerID}).Limit(1).Find(&sessions).Error; err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, nil
	}
	return store.verificationForSession(ctx, sessions[0])
}

func (store *IdentityStore) verificationForSession(ctx context.Context, session SessionRecord) (*lobby.SessionVerification, error) {
	var players []PlayerRecord
	if err := store.db.WithContext(ctx).Where(&PlayerRecord{ID: session.PlayerID}).Limit(1).Find(&players).Error; err != nil {
		return nil, err
	}
	if len(players) == 0 {
		// A foreign key normally prevents this. Treat an inconsistent database as
		// unavailable instead of accidentally granting an empty identity.
		return nil, errors.New("lobby postgres: session player is missing")
	}
	return &lobby.SessionVerification{
		Principal: lobby.Principal{PlayerID: players[0].ID, SessionID: session.ID, DisplayName: players[0].DisplayName},
		ExpiresAt: session.ExpiresAt.UTC(),
	}, nil
}

var _ lobby.IdentityStore = (*IdentityStore)(nil)

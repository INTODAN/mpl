package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/INTODAN/mpl/domain"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The unique indexes of lobby_accounts, migration 0012's.
const (
	accountSubjectIndex = "ux_lobby_accounts_google_subject"
	accountNameIndex    = "ux_lobby_accounts_display_name_key"
)

func (store *IdentityStore) AccountBySubject(ctx context.Context, subject string) (*domain.Account, error) {
	return store.findAccount(ctx, &AccountRecord{GoogleSubject: subject})
}

func (store *IdentityStore) AccountByID(ctx context.Context, id string) (*domain.Account, error) {
	return store.findAccount(ctx, &AccountRecord{ID: id})
}

func (store *IdentityStore) findAccount(ctx context.Context, filter *AccountRecord) (*domain.Account, error) {
	var accounts []AccountRecord
	if err := store.db.WithContext(ctx).Where(filter).Limit(1).Find(&accounts).Error; err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, nil
	}
	return &domain.Account{ID: accounts[0].ID, DisplayName: accounts[0].DisplayName, CreatedAt: accounts[0].CreatedAt.UTC()}, nil
}

func (store *IdentityStore) CreateAccountSession(ctx context.Context, account domain.Account, subject string, create bool, player domain.Player, session domain.Session) error {
	accountID := account.ID
	playerRecord := PlayerRecord{ID: player.ID, DisplayName: player.DisplayName, CreatedAt: player.CreatedAt.UTC(), AccountID: &accountID}
	sessionRecord := newSessionRecord(session)
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if create {
			createdAt := account.CreatedAt.UTC()
			if err := tx.Create(&AccountRecord{
				ID: account.ID, GoogleSubject: subject, DisplayName: account.DisplayName,
				DisplayNameKey: domain.AccountNameKey(account.DisplayName), CreatedAt: createdAt, UpdatedAt: createdAt,
			}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&playerRecord).Error; err != nil {
			return err
		}
		return tx.Create(&sessionRecord).Error
	})
	return accountConflict(err)
}

func (store *IdentityStore) RenameAccount(ctx context.Context, accountID, playerID, displayName string, updatedAt time.Time) error {
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		renamed := tx.Model(&AccountRecord{}).Where(&AccountRecord{ID: accountID}).Updates(map[string]any{
			"display_name": displayName, "display_name_key": domain.AccountNameKey(displayName), "updated_at": updatedAt.UTC(),
		})
		if renamed.Error != nil {
			return renamed.Error
		}
		if renamed.RowsAffected == 0 {
			return domain.ErrSignInRequired
		}
		return tx.Model(&PlayerRecord{}).Where(&PlayerRecord{ID: playerID, AccountID: &accountID}).Update("display_name", displayName).Error
	})
	return accountConflict(err)
}

func (store *IdentityStore) DeleteAccount(ctx context.Context, accountID string) error {
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Locking the account first makes a concurrent sign-in, whose new
		// player's foreign key needs the row, wait for the deletion and fail,
		// so no session of the account outlives it.
		var locked []AccountRecord
		if err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Where(&AccountRecord{ID: accountID}).Limit(1).Find(&locked).Error; err != nil {
			return err
		}
		if len(locked) == 0 {
			return domain.ErrSignInRequired
		}
		var playerIDs []string
		if err := tx.Model(&PlayerRecord{}).Where(&PlayerRecord{AccountID: &accountID}).Pluck("id", &playerIDs).Error; err != nil {
			return err
		}
		if len(playerIDs) > 0 {
			ofPlayers := clause.IN{Column: clause.Column{Name: "player_id"}, Values: anySlice(playerIDs)}
			if err := tx.Where(ofPlayers).Delete(&SessionRecord{}).Error; err != nil {
				return err
			}
		}
		// The foreign key sets the players' account_id to null.
		return tx.Where(&AccountRecord{ID: accountID}).Delete(&AccountRecord{}).Error
	})
}

// accountConflict turns the unique violations of lobby_accounts into the
// domain's errors.
func accountConflict(err error) error {
	var postgresError *pgconn.PgError
	if err == nil || !errors.As(err, &postgresError) || postgresError.Code != "23505" {
		return err
	}
	switch postgresError.ConstraintName {
	case accountNameIndex:
		return domain.ErrDisplayNameTaken
	case accountSubjectIndex:
		return domain.ErrAccountExists
	}
	return err
}

func newSessionRecord(session domain.Session) SessionRecord {
	return SessionRecord{
		ID:         session.ID,
		PlayerID:   session.PlayerID,
		BearerHash: append([]byte(nil), session.BearerHash[:]...),
		CreatedAt:  session.CreatedAt.UTC(),
		ExpiresAt:  session.ExpiresAt.UTC(),
	}
}

var _ domain.AccountStore = (*IdentityStore)(nil)

func anySlice(values []string) []any {
	converted := make([]any, len(values))
	for index, value := range values {
		converted[index] = value
	}
	return converted
}

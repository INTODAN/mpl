package postgres

import (
	"context"
	"math"
	"reflect"

	"islandBattles/lobby/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) Transact(ctx context.Context, scope domain.Scope, mutation domain.Mutation) (domain.Aggregate, error) {
	if err := validScope(scope); err != nil {
		return domain.Aggregate{}, err
	}
	if mutation == nil {
		return domain.Aggregate{}, domain.ErrInvalidInput
	}
	var committed domain.Aggregate
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		aggregate, err := loadAggregate(tx, scope, true)
		if err != nil {
			return err
		}
		original := aggregate.DeepCopy()
		change, err := mutation(&aggregate)
		if err != nil {
			return err
		}
		if !change.HasWrites() {
			committed = original
			return nil
		}
		if aggregate.Lobby.ID != original.Lobby.ID || aggregate.Lobby.GameKey != original.Lobby.GameKey || !reflect.DeepEqual(aggregate.Teams, original.Teams) {
			return domain.NewError(domain.CodeInvalidInput, "immutable lobby identity or teams changed")
		}
		if len(original.StartRoster) > 0 && !reflect.DeepEqual(aggregate.StartRoster, original.StartRoster) {
			return domain.NewError(domain.CodeInvalidInput, "start roster is immutable")
		}
		for _, member := range original.Members {
			current := domain.FindMember(&aggregate, member.PlayerID)
			if current == nil || current.ID != member.ID {
				return domain.NewError(domain.CodeInvalidInput, "membership history cannot be removed")
			}
		}
		for _, lease := range original.Leases {
			if domain.FindLease(&aggregate, lease.PlayerID, lease.Generation, lease.ClientInstanceID) == nil {
				return domain.NewError(domain.CodeInvalidInput, "lease history cannot be removed")
			}
		}
		aggregate.Lobby.Version = original.Lobby.Version
		if change.PublicChanged {
			if aggregate.Lobby.Version == math.MaxInt64 {
				return domain.ErrUnavailable
			}
			aggregate.Lobby.Version++
		}
		room := roomRecord(aggregate.Lobby)
		if err := tx.Model(&RoomRecord{}).Where(&RoomRecord{ID: scope.LobbyID, GameKey: scope.GameKey}).Select("*").Updates(&room).Error; err != nil {
			return err
		}
		if err := persistChildren(tx, aggregate, false); err != nil {
			return err
		}
		if err := validateRevocations(aggregate.Lobby.Scope(), change); err != nil {
			return err
		}
		// Reload the committed representation's ordering and PostgreSQL timestamp
		// precision inside this transaction; no pre-version callback value escapes.
		committed, err = loadAggregate(tx, scope, false)
		return err
	})
	if err != nil {
		return domain.Aggregate{}, storageError(err)
	}
	return committed.DeepCopy(), nil
}
func persistChildren(tx *gorm.DB, aggregate domain.Aggregate, create bool) error {
	if create {
		for _, team := range aggregate.Teams {
			record := TeamRecord{ID: team.ID, LobbyID: aggregate.Lobby.ID, Name: team.Name, NormalizedName: domain.TeamNameKey(team.Name), Capacity: team.Capacity, TeamOrder: team.Order}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		}
	}
	for _, member := range aggregate.Members {
		record := MembershipRecord{ID: member.ID, LobbyID: aggregate.Lobby.ID, PlayerID: member.PlayerID, DisplayName: member.DisplayName, Generation: member.Generation, Active: member.Active, TeamID: member.TeamID, Ready: member.Ready, JoinedAt: member.JoinedAt, JoinSequence: member.JoinSequence, DisconnectedUntil: member.DisconnectedUntil}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"generation", "active", "team_id", "ready", "joined_at", "join_sequence", "disconnected_until"})}).Create(&record).Error; err != nil {
			return err
		}
	}
	for _, lease := range aggregate.Leases {
		if lease.LobbyID != aggregate.Lobby.ID {
			return domain.NewError(domain.CodeInvalidInput, "lease scope mismatch")
		}
		record := LeaseRecord{LobbyID: lease.LobbyID, PlayerID: lease.PlayerID, Generation: lease.Generation, ClientInstanceID: lease.ClientInstanceID, ExpiresAt: lease.ExpiresAt, ReleasedAt: lease.ReleasedAt}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "lobby_id"}, {Name: "player_id"}, {Name: "generation"}, {Name: "client_instance_id"}}, DoUpdates: clause.AssignmentColumns([]string{"expires_at", "released_at"})}).Create(&record).Error; err != nil {
			return err
		}
	}
	for _, entry := range aggregate.StartRoster {
		record := StartRosterRecord{LobbyID: aggregate.Lobby.ID, PlayerID: entry.PlayerID, TeamID: entry.TeamID, MemberID: entry.MemberID, TeamOrder: entry.TeamOrder}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

// validateRevocations rejects a revocation for another room or an invalid
// membership before commit, because it is pushed only after commit.
func validateRevocations(scope domain.Scope, change domain.Change) error {
	for _, intent := range change.Revocations {
		if intent.Scope != scope || intent.Generation < 1 || domain.ValidateUUID(intent.PlayerID) != nil {
			return domain.NewError(domain.CodeInvalidInput, "invalid revocation intent")
		}
	}
	return nil
}

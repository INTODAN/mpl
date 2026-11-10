package postgres

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"time"

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
		if aggregate.Lobby.ID != original.Lobby.ID || aggregate.Lobby.GameKey != original.Lobby.GameKey {
			return domain.NewError(domain.CodeInvalidInput, "immutable lobby identity changed")
		}
		teamsChanged := !reflect.DeepEqual(aggregate.Teams, original.Teams)
		if teamsChanged || !bytes.Equal(aggregate.Lobby.GameSettings, original.Lobby.GameSettings) {
			if err := validateSettingsChange(original, aggregate); err != nil {
				return err
			}
		}
		if err := validateRoundChange(original, aggregate); err != nil {
			return err
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
		// A kick is final: the row stays, so the join code can't let the
		// watcher back in.
		for _, watcher := range original.Watchers {
			if watcher.Watching() {
				continue
			}
			current := domain.FindWatcher(&aggregate, watcher.PlayerID)
			if current == nil || current.Watching() {
				return domain.NewError(domain.CodeInvalidInput, "a kicked watcher cannot be removed")
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
		// Reopening empties the roster of the round that ended; the next
		// start writes a new one.
		if len(original.StartRoster) > 0 && len(aggregate.StartRoster) == 0 {
			if err := tx.Where(&StartRosterRecord{LobbyID: scope.LobbyID}).Delete(&StartRosterRecord{}).Error; err != nil {
				return err
			}
		}
		if teamsChanged {
			if err := prepareTeamChanges(tx, original.Teams, aggregate.Teams); err != nil {
				return err
			}
		}
		if err := persistChildren(tx, aggregate, false, original.Watchers); err != nil {
			return err
		}
		if teamsChanged {
			if err := finishTeamChanges(tx, original.Teams, aggregate.Teams); err != nil {
				return err
			}
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

// persistChildren stores the aggregate's children. A create passes no
// original watchers.
func persistChildren(tx *gorm.DB, aggregate domain.Aggregate, create bool, originalWatchers []domain.Watcher) error {
	if create {
		for _, team := range aggregate.Teams {
			record := TeamRecord{ID: team.ID, LobbyID: aggregate.Lobby.ID, Name: team.Name, NormalizedName: domain.TeamNameKey(team.Name), TeamOrder: team.Order}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		}
	}
	for _, member := range aggregate.Members {
		kind := domain.MemberHuman
		var seatSettings []byte
		if member.IsSeat() {
			kind, seatSettings = domain.MemberSeat, member.SeatSettings
			if len(seatSettings) == 0 {
				return domain.NewError(domain.CodeInvalidInput, "a seat needs settings")
			}
			// A seat's player has no session; it exists so the start roster
			// and the game can name the seat's player like any other. It is
			// inserted once, with the seat. A plain insert fails on an ID
			// that is already someone's rather than reusing that row.
			var stored int64
			if err := tx.Model(&MembershipRecord{}).Where(&MembershipRecord{ID: member.ID}).Count(&stored).Error; err != nil {
				return err
			}
			if stored == 0 {
				player := PlayerRecord{ID: member.PlayerID, DisplayName: member.DisplayName, CreatedAt: member.JoinedAt}
				if err := tx.Create(&player).Error; err != nil {
					return err
				}
			}
		}
		var seatRequestID *string
		if member.SeatRequestID != "" {
			seatRequestID = &member.SeatRequestID
		}
		record := MembershipRecord{ID: member.ID, LobbyID: aggregate.Lobby.ID, PlayerID: member.PlayerID, DisplayName: member.DisplayName, Generation: member.Generation, Active: member.Active, TeamID: member.TeamID, Ready: member.Ready, JoinedAt: member.JoinedAt, JoinSequence: member.JoinSequence, DisconnectedUntil: member.DisconnectedUntil, Kind: string(kind), SeatSettings: seatSettings, SeatRequestID: seatRequestID}
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
	return persistWatchers(tx, aggregate, originalWatchers)
}

// validateRoundChange keeps a started lobby's roster frozen, and lets the
// round change only when a started lobby is reopened: it goes back to
// waiting in its next round with an empty roster, which the next start fills
// again.
func validateRoundChange(original domain.Aggregate, aggregate domain.Aggregate) error {
	if original.Lobby.Phase == domain.PhaseStarted && aggregate.Lobby.Phase == domain.PhaseWaiting {
		if aggregate.Lobby.Round != original.Lobby.Round+1 || len(aggregate.StartRoster) > 0 {
			return domain.NewError(domain.CodeInvalidInput, "a reopened lobby must wait in its next round with an empty start roster")
		}
		return nil
	}
	if aggregate.Lobby.Round != original.Lobby.Round {
		return domain.NewError(domain.CodeInvalidInput, "the round changes only when a started lobby is reopened")
	}
	if len(original.StartRoster) > 0 && !reflect.DeepEqual(aggregate.StartRoster, original.StartRoster) {
		return domain.NewError(domain.CodeInvalidInput, "start roster is immutable")
	}
	return nil
}

// validateSettingsChange lets a mutation change the game settings and the
// teams only while the lobby waits, and only by rebuilding the teams by
// position: the teams the change keeps hold their ID and order, new teams
// follow them in order, and the capacity is the number of teams.
func validateSettingsChange(original domain.Aggregate, aggregate domain.Aggregate) error {
	if original.Lobby.Phase != domain.PhaseWaiting || aggregate.Lobby.Phase != domain.PhaseWaiting || len(original.StartRoster) > 0 {
		return domain.NewError(domain.CodeInvalidInput, "game settings or teams changed outside the waiting phase")
	}
	if len(aggregate.Teams) < domain.MinTeams || len(aggregate.Teams) > domain.MaxTeams || aggregate.Lobby.Capacity != len(aggregate.Teams) {
		return domain.NewError(domain.CodeInvalidInput, "changed teams don't match the lobby's capacity")
	}
	for index, team := range aggregate.Teams {
		if team.LobbyID != aggregate.Lobby.ID {
			return domain.NewError(domain.CodeInvalidInput, "team scope mismatch")
		}
		if index < len(original.Teams) {
			if team.ID != original.Teams[index].ID || team.Order != original.Teams[index].Order {
				return domain.NewError(domain.CodeInvalidInput, "teams must be rebuilt by position")
			}
			continue
		}
		if index == 0 || team.Order != aggregate.Teams[index-1].Order+1 {
			return domain.NewError(domain.CodeInvalidInput, "teams must be rebuilt by position")
		}
	}
	return nil
}

// A team's rebuild runs in two steps around the membership writes, so that no
// statement trips a constraint halfway: prepareTeamChanges moves each renamed
// team to a placeholder normalized name, which frees its old name for another
// team (two teams can swap names), and inserts the new teams, which members
// may then take. Once the members have left the removed teams,
// finishTeamChanges deletes those teams and gives the renamed teams their new
// names. A rebuild by position never both adds and removes teams, so a new
// team's order is free.
func prepareTeamChanges(tx *gorm.DB, original []domain.Team, changed []domain.Team) error {
	for index, team := range changed {
		if index >= len(original) {
			record := TeamRecord{ID: team.ID, LobbyID: team.LobbyID, Name: team.Name, NormalizedName: domain.TeamNameKey(team.Name), TeamOrder: team.Order}
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
			continue
		}
		if team.Name == original[index].Name {
			continue
		}
		if err := tx.Model(&TeamRecord{}).Where(&TeamRecord{ID: team.ID, LobbyID: team.LobbyID}).Updates(&TeamRecord{NormalizedName: placeholderTeamNameKey(team.ID)}).Error; err != nil {
			return err
		}
	}
	return nil
}

func finishTeamChanges(tx *gorm.DB, original []domain.Team, changed []domain.Team) error {
	if len(original) > len(changed) {
		removed := make([]any, 0, len(original)-len(changed))
		for _, team := range original[len(changed):] {
			removed = append(removed, team.ID)
		}
		if err := tx.Where(&TeamRecord{LobbyID: original[0].LobbyID}).Where(clause.IN{Column: clause.Column{Name: "id"}, Values: removed}).Delete(&TeamRecord{}).Error; err != nil {
			return err
		}
	}
	for index, team := range changed {
		if index >= len(original) || team.Name == original[index].Name {
			continue
		}
		if err := tx.Model(&TeamRecord{}).Where(&TeamRecord{ID: team.ID, LobbyID: team.LobbyID}).Updates(&TeamRecord{Name: team.Name, NormalizedName: domain.TeamNameKey(team.Name)}).Error; err != nil {
			return err
		}
	}
	return nil
}

// placeholderTeamNameKey is a normalized name that no team can have: team
// names are trimmed, so a normalized name never starts with a space.
func placeholderTeamNameKey(teamID string) string { return " " + teamID }

// persistWatchers writes only the watcher rows that changed since the
// mutation loaded them: new rows are inserted, and rows whose kick changed
// are updated. Rows the mutation left alone, as every heartbeat does, aren't
// written. Rows of watchers who left are deleted by key.
func persistWatchers(tx *gorm.DB, aggregate domain.Aggregate, original []domain.Watcher) error {
	before := make(map[string]domain.Watcher, len(original))
	for _, watcher := range original {
		before[watcher.PlayerID] = watcher
	}
	for _, watcher := range aggregate.Watchers {
		if previous, found := before[watcher.PlayerID]; found {
			delete(before, watcher.PlayerID)
			if sameKick(previous.KickedAt, watcher.KickedAt) {
				continue
			}
		}
		record := WatcherRecord{LobbyID: aggregate.Lobby.ID, PlayerID: watcher.PlayerID, DisplayName: watcher.DisplayName, JoinedAt: watcher.JoinedAt, KickedAt: watcher.KickedAt}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "lobby_id"}, {Name: "player_id"}}, DoUpdates: clause.AssignmentColumns([]string{"kicked_at"})}).Create(&record).Error; err != nil {
			return err
		}
	}
	if len(before) == 0 {
		return nil
	}
	left := make([]any, 0, len(before))
	for playerID := range before {
		left = append(left, playerID)
	}
	return tx.Where(&WatcherRecord{LobbyID: aggregate.Lobby.ID}).Where(clause.IN{Column: clause.Column{Name: "player_id"}, Values: left}).Delete(&WatcherRecord{}).Error
}

func sameKick(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.Equal(*second)
}

// validateRevocations rejects a revocation for another room or an invalid
// membership before commit, because it is pushed only after commit.
func validateRevocations(scope domain.Scope, change domain.Change) error {
	for _, intent := range change.Revocations {
		if intent.Scope != scope || (intent.Watch != (intent.Generation == 0)) || intent.Generation < 0 || domain.ValidateUUID(intent.PlayerID) != nil {
			return domain.NewError(domain.CodeInvalidInput, "invalid revocation intent")
		}
	}
	return nil
}

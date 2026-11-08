package postgres

import (
	"context"
	"database/sql"

	"islandBattles/lobby/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) ResolveCode(ctx context.Context, gameKey, code string) (domain.Scope, error) {
	if err := domain.ValidateGameKey(gameKey); err != nil {
		return domain.Scope{}, err
	}
	normalized, err := domain.NormalizeJoinCode(code)
	if err != nil {
		return domain.Scope{}, err
	}
	var records []RoomRecord
	err = store.db.WithContext(ctx).Where(&RoomRecord{GameKey: gameKey, JoinCode: normalized}).Limit(1).Find(&records).Error
	if err != nil {
		return domain.Scope{}, storageError(err)
	}
	if len(records) == 0 {
		return domain.Scope{}, domain.ErrNotFound
	}
	return domain.Scope{GameKey: records[0].GameKey, LobbyID: records[0].ID}, nil
}
func (store *Store) Read(ctx context.Context, scope domain.Scope) (domain.Aggregate, error) {
	if err := validScope(scope); err != nil {
		return domain.Aggregate{}, err
	}
	var aggregate domain.Aggregate
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		aggregate, err = loadAggregate(tx, scope, false)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return domain.Aggregate{}, storageError(err)
	}
	return aggregate.DeepCopy(), nil
}
func loadAggregate(tx *gorm.DB, scope domain.Scope, lock bool) (domain.Aggregate, error) {
	query := tx.Where(&RoomRecord{ID: scope.LobbyID, GameKey: scope.GameKey})
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var rooms []RoomRecord
	if err := query.Limit(1).Find(&rooms).Error; err != nil {
		return domain.Aggregate{}, err
	}
	if len(rooms) == 0 {
		return domain.Aggregate{}, domain.ErrNotFound
	}
	aggregate := domain.Aggregate{Lobby: domainRoom(rooms[0]), Teams: []domain.Team{}, Members: []domain.Member{}, Leases: []domain.Lease{}, StartRoster: domain.StartRoster{}}
	var teams []TeamRecord
	if err := tx.Where(&TeamRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "team_order"}}).Find(&teams).Error; err != nil {
		return domain.Aggregate{}, err
	}
	for _, team := range teams {
		aggregate.Teams = append(aggregate.Teams, domain.Team{ID: team.ID, LobbyID: team.LobbyID, Name: team.Name, Capacity: team.Capacity, Order: team.TeamOrder})
	}
	var members []MembershipRecord
	if err := tx.Where(&MembershipRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "join_sequence"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Find(&members).Error; err != nil {
		return domain.Aggregate{}, err
	}
	for _, member := range members {
		aggregate.Members = append(aggregate.Members, domain.Member{ID: member.ID, PlayerID: member.PlayerID, DisplayName: member.DisplayName, Generation: member.Generation, Active: member.Active, TeamID: member.TeamID, Ready: member.Ready, JoinedAt: member.JoinedAt, JoinSequence: member.JoinSequence, DisconnectedUntil: member.DisconnectedUntil, Kind: domain.MemberKind(member.Kind), SeatSettings: member.SeatSettings, SeatRequestID: deref(member.SeatRequestID)})
	}
	var leases []LeaseRecord
	if err := tx.Where(&LeaseRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "player_id"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "generation"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "client_instance_id"}}).Find(&leases).Error; err != nil {
		return domain.Aggregate{}, err
	}
	for _, lease := range leases {
		aggregate.Leases = append(aggregate.Leases, domain.Lease{LobbyID: lease.LobbyID, PlayerID: lease.PlayerID, Generation: lease.Generation, ClientInstanceID: lease.ClientInstanceID, ExpiresAt: lease.ExpiresAt, ReleasedAt: lease.ReleasedAt})
	}
	var roster []StartRosterRecord
	if err := tx.Where(&StartRosterRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "team_order"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "member_id"}}).Find(&roster).Error; err != nil {
		return domain.Aggregate{}, err
	}
	for _, entry := range roster {
		aggregate.StartRoster = append(aggregate.StartRoster, domain.StartRosterEntry{PlayerID: entry.PlayerID, TeamID: entry.TeamID, MemberID: entry.MemberID, TeamOrder: entry.TeamOrder})
	}
	return aggregate, nil
}

// deref returns the string a nullable column holds, or "" for null.
func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

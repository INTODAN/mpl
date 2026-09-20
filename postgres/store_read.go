package postgres

import (
	"context"
	"database/sql"
	"time"

	"islandBattles/lobby"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) ResolveCode(ctx context.Context, gameKey, code string) (lobby.Scope, error) {
	if err := lobby.ValidateGameKey(gameKey); err != nil {
		return lobby.Scope{}, err
	}
	normalized, err := lobby.NormalizeJoinCode(code)
	if err != nil {
		return lobby.Scope{}, err
	}
	var records []RoomRecord
	err = store.db.WithContext(ctx).Where(&RoomRecord{GameKey: gameKey, JoinCode: normalized}).Limit(1).Find(&records).Error
	if err != nil {
		return lobby.Scope{}, storageError(err)
	}
	if len(records) == 0 {
		return lobby.Scope{}, lobby.ErrNotFound
	}
	return lobby.Scope{GameKey: records[0].GameKey, LobbyID: records[0].ID}, nil
}
func (store *Store) Read(ctx context.Context, scope lobby.Scope) (lobby.Aggregate, error) {
	if err := validScope(scope); err != nil {
		return lobby.Aggregate{}, err
	}
	var aggregate lobby.Aggregate
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		aggregate, err = loadAggregate(tx, scope, false)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return lobby.Aggregate{}, storageError(err)
	}
	return aggregate.DeepCopy(), nil
}
func loadAggregate(tx *gorm.DB, scope lobby.Scope, lock bool) (lobby.Aggregate, error) {
	query := tx.Where(&RoomRecord{ID: scope.LobbyID, GameKey: scope.GameKey})
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var rooms []RoomRecord
	if err := query.Limit(1).Find(&rooms).Error; err != nil {
		return lobby.Aggregate{}, err
	}
	if len(rooms) == 0 {
		return lobby.Aggregate{}, lobby.ErrNotFound
	}
	aggregate := lobby.Aggregate{Lobby: domainRoom(rooms[0]), Teams: []lobby.Team{}, Members: []lobby.Member{}, Leases: []lobby.Lease{}, StartRoster: lobby.StartRoster{}}
	var teams []TeamRecord
	if err := tx.Where(&TeamRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "team_order"}}).Find(&teams).Error; err != nil {
		return lobby.Aggregate{}, err
	}
	for _, team := range teams {
		aggregate.Teams = append(aggregate.Teams, lobby.Team{ID: team.ID, LobbyID: team.LobbyID, Name: team.Name, Capacity: team.Capacity, Order: team.TeamOrder})
	}
	var members []MembershipRecord
	if err := tx.Where(&MembershipRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "join_sequence"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Find(&members).Error; err != nil {
		return lobby.Aggregate{}, err
	}
	for _, member := range members {
		aggregate.Members = append(aggregate.Members, lobby.Member{ID: member.ID, PlayerID: member.PlayerID, DisplayName: member.DisplayName, Generation: member.Generation, Active: member.Active, TeamID: member.TeamID, Ready: member.Ready, JoinedAt: member.JoinedAt, JoinSequence: member.JoinSequence, DisconnectedUntil: member.DisconnectedUntil})
	}
	var leases []LeaseRecord
	if err := tx.Where(&LeaseRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "player_id"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "generation"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "client_instance_id"}}).Find(&leases).Error; err != nil {
		return lobby.Aggregate{}, err
	}
	for _, lease := range leases {
		aggregate.Leases = append(aggregate.Leases, lobby.Lease{LobbyID: lease.LobbyID, PlayerID: lease.PlayerID, Generation: lease.Generation, ClientInstanceID: lease.ClientInstanceID, ExpiresAt: lease.ExpiresAt, ReleasedAt: lease.ReleasedAt})
	}
	var roster []StartRosterRecord
	if err := tx.Where(&StartRosterRecord{LobbyID: scope.LobbyID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "team_order"}}).Order(clause.OrderByColumn{Column: clause.Column{Name: "member_id"}}).Find(&roster).Error; err != nil {
		return lobby.Aggregate{}, err
	}
	for _, entry := range roster {
		aggregate.StartRoster = append(aggregate.StartRoster, lobby.StartRosterEntry{PlayerID: entry.PlayerID, TeamID: entry.TeamID, MemberID: entry.MemberID, TeamOrder: entry.TeamOrder})
	}
	return aggregate, nil
}

// DueScopes uses stored current-generation effective expiries, not sweep time,
// and rotates a bounded cursor so a failing room cannot starve later candidates.
func (store *Store) DueScopes(ctx context.Context, now time.Time, limit int) ([]lobby.Scope, error) {
	if limit < 1 || limit > 1000 {
		return nil, lobby.NewError(lobby.CodeInvalidInput, "due scope limit must be between 1 and 1000")
	}
	store.dueMutex.Lock()
	defer store.dueMutex.Unlock()
	query := store.db.WithContext(ctx).Model(&RoomRecord{}).Where(clause.Neq{Column: "phase", Value: string(lobby.PhaseClosed)}).Where(clause.Expr{SQL: `EXISTS (
 SELECT 1 FROM lobby_memberships AS membership
 WHERE membership.lobby_id = lobby_rooms.id AND membership.active = TRUE
 AND (membership.disconnected_until IS NULL OR membership.disconnected_until <= ?)
 AND NOT EXISTS (SELECT 1 FROM lobby_leases AS lease
 WHERE lease.lobby_id = membership.lobby_id AND lease.player_id = membership.player_id
 AND lease.generation = membership.generation
 AND LEAST(lease.expires_at, COALESCE(lease.released_at, lease.expires_at)) > ?))`, Vars: []any{now.UTC(), now.UTC()}})
	var rooms []RoomRecord
	first := query.Session(&gorm.Session{})
	if store.dueCursor != "" {
		first = first.Where(clause.Gt{Column: "id", Value: store.dueCursor})
	}
	if err := first.Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Limit(limit).Find(&rooms).Error; err != nil {
		return nil, storageError(err)
	}
	if len(rooms) < limit && store.dueCursor != "" {
		var wrapped []RoomRecord
		if err := query.Session(&gorm.Session{}).Where(clause.Lte{Column: "id", Value: store.dueCursor}).Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).Limit(limit - len(rooms)).Find(&wrapped).Error; err != nil {
			return nil, storageError(err)
		}
		rooms = append(rooms, wrapped...)
	}
	scopes := make([]lobby.Scope, 0, len(rooms))
	for _, room := range rooms {
		scopes = append(scopes, lobby.Scope{GameKey: room.GameKey, LobbyID: room.ID})
	}
	if len(rooms) > 0 {
		store.dueCursor = rooms[len(rooms)-1].ID
	}
	return scopes, nil
}

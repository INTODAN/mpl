package postgres

import (
	"errors"
	"sync"

	"islandBattles/lobby"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Store implements aggregate transactions and durable delivery claims. All
// network delivery occurs outside this adapter after Claim has committed.
type Store struct {
	db          *gorm.DB
	clock       lobby.Clock
	identifiers lobby.IdentifierSource
	dueMutex    sync.Mutex
	dueCursor   string
}

var _ lobby.Store = (*Store)(nil)
var _ lobby.DeliveryStore = (*Store)(nil)

func NewStore(db *gorm.DB, clock lobby.Clock, identifiers lobby.IdentifierSource) (*Store, error) {
	if db == nil || clock == nil || identifiers == nil {
		return nil, lobby.NewError(lobby.CodeInvalidInput, "store dependencies are required")
	}
	return &Store{db: db, clock: clock, identifiers: identifiers}, nil
}
func storageError(err error) error {
	if err == nil {
		return nil
	}
	var domain *lobby.Error
	if errors.As(err, &domain) || errors.Is(err, lobby.ErrJoinCodeCollision) {
		return err
	}
	return &lobby.Error{Code: lobby.CodeUnavailable, Message: "lobby storage unavailable", Cause: err}
}
func isCodeCollision(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "ux_lobby_rooms_game_join_code"
}
func validScope(scope lobby.Scope) error {
	if err := lobby.ValidateGameKey(scope.GameKey); err != nil {
		return err
	}
	return lobby.ValidateUUID(scope.LobbyID)
}
func roomRecord(room lobby.Lobby) RoomRecord {
	return RoomRecord{ID: room.ID, GameKey: room.GameKey, Name: room.Name, JoinCode: room.JoinCode, Capacity: room.Capacity, Phase: string(room.Phase), Version: room.Version, HostPlayerID: room.HostPlayerID, CreatedAt: room.CreatedAt, NextJoinSequence: room.NextJoinSequence}
}
func domainRoom(room RoomRecord) lobby.Lobby {
	return lobby.Lobby{ID: room.ID, GameKey: room.GameKey, Name: room.Name, JoinCode: room.JoinCode, Capacity: room.Capacity, Phase: lobby.Phase(room.Phase), Version: room.Version, HostPlayerID: room.HostPlayerID, CreatedAt: room.CreatedAt, NextJoinSequence: room.NextJoinSequence}
}

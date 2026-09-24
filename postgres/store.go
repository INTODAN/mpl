package postgres

import (
	"errors"
	"sync"

	"islandBattles/lobby/domain"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// Store implements aggregate transactions and durable delivery claims. All
// network delivery occurs outside this adapter after Claim has committed.
type Store struct {
	db          *gorm.DB
	clock       domain.Clock
	identifiers domain.IdentifierSource
	dueMutex    sync.Mutex
	dueCursor   string
}

var _ domain.Store = (*Store)(nil)
var _ domain.DeliveryStore = (*Store)(nil)

func NewStore(db *gorm.DB, clock domain.Clock, identifiers domain.IdentifierSource) (*Store, error) {
	if db == nil || clock == nil || identifiers == nil {
		return nil, domain.NewError(domain.CodeInvalidInput, "store dependencies are required")
	}
	return &Store{db: db, clock: clock, identifiers: identifiers}, nil
}
func storageError(err error) error {
	if err == nil {
		return nil
	}
	var domainError *domain.Error
	if errors.As(err, &domainError) || errors.Is(err, domain.ErrJoinCodeCollision) {
		return err
	}
	return &domain.Error{Code: domain.CodeUnavailable, Message: "lobby storage unavailable", Cause: err}
}
func isCodeCollision(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "ux_lobby_rooms_game_join_code"
}
func validScope(scope domain.Scope) error {
	if err := domain.ValidateGameKey(scope.GameKey); err != nil {
		return err
	}
	return domain.ValidateUUID(scope.LobbyID)
}
func roomRecord(room domain.Lobby) RoomRecord {
	return RoomRecord{ID: room.ID, GameKey: room.GameKey, Name: room.Name, JoinCode: room.JoinCode, Capacity: room.Capacity, Phase: string(room.Phase), Version: room.Version, HostPlayerID: room.HostPlayerID, CreatedAt: room.CreatedAt, NextJoinSequence: room.NextJoinSequence}
}
func domainRoom(room RoomRecord) domain.Lobby {
	return domain.Lobby{ID: room.ID, GameKey: room.GameKey, Name: room.Name, JoinCode: room.JoinCode, Capacity: room.Capacity, Phase: domain.Phase(room.Phase), Version: room.Version, HostPlayerID: room.HostPlayerID, CreatedAt: room.CreatedAt, NextJoinSequence: room.NextJoinSequence}
}

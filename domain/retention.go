package domain

import (
	"context"
	"time"
)

// RetentionPolicy says when the retention pass deletes lobby data and how much
// work one pass may do. Nothing is deleted before its period has passed.
type RetentionPolicy struct {
	// CreateRequests is the idempotency window of a create request: a retry
	// inside it replays the original response, and the record is deleted after.
	CreateRequests time.Duration
	// ExpiredSessions is how long an expired session is kept before it is
	// deleted, which covers requests that verified it just before it expired.
	ExpiredSessions time.Duration
	// Lobbies is the default for games that don't override it.
	Lobbies LobbyRetention
	// BatchSize bounds the rows one pass deletes from each kind of data.
	BatchSize int
	// PassInterval is the least time between two passes in one process while
	// the previous pass found no backlog.
	PassInterval time.Duration
}

// LobbyRetention says when a lobby is deleted, in any phase. A game can
// override either period in its registration; a zero field keeps the default.
type LobbyRetention struct {
	// IdlePeriod runs from the last time any member was connected.
	IdlePeriod time.Duration
	// MaximumAge runs from creation and applies even to a lobby in use.
	MaximumAge time.Duration
}

// DefaultRetention keeps create requests for a day, expired sessions for an
// hour, and lobbies for an hour after their last connection or a day after
// creation. A pass deletes at most 50 rows of each kind, at most once a minute
// per process unless the previous pass found more to delete.
func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{
		CreateRequests:  24 * time.Hour,
		ExpiredSessions: time.Hour,
		Lobbies:         LobbyRetention{IdlePeriod: time.Hour, MaximumAge: 24 * time.Hour},
		BatchSize:       50,
		PassInterval:    time.Minute,
	}
}

// MaxRetentionBatchSize bounds BatchSize, so one pass stays short.
const MaxRetentionBatchSize = 1000

// validate also requires the idle period to outlast a lease and its grace,
// so a member who may still reconnect never loses the lobby to idleness.
func (retention RetentionPolicy) validate(policy Policy) error {
	if retention.CreateRequests <= 0 || retention.ExpiredSessions <= 0 || retention.PassInterval <= 0 ||
		retention.BatchSize < 1 || retention.BatchSize > MaxRetentionBatchSize {
		return NewError(CodeInvalidInput, "invalid lobby retention policy")
	}
	return retention.Lobbies.validate(policy, false)
}

func (retention LobbyRetention) validate(policy Policy, override bool) error {
	if override && retention.IdlePeriod == 0 && retention.MaximumAge == 0 {
		return nil
	}
	if retention.IdlePeriod < 0 || retention.MaximumAge < 0 ||
		(!override && (retention.IdlePeriod == 0 || retention.MaximumAge == 0)) ||
		(retention.IdlePeriod != 0 && retention.IdlePeriod <= policy.LeaseDuration+policy.GraceDuration) {
		return NewError(CodeInvalidInput, "invalid lobby retention periods")
	}
	return nil
}

// LobbyRetention returns the periods for a game's lobbies: the game's
// overrides, with the policy's defaults for the fields it leaves zero. A game
// that is no longer registered gets the defaults.
func (policy Policy) LobbyRetention(gameKey string) LobbyRetention {
	retention := policy.Retention.Lobbies
	for _, game := range policy.Games {
		if game.Key != gameKey {
			continue
		}
		if game.Retention.IdlePeriod != 0 {
			retention.IdlePeriod = game.Retention.IdlePeriod
		}
		if game.Retention.MaximumAge != 0 {
			retention.MaximumAge = game.Retention.MaximumAge
		}
	}
	return retention
}

// RetentionCutoffs are the instants before which a pass deletes data, all
// derived from one clock reading.
type RetentionCutoffs struct {
	BatchSize int
	// CreateRequestsBefore deletes create requests made before it.
	CreateRequestsBefore time.Time
	// SessionsExpiredBefore deletes sessions that expired before it.
	SessionsExpiredBefore time.Time
	// PlayersCreatedBefore limits which players are checked for references.
	// A player whose session was deleted was created before it, so younger
	// players are skipped without being checked.
	PlayersCreatedBefore time.Time
	// Games holds one entry per registered game.
	Games []LobbyCutoffs
	// OtherGames applies to lobbies of games that are no longer registered.
	OtherGames LobbyCutoffs
}

// LobbyCutoffs delete a game's lobbies that nobody has been connected to
// since IdleBefore, and those created before CreatedBefore.
type LobbyCutoffs struct {
	GameKey       string
	IdleBefore    time.Time
	CreatedBefore time.Time
}

// RetentionCutoffs derives the cutoffs of a pass that runs at now.
func (policy Policy) RetentionCutoffs(now time.Time) RetentionCutoffs {
	now = now.UTC()
	retention := policy.Retention
	sessionsExpiredBefore := now.Add(-retention.ExpiredSessions)
	cutoffs := RetentionCutoffs{
		BatchSize:             retention.BatchSize,
		CreateRequestsBefore:  now.Add(-retention.CreateRequests),
		SessionsExpiredBefore: sessionsExpiredBefore,
		PlayersCreatedBefore:  sessionsExpiredBefore.Add(-policy.SessionDuration),
		Games:                 make([]LobbyCutoffs, 0, len(policy.Games)),
		OtherGames:            lobbyCutoffs("", retention.Lobbies, now),
	}
	for _, game := range policy.Games {
		cutoffs.Games = append(cutoffs.Games, lobbyCutoffs(game.Key, policy.LobbyRetention(game.Key), now))
	}
	return cutoffs
}

func lobbyCutoffs(gameKey string, retention LobbyRetention, now time.Time) LobbyCutoffs {
	return LobbyCutoffs{GameKey: gameKey, IdleBefore: now.Add(-retention.IdlePeriod), CreatedBefore: now.Add(-retention.MaximumAge)}
}

// RetentionResult counts what one pass deleted. Players counts the players no
// longer referenced by anything, and Lobbies the rooms with their child rows.
type RetentionResult struct {
	CreateRequests int
	Sessions       int
	Lobbies        int
	Players        int
}

// Backlog reports whether a kind of data filled its batch, so more may wait.
func (result RetentionResult) Backlog(batchSize int) bool {
	return max(result.CreateRequests, result.Sessions, result.Lobbies, result.Players) >= batchSize
}

// RetentionStore deletes lobby data past its cutoffs, at most
// cutoffs.BatchSize rows of each kind, each kind in its own short
// transaction. It skips rows another transaction holds, so it never waits on a
// lobby in use, and deletes a room's child rows before the room.
type RetentionStore interface {
	DeleteExpired(context.Context, RetentionCutoffs) (RetentionResult, error)
}

// RetentionRunner runs a retention pass at the end of a request when one is
// due. It reports failures itself and never fails the request.
type RetentionRunner interface {
	RunIfDue(context.Context)
}

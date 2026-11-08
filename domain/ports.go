package domain

import (
	"context"
	"time"
)

type Application interface {
	Create(context.Context, Principal, CreateRequest) (Snapshot, error)
	Join(context.Context, Principal, JoinRequest) (Snapshot, error)
	Snapshot(context.Context, Principal, Scope) (Snapshot, error)
	SelectTeam(context.Context, Principal, Scope, SelectTeamRequest) (Snapshot, error)
	SetReady(context.Context, Principal, Scope, ReadyRequest) (Snapshot, error)
	Start(context.Context, Principal, Scope) (Snapshot, error)
	// AddSeat and RemoveSeat let the host change the seats that no person
	// plays while the lobby waits. RemoveSeat takes the seat's member ID.
	AddSeat(context.Context, Principal, Scope, AddSeatRequest) (Snapshot, error)
	RemoveSeat(context.Context, Principal, Scope, string) (Snapshot, error)
	Leave(context.Context, Principal, Scope) error
	Heartbeat(context.Context, Principal, Scope, HeartbeatRequest) (HeartbeatResult, error)
	ReleaseLease(context.Context, Principal, Scope, ReleaseLeaseRequest) error
	// Watch, Unwatch, Watchers and KickWatcher manage the people who watch a
	// lobby with its join code without taking a seat.
	Watch(context.Context, Principal, WatchRequest) (WatchAccess, error)
	Unwatch(context.Context, Principal, Scope) error
	Watchers(context.Context, Principal, Scope) ([]Watcher, error)
	KickWatcher(context.Context, Principal, Scope, string) error
}

// WatchAuthorizer lets a game check that a player may watch a lobby, for the
// reads it opens to watchers: an active member, or a watcher the host hasn't
// kicked. It returns ErrNotFound otherwise, also for a lobby that is gone.
type WatchAuthorizer interface {
	AuthorizeWatch(context.Context, Principal, Scope) error
}
type Clock interface{ Now() time.Time }
type IdentifierSource interface {
	UUID() (string, error)
	JoinCode() (string, error)
	Bytes(int) ([]byte, error)
}
type Mutation func(*Aggregate) (Change, error)

// Store returns detached copies. Transact locks the room before loading children,
// invokes Mutation exactly once, and atomically persists its changes. Callback
// errors roll back. PublicChanged increments version exactly once. A store never
// calls the network; ChangePublisher pushes committed changes.
// Create returns the saved original snapshot on an identical idempotent replay.
// A join-code unique collision returns ErrJoinCodeCollision for bounded retry.
type Store interface {
	Create(context.Context, Principal, CreateRequest, Aggregate) (Snapshot, error)
	ResolveCode(context.Context, string, string) (Scope, error)
	Read(context.Context, Scope) (Aggregate, error)
	Transact(context.Context, Scope, Mutation) (Aggregate, error)
}

// IdentityStore creates player and unique player session atomically. Optional
// lookups return (nil,nil) only when absent; storage errors are never absence.
type IdentityStore interface {
	CreateGuest(context.Context, Player, Session) error
	LookupByBearerHash(context.Context, CredentialHash) (*SessionVerification, error)
	LookupByPlayer(context.Context, string) (*SessionVerification, error)
}
type Authenticator interface {
	CreateGuest(context.Context, CreateGuestRequest) (GuestSession, error)
	// Authenticate receives the complete Authorization header and parses strict Bearer.
	Authenticate(context.Context, string) (SessionVerification, error)
}
type ConnectionTokenIssuer interface {
	IssueConnectionToken(context.Context, SessionVerification) (ConnectionToken, error)
}
type SubscriptionAuthorizer interface {
	// Arguments are trusted Centrifugo authenticated user UUID and canonical channel.
	// Denials return ErrPermissionDenied/ErrUnauthenticated; outages ErrUnavailable.
	AuthorizeSubscription(context.Context, string, string) (SubscriptionAuthorization, error)
}

// ChangePublisher pushes a committed change to subscribers right after its
// transaction: one Invalidation carrying the room's version when PublicChanged,
// and an unsubscribe from the room channel for each revocation that stillDue
// confirms right before each attempt. It retries briefly and logs what still
// fails. The commit stands either way, and clients recover by re-reading the
// lobby over HTTP.
type ChangePublisher interface {
	PublishChange(ctx context.Context, room Lobby, change Change, stillDue RevocationCheck) error
}

// RevocationCheck reports whether a committed revocation still applies. It is
// false once the player holds a newer active membership, so a late unsubscribe
// never drops the subscription of a player who rejoined meanwhile.
type RevocationCheck func(context.Context, RevocationIntent) (bool, error)

// SnapshotReader authorizes current membership and returns a detached snapshot.
// Application implements this boundary, including expiry normalization.
type SnapshotReader interface {
	Snapshot(context.Context, Principal, Scope) (Snapshot, error)
}

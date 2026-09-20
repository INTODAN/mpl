package lobby

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
	Leave(context.Context, Principal, Scope) error
	Heartbeat(context.Context, Principal, Scope, HeartbeatRequest) (HeartbeatResult, error)
	ReleaseLease(context.Context, Principal, Scope, ReleaseLeaseRequest) error
}
type Clock interface{ Now() time.Time }
type IdentifierSource interface {
	UUID() (string, error)
	JoinCode() (string, error)
	Bytes(int) ([]byte, error)
}
type Mutation func(*Aggregate) (Change, error)

// Store returns detached copies. Transact locks the room before loading children,
// invokes Mutation exactly once, and atomically persists changes and delivery jobs.
// Callback errors roll back. PublicChanged increments version exactly once.
// Create returns the saved original snapshot on an identical idempotent replay.
// A join-code unique collision returns ErrJoinCodeCollision for bounded retry.
type Store interface {
	Create(context.Context, Principal, CreateRequest, Aggregate) (Snapshot, error)
	ResolveCode(context.Context, string, string) (Scope, error)
	Read(context.Context, Scope) (Aggregate, error)
	Transact(context.Context, Scope, Mutation) (Aggregate, error)
	// DueScopes is bounded and must fairly rotate failed scopes across calls.
	DueScopes(context.Context, time.Time, int) ([]Scope, error)
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

// DeliveryStore commits claims before returning. Claim increments Attempts and
// allocates a fresh cryptographic token for each job, including reclaimed jobs.
// Ack/Retry return false when ownership is lost, expired, or already completed.
// They condition on token AND ClaimDeadline > now. Retry retains Attempts.
type DeliveryStore interface {
	Claim(context.Context, ClaimRequest) ([]DeliveryJob, error)
	Ack(context.Context, string, string, time.Time) (bool, error)
	Retry(context.Context, RetryRequest) (bool, error)
	CurrentMembership(context.Context, Scope, string) (*MembershipState, error)
}
type RealtimeTransport interface {
	PublishInvalidation(context.Context, string, Invalidation) error
	Unsubscribe(context.Context, string, string) error // player UUID, exact channel
}

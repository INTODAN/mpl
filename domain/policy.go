package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

type Policy struct {
	HeartbeatInterval       time.Duration
	LeaseDuration           time.Duration
	GraceDuration           time.Duration
	SweepInterval           time.Duration
	SessionDuration         time.Duration
	ConnectionTokenDuration time.Duration
	SubscriptionDuration    time.Duration
	RegisteredGameKeys      []string
}

// DefaultPolicy is explicit: the zero Policy is invalid, never silently defaulted.
func DefaultPolicy(gameKeys ...string) Policy {
	return Policy{HeartbeatInterval: 15 * time.Second, LeaseDuration: 45 * time.Second,
		GraceDuration: 60 * time.Second, SweepInterval: 5 * time.Second,
		SessionDuration: 24 * time.Hour, ConnectionTokenDuration: 5 * time.Minute,
		SubscriptionDuration: 60 * time.Second, RegisteredGameKeys: append([]string{}, gameKeys...)}
}
func (policy Policy) Validate() error {
	if policy.HeartbeatInterval <= 0 || policy.LeaseDuration <= policy.HeartbeatInterval ||
		policy.GraceDuration <= 0 || policy.SweepInterval <= 0 || policy.SweepInterval > policy.GraceDuration ||
		policy.SweepInterval > policy.LeaseDuration || policy.SessionDuration <= 0 ||
		policy.ConnectionTokenDuration <= 0 || policy.ConnectionTokenDuration > policy.SessionDuration ||
		policy.SubscriptionDuration <= 0 || policy.SubscriptionDuration > policy.SessionDuration {
		return NewError(CodeInvalidInput, "invalid lobby policy durations")
	}
	if len(policy.RegisteredGameKeys) == 0 {
		return NewError(CodeInvalidInput, "at least one game must be registered")
	}
	seen := map[string]bool{}
	for _, key := range policy.RegisteredGameKeys {
		if ValidateGameKey(key) != nil || seen[key] {
			return NewError(CodeInvalidInput, "invalid or duplicate registered game key")
		}
		seen[key] = true
	}
	return nil
}
func (policy Policy) ValidateGame(key string) error {
	if err := ValidateGameKey(key); err != nil {
		return err
	}
	for _, registered := range policy.RegisteredGameKeys {
		if registered == key {
			return nil
		}
	}
	return NewError(CodeInvalidInput, "game is not registered")
}
func (policy Policy) ValidateScope(scope Scope) error {
	if err := policy.ValidateGame(scope.GameKey); err != nil {
		return err
	}
	return ValidateUUID(scope.LobbyID)
}
func NormalizeName(name string, maximum int) (string, error) {
	if !utf8.ValidString(name) {
		return "", NewError(CodeInvalidInput, "name is not valid UTF-8")
	}
	name = strings.TrimSpace(name)
	if maximum < 1 || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > maximum {
		return "", NewError(CodeInvalidInput, "name length is invalid")
	}
	return name, nil
}

// TeamNameKey is Unicode default case folding after trimming. Persist this key
// for the database uniqueness constraint; preserve the original normalized label.
func TeamNameKey(name string) string { return cases.Fold().String(strings.TrimSpace(name)) }
func ValidateTeamDefinitions(definitions []TeamDefinition, lobbyCapacity int) ([]TeamDefinition, error) {
	if lobbyCapacity < 2 || lobbyCapacity > 64 || len(definitions) < 2 || len(definitions) > 16 {
		return nil, NewError(CodeInvalidInput, "invalid lobby capacity or team count")
	}
	result := make([]TeamDefinition, len(definitions))
	seen := map[string]bool{}
	total := 0
	for index, definition := range definitions {
		name, err := NormalizeName(definition.Name, 40)
		if err != nil {
			return nil, err
		}
		key := TeamNameKey(name)
		if definition.Capacity < 1 || definition.Capacity > 64 || seen[key] {
			return nil, NewError(CodeInvalidInput, "invalid team capacity or duplicate team name")
		}
		seen[key] = true
		total += definition.Capacity
		result[index] = TeamDefinition{Name: name, Capacity: definition.Capacity}
	}
	if total < lobbyCapacity {
		return nil, NewError(CodeInvalidInput, "team capacities do not cover lobby capacity")
	}
	return result, nil
}

// ElectHost returns a detached player UUID; inactive history is never eligible.
func ElectHost(members []Member) *string {
	var chosen *Member
	for index := range members {
		member := &members[index]
		if !member.Active {
			continue
		}
		if chosen == nil || member.JoinSequence < chosen.JoinSequence || (member.JoinSequence == chosen.JoinSequence && member.ID < chosen.ID) {
			chosen = member
		}
	}
	if chosen == nil {
		return nil
	}
	value := chosen.PlayerID
	return &value
}
func EffectiveLeaseExpiry(lease Lease) time.Time {
	if lease.ReleasedAt != nil && lease.ReleasedAt.Before(lease.ExpiresAt) {
		return lease.ReleasedAt.UTC()
	}
	return lease.ExpiresAt.UTC()
}

// LatestEffectiveLeaseExpiry includes released current-generation leases. Missing
// leases return false, allowing callers to handle corrupt/incomplete state safely.
func LatestEffectiveLeaseExpiry(leases []Lease, playerID string, generation int64) (time.Time, bool) {
	var latest time.Time
	found := false
	for _, lease := range leases {
		if lease.PlayerID != playerID || lease.Generation != generation {
			continue
		}
		expiry := EffectiveLeaseExpiry(lease)
		if !found || expiry.After(latest) {
			latest = expiry
		}
		found = true
	}
	return latest, found
}
func HasLiveLease(leases []Lease, playerID string, generation int64, now time.Time) bool {
	for _, lease := range leases {
		if lease.PlayerID == playerID && lease.Generation == generation && lease.ReleasedAt == nil && lease.ExpiresAt.After(now) {
			return true
		}
	}
	return false
}

// FindMember and FindLease return pointers into the aggregate for locked mutation.
// Missing records return nil. FindLease never infers or bypasses generation checks.
func FindMember(aggregate *Aggregate, playerID string) *Member {
	if aggregate == nil {
		return nil
	}
	for index := range aggregate.Members {
		if aggregate.Members[index].PlayerID == playerID {
			return &aggregate.Members[index]
		}
	}
	return nil
}
func FindLease(aggregate *Aggregate, playerID string, generation int64, clientID string) *Lease {
	if aggregate == nil {
		return nil
	}
	for index := range aggregate.Leases {
		lease := &aggregate.Leases[index]
		if lease.PlayerID == playerID && lease.Generation == generation && lease.ClientInstanceID == clientID {
			return lease
		}
	}
	return nil
}

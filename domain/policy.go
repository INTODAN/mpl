package domain

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/cases"
)

type Policy struct {
	HeartbeatInterval       time.Duration
	LeaseDuration           time.Duration
	GraceDuration           time.Duration
	SessionDuration         time.Duration
	ConnectionTokenDuration time.Duration
	SubscriptionDuration    time.Duration
	Retention               RetentionPolicy
	Games                   []GameRegistration
}

// DefaultPolicy is explicit: the zero Policy is invalid, never silently defaulted.
func DefaultPolicy(games ...GameRegistration) Policy {
	return Policy{HeartbeatInterval: 15 * time.Second, LeaseDuration: 45 * time.Second,
		GraceDuration:   60 * time.Second,
		SessionDuration: 24 * time.Hour, ConnectionTokenDuration: 5 * time.Minute,
		SubscriptionDuration: 60 * time.Second, Retention: DefaultRetention(), Games: slices.Clone(games)}
}
func (policy Policy) Validate() error {
	if policy.HeartbeatInterval <= 0 || policy.LeaseDuration <= policy.HeartbeatInterval ||
		policy.GraceDuration <= 0 || policy.SessionDuration <= 0 ||
		policy.ConnectionTokenDuration <= 0 || policy.ConnectionTokenDuration > policy.SessionDuration ||
		policy.SubscriptionDuration <= 0 || policy.SubscriptionDuration > policy.SessionDuration {
		return NewError(CodeInvalidInput, "invalid lobby policy durations")
	}
	if err := policy.Retention.validate(policy); err != nil {
		return err
	}
	if len(policy.Games) == 0 {
		return NewError(CodeInvalidInput, "at least one game must be registered")
	}
	seen := map[string]bool{}
	for _, game := range policy.Games {
		if ValidateGameKey(game.Key) != nil || seen[game.Key] {
			return NewError(CodeInvalidInput, "invalid or duplicate registered game key")
		}
		if err := game.Retention.validate(policy, true); err != nil {
			return err
		}
		seen[game.Key] = true
	}
	return nil
}

// Game returns the registration for a game key.
func (policy Policy) Game(key string) (GameRegistration, error) {
	if err := ValidateGameKey(key); err != nil {
		return GameRegistration{}, err
	}
	for _, game := range policy.Games {
		if game.Key == key {
			return game, nil
		}
	}
	return GameRegistration{}, NewError(CodeInvalidInput, "game is not registered")
}
func (policy Policy) ValidateGame(key string) error {
	_, err := policy.Game(key)
	return err
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

// MinTeams and MaxTeams bound the number of teams in a lobby.
const (
	MinTeams = 2
	MaxTeams = 16
)

// TeamNameKey is Unicode default case folding after trimming. Persist this key
// for the database uniqueness constraint; preserve the original normalized label.
func TeamNameKey(name string) string { return cases.Fold().String(strings.TrimSpace(name)) }

// ValidateTeamDefinitions checks a new lobby's teams: from MinTeams to
// MaxTeams, with unique names. A team holds at most one member, so the number
// of teams is also the number of members the lobby holds.
func ValidateTeamDefinitions(definitions []TeamDefinition) ([]TeamDefinition, error) {
	if len(definitions) < MinTeams || len(definitions) > MaxTeams {
		return nil, NewError(CodeInvalidInput, "invalid team count")
	}
	result := make([]TeamDefinition, len(definitions))
	seen := map[string]bool{}
	for index, definition := range definitions {
		name, err := NormalizeName(definition.Name, 40)
		if err != nil {
			return nil, err
		}
		key := TeamNameKey(name)
		if seen[key] {
			return nil, NewError(CodeInvalidInput, "duplicate team name")
		}
		seen[key] = true
		result[index] = TeamDefinition{Name: name}
	}
	return result, nil
}

// ElectHost returns a detached player UUID; inactive history and seats are
// never eligible.
func ElectHost(members []Member) *string {
	var chosen *Member
	for index := range members {
		member := &members[index]
		if !member.Active || member.IsSeat() {
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

// LobbyFull reports whether the lobby has as many active members, people and
// seats, as teams. A team holds at most one member, so a full lobby has no
// room for another.
func LobbyFull(aggregate *Aggregate) bool {
	activeMembers := 0
	for _, member := range aggregate.Members {
		if member.Active {
			activeMembers++
		}
	}
	return activeMembers >= len(aggregate.Teams)
}

// TeamTaken reports whether an active member, a person or a seat, is on the
// team. A taken team has no room for another member.
func TeamTaken(aggregate *Aggregate, teamID string) bool {
	for _, member := range aggregate.Members {
		if member.Active && member.TeamID != nil && *member.TeamID == teamID {
			return true
		}
	}
	return false
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

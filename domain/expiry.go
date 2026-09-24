package domain

import (
	"time"

	"islandBattles/lobby/internal/optional"
)

// NormalizeExpired derives visible connection state from durable current-generation
// leases. The caller holds the room lock; the store assigns versions and events.
func NormalizeExpired(aggregate *Aggregate, now time.Time, policy Policy) (Change, error) {
	if aggregate == nil {
		return Change{}, ErrInvalidInput
	}
	change := Change{}
	for memberIndex := range aggregate.Members {
		member := &aggregate.Members[memberIndex]
		if !member.Active {
			continue
		}
		var latestExpiry time.Time
		hasLease := false
		hasLiveLease := false
		for _, lease := range aggregate.Leases {
			if lease.PlayerID != member.PlayerID || lease.Generation != member.Generation {
				continue
			}
			hasLease = true
			effectiveExpiry := lease.ExpiresAt
			if lease.ReleasedAt != nil && lease.ReleasedAt.Before(effectiveExpiry) {
				effectiveExpiry = *lease.ReleasedAt
			}
			if effectiveExpiry.After(latestExpiry) {
				latestExpiry = effectiveExpiry
			}
			if lease.ReleasedAt == nil && now.Before(lease.ExpiresAt) {
				hasLiveLease = true
			}
		}
		if !hasLease {
			return Change{}, NewError(CodeUnavailable, "active membership has no durable lease")
		}
		if hasLiveLease {
			if member.DisconnectedUntil != nil {
				member.DisconnectedUntil = nil
				change.PublicChanged = true
			}
			continue
		}
		deadline := latestExpiry.Add(policy.GraceDuration)
		if now.Before(deadline) {
			if member.DisconnectedUntil == nil || !member.DisconnectedUntil.Equal(deadline) {
				member.DisconnectedUntil = &deadline
				change.PublicChanged = true
			}
			continue
		}
		member.Active = false
		member.TeamID = nil
		member.Ready = false
		member.DisconnectedUntil = nil
		for leaseIndex := range aggregate.Leases {
			lease := &aggregate.Leases[leaseIndex]
			if lease.PlayerID == member.PlayerID && lease.Generation == member.Generation && lease.ReleasedAt == nil {
				releasedAt := now
				lease.ReleasedAt = &releasedAt
			}
		}
		change.PublicChanged = true
		change.Revocations = append(change.Revocations, RevocationIntent{
			PlayerID: member.PlayerID, Scope: aggregate.Lobby.Scope(), Generation: member.Generation,
		})
	}
	// Existing hosts in grace retain their role. Elect only when the host is absent
	// or inactive; the frozen game roster never participates in live host selection.
	hostActive := false
	for _, member := range aggregate.Members {
		if member.Active && aggregate.Lobby.HostPlayerID != nil && member.PlayerID == *aggregate.Lobby.HostPlayerID {
			hostActive = true
		}
	}
	if !hostActive {
		nextHost := ElectHost(aggregate.Members)
		if !optional.Equal(aggregate.Lobby.HostPlayerID, nextHost) {
			aggregate.Lobby.HostPlayerID = nextHost
			change.PublicChanged = true
		}
		if nextHost == nil && aggregate.Lobby.Phase == PhaseWaiting {
			aggregate.Lobby.Phase = PhaseClosed
			change.PublicChanged = true
		}
	}
	return change, nil
}

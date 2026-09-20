package lobby

import (
	"context"
	"time"
)

func (service *Service) Join(ctx context.Context, principal Principal, request JoinRequest) (Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return Snapshot{}, err
	}
	if err := service.policy.ValidateGame(request.GameKey); err != nil {
		return Snapshot{}, err
	}
	if ValidateUUID(request.ClientInstanceID) != nil {
		return Snapshot{}, ErrInvalidInput
	}
	code, err := NormalizeJoinCode(request.JoinCode)
	if err != nil {
		return Snapshot{}, err
	}
	displayName, err := NormalizeName(principal.DisplayName, 40)
	if err != nil {
		return Snapshot{}, ErrUnauthenticated
	}
	scope, err := service.store.ResolveCode(ctx, request.GameKey, code)
	if err != nil {
		return Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *Aggregate, now time.Time) (Change, error) {
		if aggregate.Lobby.Phase == PhaseClosed {
			return Change{}, ErrNotFound
		}
		member := FindMember(aggregate, principal.PlayerID)
		if member != nil && member.Active {
			lease := FindLease(aggregate, principal.PlayerID, member.Generation, request.ClientInstanceID)
			if lease != nil && lease.ReleasedAt != nil {
				return Change{}, ErrStaleMembership
			}
			if lease == nil {
				aggregate.Leases = append(aggregate.Leases, Lease{LobbyID: scope.LobbyID, PlayerID: principal.PlayerID, Generation: member.Generation, ClientInstanceID: request.ClientInstanceID, ExpiresAt: now.Add(service.policy.LeaseDuration)})
			} else {
				lease.ExpiresAt = now.Add(service.policy.LeaseDuration)
			}
			change := Change{PrivateChanged: true, PublicChanged: member.DisconnectedUntil != nil}
			member.DisconnectedUntil = nil
			return change, nil
		}
		var original *StartRosterEntry
		if aggregate.Lobby.Phase == PhaseStarted {
			for index := range aggregate.StartRoster {
				if aggregate.StartRoster[index].PlayerID == principal.PlayerID {
					original = &aggregate.StartRoster[index]
					break
				}
			}
			if original == nil {
				return Change{}, ErrNotFound
			}
		}
		count := 0
		for _, candidate := range aggregate.Members {
			if candidate.Active {
				count++
			}
		}
		if count >= aggregate.Lobby.Capacity {
			return Change{}, ErrLobbyFull
		}
		if member == nil {
			identifier, err := service.identifiers.UUID()
			if err != nil {
				return Change{}, unavailable(err)
			}
			aggregate.Members = append(aggregate.Members, Member{ID: identifier, PlayerID: principal.PlayerID, DisplayName: displayName})
			member = &aggregate.Members[len(aggregate.Members)-1]
		}
		member.Generation++
		member.Active = true
		member.Ready = false
		member.TeamID = nil
		member.DisconnectedUntil = nil
		member.JoinedAt = now
		member.JoinSequence = aggregate.Lobby.NextJoinSequence
		aggregate.Lobby.NextJoinSequence++
		if original != nil {
			teamID := original.TeamID
			member.TeamID = &teamID
		}
		if aggregate.Lobby.HostPlayerID == nil {
			host := principal.PlayerID
			aggregate.Lobby.HostPlayerID = &host
		}
		aggregate.Leases = append(aggregate.Leases, Lease{LobbyID: scope.LobbyID, PlayerID: principal.PlayerID, Generation: member.Generation, ClientInstanceID: request.ClientInstanceID, ExpiresAt: now.Add(service.policy.LeaseDuration)})
		return Change{PublicChanged: true}, nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return BuildSnapshot(aggregate, principal.PlayerID)
}

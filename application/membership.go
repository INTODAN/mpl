package application

import (
	"context"
	"errors"
	"time"

	"islandBattles/lobby/domain"
)

func (service *Service) Create(ctx context.Context, principal domain.Principal, request domain.CreateRequest) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Snapshot{}, err
	}
	if err := service.policy.ValidateGame(request.GameKey); err != nil {
		return domain.Snapshot{}, err
	}
	if domain.ValidateUUID(request.RequestID) != nil || domain.ValidateUUID(request.ClientInstanceID) != nil {
		return domain.Snapshot{}, domain.ErrInvalidInput
	}
	name, err := domain.NormalizeName(request.Name, 80)
	if err != nil {
		return domain.Snapshot{}, err
	}
	request.Name = name
	teams, err := domain.ValidateTeamDefinitions(request.Teams)
	if err != nil {
		return domain.Snapshot{}, err
	}
	request.Teams = teams
	displayName, err := domain.NormalizeName(principal.DisplayName, 40)
	if err != nil {
		return domain.Snapshot{}, domain.ErrUnauthenticated
	}
	settings, err := service.policy.ValidateGameSettings(ctx, request.GameKey, request.GameSettings, request.Teams)
	if err != nil {
		return domain.Snapshot{}, err
	}
	request.GameSettings = settings
	for range 5 {
		roomID, err := service.identifiers.UUID()
		if err != nil {
			return domain.Snapshot{}, unavailable(err)
		}
		memberID, err := service.identifiers.UUID()
		if err != nil {
			return domain.Snapshot{}, unavailable(err)
		}
		joinCode, err := service.identifiers.JoinCode()
		if err != nil {
			return domain.Snapshot{}, unavailable(err)
		}
		now := service.clock.Now().UTC()
		host := principal.PlayerID
		aggregate := domain.Aggregate{
			Lobby:   domain.Lobby{ID: roomID, GameKey: request.GameKey, Name: request.Name, JoinCode: joinCode, Capacity: len(request.Teams), Phase: domain.PhaseWaiting, Round: 1, Version: 1, HostPlayerID: &host, CreatedAt: now, GameSettings: settings, NextJoinSequence: 2, LastConnectedAt: now},
			Members: []domain.Member{{ID: memberID, PlayerID: principal.PlayerID, DisplayName: displayName, Generation: 1, Active: true, JoinedAt: now, JoinSequence: 1, Kind: domain.MemberHuman}},
			Leases:  []domain.Lease{{LobbyID: roomID, PlayerID: principal.PlayerID, Generation: 1, ClientInstanceID: request.ClientInstanceID, ExpiresAt: now.Add(service.policy.LeaseDuration)}},
			Teams:   []domain.Team{}, StartRoster: domain.StartRoster{},
		}
		for order, definition := range request.Teams {
			teamID, err := service.identifiers.UUID()
			if err != nil {
				return domain.Snapshot{}, unavailable(err)
			}
			aggregate.Teams = append(aggregate.Teams, domain.Team{ID: teamID, LobbyID: roomID, Name: definition.Name, Order: order + 1})
		}
		snapshot, err := service.store.Create(ctx, principal, request, aggregate)
		if errors.Is(err, domain.ErrJoinCodeCollision) {
			continue
		}
		if err != nil {
			return domain.Snapshot{}, err
		}
		return service.withStartRules(snapshot)
	}
	return domain.Snapshot{}, domain.ErrUnavailable
}

func (service *Service) Join(ctx context.Context, principal domain.Principal, request domain.JoinRequest) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if err := service.policy.ValidateGame(request.GameKey); err != nil {
		return domain.Snapshot{}, err
	}
	if domain.ValidateUUID(request.ClientInstanceID) != nil {
		return domain.Snapshot{}, domain.ErrInvalidInput
	}
	code, err := domain.NormalizeJoinCode(request.JoinCode)
	if err != nil {
		return domain.Snapshot{}, err
	}
	displayName, err := domain.NormalizeName(principal.DisplayName, 40)
	if err != nil {
		return domain.Snapshot{}, domain.ErrUnauthenticated
	}
	scope, err := service.store.ResolveCode(ctx, request.GameKey, code)
	if err != nil {
		return domain.Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		if aggregate.Lobby.Phase == domain.PhaseClosed {
			return domain.Change{}, domain.ErrNotFound
		}
		member := domain.FindMember(aggregate, principal.PlayerID)
		if member != nil && member.Active {
			lease := domain.FindLease(aggregate, principal.PlayerID, member.Generation, request.ClientInstanceID)
			if lease != nil && lease.ReleasedAt != nil {
				return domain.Change{}, domain.ErrStaleMembership
			}
			if lease == nil {
				aggregate.Leases = append(aggregate.Leases, domain.Lease{LobbyID: scope.LobbyID, PlayerID: principal.PlayerID, Generation: member.Generation, ClientInstanceID: request.ClientInstanceID, ExpiresAt: now.Add(service.policy.LeaseDuration)})
			} else {
				lease.ExpiresAt = now.Add(service.policy.LeaseDuration)
			}
			change := domain.Change{PrivateChanged: true, PublicChanged: member.DisconnectedUntil != nil}
			member.DisconnectedUntil = nil
			aggregate.Lobby.MarkConnected(now)
			return change, nil
		}
		var original *domain.StartRosterEntry
		if aggregate.Lobby.Phase == domain.PhaseStarted {
			for index := range aggregate.StartRoster {
				if aggregate.StartRoster[index].PlayerID == principal.PlayerID {
					original = &aggregate.StartRoster[index]
					break
				}
			}
			if original == nil {
				return domain.Change{}, domain.ErrNotFound
			}
		}
		if domain.LobbyFull(aggregate) {
			return domain.Change{}, domain.ErrLobbyFull
		}
		if member == nil {
			identifier, err := service.identifiers.UUID()
			if err != nil {
				return domain.Change{}, unavailable(err)
			}
			aggregate.Members = append(aggregate.Members, domain.Member{ID: identifier, PlayerID: principal.PlayerID, DisplayName: displayName, Kind: domain.MemberHuman})
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
		aggregate.Leases = append(aggregate.Leases, domain.Lease{LobbyID: scope.LobbyID, PlayerID: principal.PlayerID, Generation: member.Generation, ClientInstanceID: request.ClientInstanceID, ExpiresAt: now.Add(service.policy.LeaseDuration)})
		aggregate.Lobby.MarkConnected(now)
		// A member isn't also a watcher: drop the row, unless it is a kick,
		// which stays so that the session can't watch after it leaves.
		// Kicked rows don't count as watchers.
		if watcher := domain.FindWatcher(aggregate, principal.PlayerID); watcher != nil && watcher.Watching() {
			kept := aggregate.Watchers[:0]
			for _, candidate := range aggregate.Watchers {
				if candidate.PlayerID != principal.PlayerID {
					kept = append(kept, candidate)
				}
			}
			aggregate.Watchers = kept
		}
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}

func (service *Service) Snapshot(ctx context.Context, principal domain.Principal, scope domain.Scope) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		_, err := activeMember(aggregate, principal.PlayerID)
		return domain.Change{}, err
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}

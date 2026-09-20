package lobby

import (
	"context"
	"errors"
)

func (service *Service) Create(ctx context.Context, principal Principal, request CreateRequest) (Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := service.policy.ValidateGame(request.GameKey); err != nil {
		return Snapshot{}, err
	}
	if ValidateUUID(request.RequestID) != nil || ValidateUUID(request.ClientInstanceID) != nil {
		return Snapshot{}, ErrInvalidInput
	}
	name, err := NormalizeName(request.Name, 80)
	if err != nil {
		return Snapshot{}, err
	}
	request.Name = name
	teams, err := ValidateTeamDefinitions(request.Teams, request.Capacity)
	if err != nil {
		return Snapshot{}, err
	}
	request.Teams = teams
	displayName, err := NormalizeName(principal.DisplayName, 40)
	if err != nil {
		return Snapshot{}, ErrUnauthenticated
	}
	for range 5 {
		roomID, err := service.identifiers.UUID()
		if err != nil {
			return Snapshot{}, unavailable(err)
		}
		memberID, err := service.identifiers.UUID()
		if err != nil {
			return Snapshot{}, unavailable(err)
		}
		joinCode, err := service.identifiers.JoinCode()
		if err != nil {
			return Snapshot{}, unavailable(err)
		}
		now := service.clock.Now().UTC()
		host := principal.PlayerID
		aggregate := Aggregate{
			Lobby:   Lobby{ID: roomID, GameKey: request.GameKey, Name: request.Name, JoinCode: joinCode, Capacity: request.Capacity, Phase: PhaseWaiting, Version: 1, HostPlayerID: &host, CreatedAt: now, NextJoinSequence: 2},
			Members: []Member{{ID: memberID, PlayerID: principal.PlayerID, DisplayName: displayName, Generation: 1, Active: true, JoinedAt: now, JoinSequence: 1}},
			Leases:  []Lease{{LobbyID: roomID, PlayerID: principal.PlayerID, Generation: 1, ClientInstanceID: request.ClientInstanceID, ExpiresAt: now.Add(service.policy.LeaseDuration)}},
			Teams:   []Team{}, StartRoster: StartRoster{},
		}
		for order, definition := range request.Teams {
			teamID, err := service.identifiers.UUID()
			if err != nil {
				return Snapshot{}, unavailable(err)
			}
			aggregate.Teams = append(aggregate.Teams, Team{ID: teamID, LobbyID: roomID, Name: definition.Name, Capacity: definition.Capacity, Order: order + 1})
		}
		snapshot, err := service.store.Create(ctx, principal, request, aggregate)
		if errors.Is(err, ErrJoinCodeCollision) {
			continue
		}
		return snapshot, err
	}
	return Snapshot{}, ErrUnavailable
}

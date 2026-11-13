package application

import (
	"bytes"
	"context"
	"time"

	"islandBattles/lobby/domain"
)

// ChangeSettings replaces a waiting lobby's game settings and teams. Only the
// host can change them. The teams and settings follow the rules of a create
// request, and the game checks the settings against the new teams before the
// transaction, so it never runs while the room is locked.
//
// The teams are rebuilt by position: the lobby's first team stays the first,
// with its ID, and takes the first new name, and so on. New teams are added
// at the end, and teams beyond the new count are removed: a person on a
// removed team stays in the lobby without a team, and a seat on a removed
// team is removed like RemoveSeat removes it. Every person's ready is cleared
// and the lobby's capacity becomes the new number of teams. A change that
// leaves more members than teams, such as more people than the new map has
// teams, fails with lobby_full and changes nothing. Settings and teams equal
// to the lobby's change nothing.
func (service *Service) ChangeSettings(ctx context.Context, principal domain.Principal, scope domain.Scope, request domain.ChangeSettingsRequest) (domain.Snapshot, error) {
	if err := validatePrincipal(principal); err != nil {
		return domain.Snapshot{}, err
	}
	if err := service.policy.ValidateScope(scope); err != nil {
		return domain.Snapshot{}, err
	}
	teams, err := domain.ValidateTeamDefinitions(request.Teams)
	if err != nil {
		return domain.Snapshot{}, err
	}
	settings, err := service.policy.ValidateGameSettings(ctx, scope.GameKey, scope.LobbyID, request.GameSettings, teams)
	if err != nil {
		return domain.Snapshot{}, err
	}
	aggregate, err := service.runMutation(ctx, scope, func(aggregate *domain.Aggregate, now time.Time) (domain.Change, error) {
		if err := requireWaitingHost(aggregate, principal); err != nil {
			return domain.Change{}, err
		}
		if sameSettings(aggregate, settings, teams) {
			return domain.Change{}, nil
		}
		// Every person stays and needs a team of their own; a seat stays
		// only on a team that the change keeps.
		remainingMembers := 0
		for _, member := range aggregate.Members {
			if member.Active && (!member.IsSeat() || keepsTeam(aggregate.Teams, member.TeamID, len(teams))) {
				remainingMembers++
			}
		}
		if remainingMembers > len(teams) {
			return domain.Change{}, domain.ErrLobbyFull
		}
		rebuilt, err := service.rebuildTeams(aggregate, teams)
		if err != nil {
			return domain.Change{}, err
		}
		for index := range aggregate.Members {
			member := &aggregate.Members[index]
			if member.IsSeat() {
				if member.Active && !keepsTeam(aggregate.Teams, member.TeamID, len(teams)) {
					removeSeat(member)
				}
				continue
			}
			if !keepsTeam(aggregate.Teams, member.TeamID, len(teams)) {
				member.TeamID = nil
			}
			member.Ready = false
		}
		aggregate.Teams = rebuilt
		aggregate.Lobby.Capacity = len(rebuilt)
		aggregate.Lobby.GameSettings = settings
		return domain.Change{PublicChanged: true}, nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	return service.snapshot(aggregate, principal.PlayerID)
}

// sameSettings reports whether the lobby already has these settings and
// these team names in this order.
func sameSettings(aggregate *domain.Aggregate, settings []byte, teams []domain.TeamDefinition) bool {
	if !bytes.Equal(aggregate.Lobby.GameSettings, settings) || len(aggregate.Teams) != len(teams) {
		return false
	}
	for index, team := range aggregate.Teams {
		if team.Name != teams[index].Name {
			return false
		}
	}
	return true
}

// keepsTeam reports whether a member on teamID keeps their team when the
// lobby's teams, in order, are rebuilt to teamCount teams. A member without a
// team keeps having none, so it counts as kept.
func keepsTeam(teams []domain.Team, teamID *string, teamCount int) bool {
	if teamID == nil {
		return true
	}
	for index, team := range teams {
		if team.ID == *teamID {
			return index < teamCount
		}
	}
	return false
}

// rebuildTeams returns the lobby's teams rebuilt by position for the new
// definitions: kept teams keep their ID and order and take the new name, and
// new teams get a new ID and the next order.
func (service *Service) rebuildTeams(aggregate *domain.Aggregate, definitions []domain.TeamDefinition) ([]domain.Team, error) {
	rebuilt := make([]domain.Team, len(definitions))
	for index, definition := range definitions {
		if index < len(aggregate.Teams) {
			team := aggregate.Teams[index]
			team.Name = definition.Name
			rebuilt[index] = team
			continue
		}
		teamID, err := service.identifiers.UUID()
		if err != nil {
			return nil, unavailable(err)
		}
		// definitions holds at least two teams, so a new team always follows
		// a kept one.
		rebuilt[index] = domain.Team{ID: teamID, LobbyID: aggregate.Lobby.ID, Name: definition.Name, Order: rebuilt[index-1].Order + 1}
	}
	return rebuilt, nil
}

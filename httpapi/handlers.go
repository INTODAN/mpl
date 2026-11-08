package httpapi

import (
	"context"
	"encoding/json"
	"islandBattles/lobby/domain"
)

type handlers struct {
	app           domain.Application
	authenticator domain.Authenticator
	issuer        domain.ConnectionTokenIssuer
}

func (handler handlers) session(ctx context.Context, input *CreateSessionInput) (*CreateSessionOutput, error) {
	result, err := handler.authenticator.CreateGuest(ctx, domain.CreateGuestRequest{DisplayName: input.Body.DisplayName})
	if err != nil {
		return nil, problemFor(err)
	}
	return &CreateSessionOutput{Body: GuestSession{PlayerID: result.PlayerID, AccessToken: result.AccessToken, ExpiresAt: result.ExpiresAt}}, nil
}
func (handler handlers) token(ctx context.Context, _ *ConnectionTokenInput) (*ConnectionTokenOutput, error) {
	result, err := handler.issuer.IssueConnectionToken(ctx, sessionFromContext(ctx))
	if err != nil {
		return nil, problemFor(err)
	}
	return &ConnectionTokenOutput{Body: ConnectionToken{Token: result.Token, ExpiresAt: result.ExpiresAt, WebsocketURL: result.WebsocketURL}}, nil
}
func sessionFromContext(ctx context.Context) domain.SessionVerification {
	verification, _ := ctx.Value(sessionContextKey{}).(domain.SessionVerification)
	return verification
}
func (handler handlers) create(ctx context.Context, input *CreateLobbyInput) (*CreateLobbyOutput, error) {
	body := input.Body
	teams := make([]domain.TeamDefinition, len(body.Teams))
	for i, team := range body.Teams {
		teams[i] = domain.TeamDefinition{Name: team.Name, Capacity: team.Capacity}
	}
	result, err := handler.app.Create(ctx, principalFromContext(ctx), domain.CreateRequest{RequestID: body.RequestID, GameKey: body.GameKey, Name: body.Name, Capacity: body.Capacity, Teams: teams, ClientInstanceID: body.ClientInstanceID, GameSettings: json.RawMessage(body.GameSettings)})
	if err != nil {
		return nil, problemFor(err)
	}
	return &CreateLobbyOutput{Body: snapshotFromDomain(result)}, nil
}
func (handler handlers) join(ctx context.Context, input *JoinLobbyInput) (*JoinLobbyOutput, error) {
	body := input.Body
	result, err := handler.app.Join(ctx, principalFromContext(ctx), domain.JoinRequest{GameKey: body.GameKey, JoinCode: body.JoinCode, ClientInstanceID: body.ClientInstanceID})
	if err != nil {
		return nil, problemFor(err)
	}
	return &JoinLobbyOutput{Body: snapshotFromDomain(result)}, nil
}
func scope(input ScopeInput) domain.Scope {
	return domain.Scope{GameKey: input.GameKey, LobbyID: input.LobbyID}
}
func (handler handlers) snapshot(ctx context.Context, input *ScopeInput) (*SnapshotOutput, error) {
	result, err := handler.app.Snapshot(ctx, principalFromContext(ctx), scope(*input))
	if err != nil {
		return nil, problemFor(err)
	}
	return &SnapshotOutput{Body: snapshotFromDomain(result)}, nil
}
func (handler handlers) team(ctx context.Context, input *SelectTeamInput) (*SnapshotOutput, error) {
	result, err := handler.app.SelectTeam(ctx, principalFromContext(ctx), scope(input.ScopeInput), domain.SelectTeamRequest{TeamID: input.Body.TeamID.Value})
	if err != nil {
		return nil, problemFor(err)
	}
	return &SnapshotOutput{Body: snapshotFromDomain(result)}, nil
}
func (handler handlers) ready(ctx context.Context, input *ReadyInput) (*SnapshotOutput, error) {
	result, err := handler.app.SetReady(ctx, principalFromContext(ctx), scope(input.ScopeInput), domain.ReadyRequest{Ready: input.Body.Ready})
	if err != nil {
		return nil, problemFor(err)
	}
	return &SnapshotOutput{Body: snapshotFromDomain(result)}, nil
}
func (handler handlers) start(ctx context.Context, input *StartInput) (*SnapshotOutput, error) {
	result, err := handler.app.Start(ctx, principalFromContext(ctx), scope(input.ScopeInput))
	if err != nil {
		return nil, problemFor(err)
	}
	return &SnapshotOutput{Body: snapshotFromDomain(result)}, nil
}
func (handler handlers) addSeat(ctx context.Context, input *AddSeatInput) (*SnapshotOutput, error) {
	result, err := handler.app.AddSeat(ctx, principalFromContext(ctx), scope(input.ScopeInput), domain.AddSeatRequest{TeamID: input.Body.TeamID, SeatSettings: json.RawMessage(input.Body.SeatSettings)})
	if err != nil {
		return nil, problemFor(err)
	}
	return &SnapshotOutput{Body: snapshotFromDomain(result)}, nil
}
func (handler handlers) removeSeat(ctx context.Context, input *RemoveSeatInput) (*SnapshotOutput, error) {
	result, err := handler.app.RemoveSeat(ctx, principalFromContext(ctx), scope(input.ScopeInput), input.SeatID)
	if err != nil {
		return nil, problemFor(err)
	}
	return &SnapshotOutput{Body: snapshotFromDomain(result)}, nil
}
func (handler handlers) leave(ctx context.Context, input *LeaveInput) (*EmptyOutput, error) {
	if err := handler.app.Leave(ctx, principalFromContext(ctx), scope(input.ScopeInput)); err != nil {
		return nil, problemFor(err)
	}
	return &EmptyOutput{}, nil
}
func (handler handlers) heartbeat(ctx context.Context, input *HeartbeatInput) (*HeartbeatOutput, error) {
	body := input.Body
	result, err := handler.app.Heartbeat(ctx, principalFromContext(ctx), scope(input.ScopeInput), domain.HeartbeatRequest{ClientInstanceID: body.ClientInstanceID, MembershipGeneration: body.MembershipGeneration})
	if err != nil {
		return nil, problemFor(err)
	}
	return &HeartbeatOutput{Body: HeartbeatResult{Version: result.Version, MembershipGeneration: result.MembershipGeneration, LeaseExpiresAt: result.LeaseExpiresAt, DisconnectedUntil: result.DisconnectedUntil}}, nil
}
func (handler handlers) release(ctx context.Context, input *ReleaseLeaseInput) (*EmptyOutput, error) {
	if err := handler.app.ReleaseLease(ctx, principalFromContext(ctx), scope(input.ScopeInput), domain.ReleaseLeaseRequest{ClientInstanceID: input.ClientInstanceID, MembershipGeneration: input.MembershipGeneration}); err != nil {
		return nil, problemFor(err)
	}
	return &EmptyOutput{}, nil
}

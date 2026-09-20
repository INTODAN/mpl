package realtime

import (
	"context"
	"encoding/json"
	"islandBattles/lobby"

	"github.com/centrifugal/gocent/v3"
)

type Transport struct{ client *gocent.Client }

func NewTransport(client *gocent.Client) (*Transport, error) {
	if client == nil {
		return nil, lobby.ErrInvalidInput
	}
	return &Transport{client: client}, nil
}

func (transport *Transport) PublishInvalidation(ctx context.Context, channel string, event lobby.Invalidation) error {
	expected, err := lobby.RoomChannel(lobby.Scope{GameKey: event.GameKey, LobbyID: event.LobbyID})
	if err != nil || expected != channel || event.SchemaVersion != lobby.InvalidationSchemaVersion || event.Type != lobby.InvalidationType || event.Version <= 0 || lobby.ValidateUUID(event.EventID) != nil {
		return lobby.ErrInvalidInput
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = transport.client.Publish(ctx, channel, payload)
	return err
}

func (transport *Transport) Unsubscribe(ctx context.Context, playerID, channelName string) error {
	channel, err := lobby.ParseChannel(channelName)
	if err != nil || channel.Namespace != lobby.ChannelLobby || lobby.ValidateUUID(playerID) != nil {
		return lobby.ErrInvalidInput
	}
	return transport.client.Unsubscribe(ctx, channelName, playerID)
}

var _ lobby.RealtimeTransport = (*Transport)(nil)
var _ lobby.SubscriptionAuthorizer = (*Authorizer)(nil)

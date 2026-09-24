package realtime

import (
	"context"
	"encoding/json"
	"islandBattles/lobby/domain"

	"github.com/centrifugal/gocent/v3"
)

type Transport struct{ client *gocent.Client }

func NewTransport(client *gocent.Client) (*Transport, error) {
	if client == nil {
		return nil, domain.ErrInvalidInput
	}
	return &Transport{client: client}, nil
}

func (transport *Transport) PublishInvalidation(ctx context.Context, channel string, event domain.Invalidation) error {
	expected, err := domain.RoomChannel(domain.Scope{GameKey: event.GameKey, LobbyID: event.LobbyID})
	if err != nil || expected != channel || event.SchemaVersion != domain.InvalidationSchemaVersion || event.Type != domain.InvalidationType || event.Version <= 0 || domain.ValidateUUID(event.EventID) != nil {
		return domain.ErrInvalidInput
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = transport.client.Publish(ctx, channel, payload)
	return err
}

func (transport *Transport) Unsubscribe(ctx context.Context, playerID, channelName string) error {
	channel, err := domain.ParseChannel(channelName)
	if err != nil || channel.Namespace != domain.ChannelLobby || domain.ValidateUUID(playerID) != nil {
		return domain.ErrInvalidInput
	}
	return transport.client.Unsubscribe(ctx, channelName, playerID)
}

var _ domain.RealtimeTransport = (*Transport)(nil)
var _ domain.SubscriptionAuthorizer = (*Authorizer)(nil)

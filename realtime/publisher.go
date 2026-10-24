package realtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"islandBattles/lobby/domain"

	"github.com/centrifugal/gocent/v3"
)

// Publication is one message for one Centrifugo channel.
type Publication struct {
	Channel string
	Data    []byte
}

// PushRetry bounds the time a request spends pushing after its commit.
type PushRetry struct {
	Attempts       int
	AttemptTimeout time.Duration
	// Pause precedes the second attempt and doubles before each later one.
	Pause time.Duration
}

// DefaultPushRetry makes three attempts within about a second.
func DefaultPushRetry() PushRetry {
	return PushRetry{Attempts: 3, AttemptTimeout: 300 * time.Millisecond, Pause: 100 * time.Millisecond}
}

// Publisher pushes straight to Centrifugo's HTTP API after a commit. It sends
// all of a call's commands in one API request and retries the failed ones
// briefly. What still fails is logged and returned; it never undoes the commit,
// and clients recover by re-reading their state over HTTP. Nothing is queued.
type Publisher struct {
	client *gocent.Client
	logger *slog.Logger
	retry  PushRetry
}

var _ domain.ChangePublisher = (*Publisher)(nil)
var _ domain.SubscriptionAuthorizer = (*Authorizer)(nil)

func NewPublisher(client *gocent.Client, logger *slog.Logger, retry PushRetry) (*Publisher, error) {
	if client == nil || logger == nil || retry.Attempts < 1 || retry.AttemptTimeout <= 0 || retry.Pause < 0 {
		return nil, domain.ErrInvalidInput
	}
	return &Publisher{client: client, logger: logger, retry: retry}, nil
}

// Publish delivers each publication to its channel. Games use it for their own
// events, such as a turn result on each player's personal channel.
func (publisher *Publisher) Publish(ctx context.Context, publications ...Publication) error {
	commands := make([]command, 0, len(publications))
	for _, publication := range publications {
		commands = append(commands, command{channel: publication.Channel, data: publication.Data, idempotencyKey: rand.Text()})
	}
	return publisher.push(ctx, commands)
}

// PublishChange announces a committed room version to the room channel and
// unsubscribes each revoked player from it. Before every attempt, stillDue
// confirms each unsubscribe, which is skipped once the player has rejoined.
func (publisher *Publisher) PublishChange(ctx context.Context, room domain.Lobby, change domain.Change, stillDue domain.RevocationCheck) error {
	if stillDue == nil {
		return domain.ErrInvalidInput
	}
	commands, err := roomCommands(room, change, stillDue)
	if err != nil {
		publisher.logger.Error("Lobby change could not be pushed to Centrifugo", "lobby_id", room.ID, "error", err)
		return err
	}
	return publisher.push(ctx, commands)
}

func roomCommands(room domain.Lobby, change domain.Change, stillDue domain.RevocationCheck) ([]command, error) {
	channel, err := domain.RoomChannel(room.Scope())
	if err != nil {
		return nil, err
	}
	var commands []command
	if change.PublicChanged {
		if room.Version <= 0 {
			return nil, domain.ErrInvalidInput
		}
		eventID, err := domain.CryptoIdentifiers{}.UUID()
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(domain.Invalidation{SchemaVersion: domain.InvalidationSchemaVersion, EventID: eventID, GameKey: room.GameKey, LobbyID: room.ID, Version: room.Version, Type: domain.InvalidationType})
		if err != nil {
			return nil, err
		}
		commands = append(commands, command{channel: channel, data: data, idempotencyKey: eventID})
	}
	for _, revocation := range change.Revocations {
		if revocation.Scope != room.Scope() || domain.ValidateUUID(revocation.PlayerID) != nil {
			return nil, domain.ErrInvalidInput
		}
		// Unsubscribing by user, without a client ID, covers every device.
		commands = append(commands, command{channel: channel, user: revocation.PlayerID, revocation: revocation, stillDue: stillDue})
	}
	return commands, nil
}

// command is a publication, or a server-side unsubscribe when user is set.
type command struct {
	channel        string
	data           []byte
	user           string
	idempotencyKey string
	revocation     domain.RevocationIntent
	stillDue       domain.RevocationCheck
}

func (publisher *Publisher) push(ctx context.Context, commands []command) error {
	// The change is already committed, so a caller hanging up must not cancel
	// delivery to everyone else. PushRetry bounds the time instead.
	ctx = context.WithoutCancel(ctx)
	pause := publisher.retry.Pause
	var err error
	for attempt := 1; ; attempt++ {
		commands, err = publisher.attempt(ctx, commands)
		if len(commands) == 0 {
			return nil
		}
		if attempt == publisher.retry.Attempts {
			break
		}
		time.Sleep(pause)
		pause *= 2
	}
	publisher.logger.Error("Centrifugo push failed; clients recover by re-reading over HTTP",
		"failed_commands", len(commands), "channel", commands[0].channel, "attempts", publisher.retry.Attempts, "error", err)
	return err
}

// attempt sends the commands in one request and returns the ones that failed.
// Publications keep their idempotency key across attempts, so a retry after a
// lost reply does not publish twice. An unsubscribe is rechecked first and
// dropped once it no longer applies.
func (publisher *Publisher) attempt(ctx context.Context, commands []command) ([]command, error) {
	var failed, sent []command
	var failures []error
	pipe := publisher.client.Pipe()
	for _, command := range commands {
		var err error
		if command.user != "" {
			due, checkError := command.stillDue(ctx, command.revocation)
			if checkError != nil {
				failed, failures = append(failed, command), append(failures, checkError)
				continue
			}
			if !due {
				continue
			}
			err = pipe.AddUnsubscribe(command.channel, command.user)
		} else {
			err = pipe.AddPublish(command.channel, command.data, gocent.WithIdempotencyKey(command.idempotencyKey))
		}
		if err != nil {
			return commands, err
		}
		sent = append(sent, command)
	}
	if len(sent) == 0 {
		return failed, errors.Join(failures...)
	}
	attemptContext, cancel := context.WithTimeout(ctx, publisher.retry.AttemptTimeout)
	defer cancel()
	replies, err := publisher.client.SendPipe(attemptContext, pipe)
	if err != nil {
		return append(failed, sent...), errors.Join(append(failures, err)...)
	}
	for index, reply := range replies {
		if reply.Error != nil {
			failed = append(failed, sent[index])
			failures = append(failures, reply.Error)
		}
	}
	return failed, errors.Join(failures...)
}

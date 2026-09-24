package delivery_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"islandBattles/lobby/delivery"
	"islandBattles/lobby/domain"
)

type clock struct{ now time.Time }

func (clock clock) Now() time.Time { return clock.now }

type jobs struct {
	sync.Mutex
	queue      []domain.DeliveryJob
	acks       []string
	retries    []domain.RetryRequest
	state      *domain.MembershipState
	stateError error
	ackError   error
}

func (store *jobs) Claim(ctx context.Context, request domain.ClaimRequest) ([]domain.DeliveryJob, error) {
	store.Lock()
	defer store.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := append([]domain.DeliveryJob{}, store.queue...)
	store.queue = nil
	return result, nil
}
func (store *jobs) Ack(_ context.Context, id, token string, _ time.Time) (bool, error) {
	store.Lock()
	defer store.Unlock()
	store.acks = append(store.acks, id+":"+token)
	return store.ackError == nil, store.ackError
}
func (store *jobs) Retry(_ context.Context, request domain.RetryRequest) (bool, error) {
	store.Lock()
	defer store.Unlock()
	store.retries = append(store.retries, request)
	return true, nil
}
func (store *jobs) CurrentMembership(context.Context, domain.Scope, string) (*domain.MembershipState, error) {
	return store.state, store.stateError
}

type transport struct {
	sync.Mutex
	publications []domain.Invalidation
	revocations  []string
	fail         error
	block        bool
}

func (publisher *transport) PublishInvalidation(ctx context.Context, _ string, event domain.Invalidation) error {
	if publisher.block {
		<-ctx.Done()
		return ctx.Err()
	}
	publisher.Lock()
	defer publisher.Unlock()
	publisher.publications = append(publisher.publications, event)
	return publisher.fail
}
func (publisher *transport) Unsubscribe(_ context.Context, player, channel string) error {
	publisher.Lock()
	defer publisher.Unlock()
	publisher.revocations = append(publisher.revocations, player+" "+channel)
	return publisher.fail
}

func eventJob() domain.DeliveryJob {
	return domain.DeliveryJob{ID: "job", Kind: domain.JobInvalidation, ClaimToken: "claim", Attempts: 1, Invalidation: &domain.Invalidation{SchemaVersion: 1, EventID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", GameKey: "island-battles", LobbyID: "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb", Version: 5, Type: "lobby.changed"}}
}

func TestFailedDeliveryPersistsRetryWithoutChangingEvent(t *testing.T) {
	now := time.Now().UTC()
	job := eventJob()
	store := &jobs{queue: []domain.DeliveryJob{job}}
	publisher := &transport{fail: errors.New("secret server diagnostic")}
	worker, err := delivery.NewWorker(store, publisher, clock{now}, delivery.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("failure swallowed")
	}
	if len(store.acks) != 0 || len(store.retries) != 1 || store.retries[0].ClaimToken != "claim" || !store.retries[0].NextAttemptAt.Equal(now.Add(time.Second)) || store.retries[0].ErrorSummary == publisher.fail.Error() {
		t.Fatalf("incorrect retry: %+v", store)
	}
	publisher.fail = nil
	store.queue = []domain.DeliveryJob{job}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.acks) != 1 || len(publisher.publications) != 2 || publisher.publications[0] != publisher.publications[1] {
		t.Fatal("retry changed event or not acknowledged")
	}
}

func TestStaleRevocationDoesNotUnsubscribeNewMembership(t *testing.T) {
	for _, test := range []struct {
		active      bool
		generation  int64
		unsubscribe bool
	}{{true, 2, false}, {true, 1, true}, {false, 2, true}} {
		store := &jobs{state: &domain.MembershipState{Generation: test.generation, Active: test.active}, queue: []domain.DeliveryJob{{ID: "revoke", Kind: domain.JobRevocation, ClaimToken: "claim", Revocation: &domain.Revocation{PlayerID: "aaaaaaaa-aaaa-4aaa-aaaa-aaaaaaaaaaaa", Scope: domain.Scope{GameKey: "island-battles", LobbyID: "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbbb"}, Generation: 1}}}}
		publisher := &transport{}
		worker, _ := delivery.NewWorker(store, publisher, clock{time.Now()}, delivery.DefaultConfig())
		if err := worker.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if (len(publisher.revocations) > 0) != test.unsubscribe || len(store.acks) != 1 {
			t.Fatalf("incorrect generation fence: %+v", test)
		}
	}
}

func TestDeliveryFailureDoesNotStarveOtherJobs(t *testing.T) {
	valid := eventJob()
	invalid := domain.DeliveryJob{ID: "invalid", Kind: "unknown", ClaimToken: "bad", Attempts: 1000}
	store := &jobs{queue: []domain.DeliveryJob{invalid, valid}}
	publisher := &transport{}
	now := time.Now()
	worker, _ := delivery.NewWorker(store, publisher, clock{now}, delivery.DefaultConfig())
	if err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("invalid job not reported")
	}
	if len(store.acks) != 1 || store.acks[0] != "job:claim" || len(store.retries) != 1 || !store.retries[0].NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatal("batch starvation/backoff overflow")
	}
}

func TestDeliveryCancellationAndLostAcknowledgement(t *testing.T) {
	store := &jobs{queue: []domain.DeliveryJob{eventJob()}, ackError: errors.New("connection lost")}
	publisher := &transport{}
	worker, _ := delivery.NewWorker(store, publisher, clock{time.Now()}, delivery.DefaultConfig())
	if err := worker.RunOnce(context.Background()); err == nil {
		t.Fatal("lost ack swallowed")
	}
	if len(store.retries) != 0 || len(publisher.publications) != 1 {
		t.Fatal("ack failure repeated original mutation")
	}
	store.queue = []domain.DeliveryJob{eventJob()}
	publisher.block = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestDeliveryRejectsInvalidConfig(t *testing.T) {
	config := delivery.DefaultConfig()
	config.ClaimDuration = config.NetworkTimeout
	if _, err := delivery.NewWorker(&jobs{}, &transport{}, clock{}, config); err == nil {
		t.Fatal("unsafe claim lifetime accepted")
	}
}

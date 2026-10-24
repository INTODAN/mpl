package application

import (
	"context"

	"islandBattles/lobby/domain"
)

// PublishingStore pushes every committed change to subscribers before Create or
// Transact returns, so the request that made a change also delivers it. A push
// never undoes its commit: the publisher retries briefly and logs what fails.
type PublishingStore struct {
	domain.Store
	publisher domain.ChangePublisher
}

func NewPublishingStore(store domain.Store, publisher domain.ChangePublisher) (*PublishingStore, error) {
	if store == nil || publisher == nil {
		return nil, domain.ErrInvalidInput
	}
	return &PublishingStore{Store: store, publisher: publisher}, nil
}

// Create announces version 1 of a new room. An idempotent replay announces the
// original version again, which clients ignore.
func (store *PublishingStore) Create(ctx context.Context, principal domain.Principal, request domain.CreateRequest, aggregate domain.Aggregate) (domain.Snapshot, error) {
	snapshot, err := store.Store.Create(ctx, principal, request, aggregate)
	if err != nil {
		return domain.Snapshot{}, err
	}
	_ = store.publisher.PublishChange(ctx, snapshot.Lobby, domain.Change{PublicChanged: true}, store.revocationStillDue)
	return snapshot, nil
}

func (store *PublishingStore) Transact(ctx context.Context, scope domain.Scope, mutation domain.Mutation) (domain.Aggregate, error) {
	if mutation == nil {
		return domain.Aggregate{}, domain.ErrInvalidInput
	}
	var change domain.Change
	committed, err := store.Store.Transact(ctx, scope, func(aggregate *domain.Aggregate) (domain.Change, error) {
		var err error
		change, err = mutation(aggregate)
		return change, err
	})
	if err != nil {
		return domain.Aggregate{}, err
	}
	change = domain.MergeChanges(change)
	change.Revocations = supersededRevocationsRemoved(committed, change.Revocations)
	if change.PublicChanged || len(change.Revocations) > 0 {
		_ = store.publisher.PublishChange(ctx, committed.Lobby, change, store.revocationStillDue)
	}
	return committed, nil
}

// supersededRevocationsRemoved drops the revocations that the committed
// aggregate already supersedes, as when one transaction expires a membership
// and the player joins again.
func supersededRevocationsRemoved(committed domain.Aggregate, revocations []domain.RevocationIntent) []domain.RevocationIntent {
	kept := make([]domain.RevocationIntent, 0, len(revocations))
	for _, revocation := range revocations {
		if revocationApplies(committed, revocation) {
			kept = append(kept, revocation)
		}
	}
	return kept
}

// revocationStillDue re-reads the room before each unsubscribe attempt. The
// player may rejoin and subscribe while a push waits between retries, and
// unsubscribing them then would drop their new subscription.
func (store *PublishingStore) revocationStillDue(ctx context.Context, revocation domain.RevocationIntent) (bool, error) {
	current, err := store.Read(ctx, revocation.Scope)
	if err != nil {
		return false, err
	}
	return revocationApplies(current, revocation), nil
}

func revocationApplies(aggregate domain.Aggregate, revocation domain.RevocationIntent) bool {
	member := domain.FindMember(&aggregate, revocation.PlayerID)
	return member == nil || !member.Active || member.Generation <= revocation.Generation
}

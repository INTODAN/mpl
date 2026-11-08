package application_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/postgres"
	"islandBattles/lobby/realtime"
)

func (fixture *serviceFixture) watch(t *testing.T, principal domain.Principal) domain.WatchAccess {
	t.Helper()
	access, err := fixture.service.Watch(context.Background(), principal, domain.WatchRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode})
	if err != nil {
		t.Fatal(err)
	}
	return access
}

// watchProxy returns Centrifugo's subscribe proxy call for the watch channel.
func (fixture *serviceFixture) watchProxy(t *testing.T) func(domain.Principal) error {
	t.Helper()
	identities, err := postgres.NewIdentityStore(fixture.db)
	if err != nil {
		t.Fatal(err)
	}
	authorizer, err := realtime.NewAuthorizer(identities, fixture.store, fixture.clock, fixture.policy)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := domain.WatchChannel(fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	return func(principal domain.Principal) error {
		_, err := authorizer.AuthorizeSubscription(context.Background(), principal.PlayerID, channel)
		return err
	}
}

func TestAWatcherTakesNoSeatAndIsAnnounced(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	ctx := context.Background()
	invalidations := fixture.pushed(invalidationPushes)
	watcher := fixture.player(t, "Robin")
	access := fixture.watch(t, watcher)
	if access.Scope != fixture.scope || access.Channel != "watch:island-battles:"+fixture.scope.LobbyID || access.Member {
		t.Fatalf("watch access = %#v", access)
	}
	if fixture.pushed(invalidationPushes) != invalidations+1 {
		t.Fatal("a new watcher published no lobby.changed")
	}
	// Watching again finds the same row and announces nothing.
	if again := fixture.watch(t, watcher); again != access || fixture.pushed(invalidationPushes) != invalidations+1 {
		t.Fatalf("watching again = %#v", again)
	}
	snapshot, err := fixture.service.Snapshot(ctx, fixture.host, fixture.scope)
	if err != nil || snapshot.WatcherCount != 1 || len(snapshot.Members) != 1 {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}
	// The watcher holds no seat: a guest fills the lobby of two, and a
	// second watcher still watches.
	guest, _ := fixture.join(t, fixture.player(t, "Guest"))
	if len(guest.Members) != 2 || guest.WatcherCount != 1 {
		t.Fatalf("guest snapshot = %#v", guest)
	}
	fixture.clock.Set(fixture.origin.Add(time.Second))
	fixture.watch(t, fixture.player(t, "Sam"))
	// A watcher holds no lease and isn't a member: they can't read the
	// lobby, heartbeat or get ready.
	if _, err := fixture.service.Snapshot(ctx, watcher, fixture.scope); err == nil {
		t.Fatal("a watcher read the members' snapshot")
	}
	if _, err := fixture.service.Heartbeat(ctx, watcher, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: integrationID(t), MembershipGeneration: 1}); err == nil {
		t.Fatal("a watcher heartbeat")
	}
	aggregate := fixture.read(t)
	for _, lease := range aggregate.Leases {
		if lease.PlayerID == watcher.PlayerID {
			t.Fatal("a watcher holds a lease")
		}
	}
	if len(aggregate.Watchers) != 2 || domain.WatcherCount(aggregate.Watchers) != 2 {
		t.Fatalf("watchers = %#v", aggregate.Watchers)
	}
	// Members list the watchers; a watcher can't.
	listed, err := fixture.service.Watchers(ctx, fixture.host, fixture.scope)
	if err != nil || len(listed) != 2 || listed[0].DisplayName != "Robin" || listed[1].DisplayName != "Sam" {
		t.Fatalf("watchers = %#v, %v", listed, err)
	}
	if _, err := fixture.service.Watchers(ctx, watcher, fixture.scope); err == nil {
		t.Fatal("a watcher listed the watchers")
	}
}

func TestWatchingDoesNotKeepALobbyConnectedOrDelayItsExpiry(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	fixture.clock.Set(fixture.origin.Add(10 * time.Second))
	fixture.watch(t, fixture.player(t, "Robin"))
	if got := fixture.lastConnectedAt(t); !got.Equal(fixture.origin) {
		t.Fatalf("watching marked the lobby connected at %v", got)
	}
	// The host's lease ends and its grace passes while the watcher watches:
	// the host leaves and the waiting lobby closes, as it would without them.
	fixture.clock.Set(fixture.origin.Add(fixture.policy.LeaseDuration + fixture.policy.GraceDuration + time.Second))
	fixture.watch(t, fixture.player(t, "Sam"))
	aggregate := fixture.read(t)
	if aggregate.Lobby.Phase != domain.PhaseClosed || memberFor(t, aggregate, fixture.host.PlayerID).Active {
		t.Fatalf("lobby = %#v", aggregate.Lobby)
	}
	// A closed lobby can still be watched.
	if domain.WatcherCount(aggregate.Watchers) != 2 {
		t.Fatalf("watchers of the closed lobby = %#v", aggregate.Watchers)
	}
}

func TestWatchersNeverJoinTheStartRoster(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	ctx := context.Background()
	watcher := fixture.player(t, "Robin")
	fixture.watch(t, watcher)
	fixture.readyPair(t)
	started, err := fixture.service.Start(ctx, fixture.host, fixture.scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(started.StartRoster) != 2 {
		t.Fatalf("roster = %#v", started.StartRoster)
	}
	for _, entry := range started.StartRoster {
		if entry.PlayerID == watcher.PlayerID {
			t.Fatal("a watcher joined the start roster")
		}
	}
	// A started lobby can be watched, and a watcher can't take a seat.
	fixture.watch(t, fixture.player(t, "Sam"))
	if _, err := fixture.service.Join(ctx, watcher, domain.JoinRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode, ClientInstanceID: integrationID(t)}); err == nil {
		t.Fatal("a watcher joined a started lobby")
	}
}

func TestAMemberWatchingKeepsTheirMemberAccess(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	invalidations := fixture.pushed(invalidationPushes)
	access := fixture.watch(t, fixture.host)
	if !access.Member || access.Channel != "watch:island-battles:"+fixture.scope.LobbyID {
		t.Fatalf("member's watch access = %#v", access)
	}
	if aggregate := fixture.read(t); len(aggregate.Watchers) != 0 || fixture.pushed(invalidationPushes) != invalidations {
		t.Fatalf("a member became a watcher: %#v", aggregate.Watchers)
	}
}

func TestWatchRefusesWrongCodesAndOtherGamesCodes(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	ctx := context.Background()
	watcher := fixture.player(t, "Robin")
	code := fixture.initial.Lobby.JoinCode
	wrong := "ABCDEFGH"
	if wrong == code {
		wrong = "HGFEDCBA"
	}
	_, err := fixture.service.Watch(ctx, watcher, domain.WatchRequest{GameKey: "island-battles", JoinCode: wrong})
	requireDomainError(t, err, domain.ErrNotFound)
	_, err = fixture.service.Watch(ctx, watcher, domain.WatchRequest{GameKey: "quiz-demo", JoinCode: code})
	requireDomainError(t, err, domain.ErrNotFound)
	_, err = fixture.service.Watch(ctx, watcher, domain.WatchRequest{GameKey: "island-battles", JoinCode: "short"})
	requireDomainError(t, err, domain.ErrInvalidInput)
	_, err = fixture.service.Watch(ctx, watcher, domain.WatchRequest{GameKey: "not-registered", JoinCode: code})
	requireDomainError(t, err, domain.ErrInvalidInput)
	if len(fixture.read(t).Watchers) != 0 {
		t.Fatal("a refused code added a watcher")
	}
}

func TestTheHostKicksAWatcherWhoCannotComeBack(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	ctx := context.Background()
	watcher := fixture.player(t, "Robin")
	fixture.watch(t, watcher)
	guest := fixture.player(t, "Guest")
	fixture.join(t, guest)
	proxy := fixture.watchProxy(t)
	for _, principal := range []domain.Principal{watcher, fixture.host, guest} {
		if err := proxy(principal); err != nil {
			t.Fatalf("watch channel refused %s: %v", principal.DisplayName, err)
		}
	}
	stranger := fixture.player(t, "Stranger")
	requireDomainError(t, proxy(stranger), domain.ErrPermissionDenied)
	// The room channel stays the members'.
	requireDomainError(t, fixture.proxy(t, fixture.store)(watcher), domain.ErrPermissionDenied)

	requireDomainError(t, fixture.service.KickWatcher(ctx, guest, fixture.scope, watcher.PlayerID), domain.ErrNotHost)
	requireDomainError(t, fixture.service.KickWatcher(ctx, watcher, fixture.scope, watcher.PlayerID), domain.ErrNotFound)
	requireDomainError(t, fixture.service.KickWatcher(ctx, fixture.host, fixture.scope, stranger.PlayerID), domain.ErrNotFound)
	revocations, invalidations := fixture.pushed(revocationPushes), fixture.pushed(invalidationPushes)
	if err := fixture.service.KickWatcher(ctx, fixture.host, fixture.scope, watcher.PlayerID); err != nil {
		t.Fatal(err)
	}
	if fixture.pushed(revocationPushes) != revocations+1 || fixture.pushed(invalidationPushes) != invalidations+1 {
		t.Fatal("kicking pushed no unsubscribe and lobby.changed")
	}
	requireDomainError(t, proxy(watcher), domain.ErrPermissionDenied)
	_, err := fixture.service.Watch(ctx, watcher, domain.WatchRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode})
	requireDomainError(t, err, domain.ErrWatcherKicked)
	requireDomainError(t, fixture.service.AuthorizeWatch(ctx, watcher, fixture.scope), domain.ErrWatcherKicked)
	// Leaving doesn't clear a kick, and kicking again changes nothing.
	if err := fixture.service.Unwatch(ctx, watcher, fixture.scope); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.KickWatcher(ctx, fixture.host, fixture.scope, watcher.PlayerID); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Watch(ctx, watcher, domain.WatchRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode})
	requireDomainError(t, err, domain.ErrWatcherKicked)
	snapshot, err := fixture.service.Snapshot(ctx, fixture.host, fixture.scope)
	if err != nil || snapshot.WatcherCount != 0 {
		t.Fatalf("watcher count after the kick = %d, %v", snapshot.WatcherCount, err)
	}
	if listed, err := fixture.service.Watchers(ctx, fixture.host, fixture.scope); err != nil || len(listed) != 0 {
		t.Fatalf("watchers after the kick = %#v, %v", listed, err)
	}
	// A kicked row can't be removed by a mutation either.
	_, err = fixture.store.Transact(ctx, fixture.scope, func(aggregate *domain.Aggregate) (domain.Change, error) {
		aggregate.Watchers = nil
		return domain.Change{PublicChanged: true}, nil
	})
	requireDomainError(t, err, domain.ErrInvalidInput)
}

func TestAWatcherWhoLeavesCanWatchAgain(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	ctx := context.Background()
	watcher := fixture.player(t, "Robin")
	fixture.watch(t, watcher)
	revocations := fixture.pushed(revocationPushes)
	if err := fixture.service.Unwatch(ctx, watcher, fixture.scope); err != nil {
		t.Fatal(err)
	}
	if fixture.pushed(revocationPushes) != revocations+1 || len(fixture.read(t).Watchers) != 0 {
		t.Fatal("leaving kept the row or pushed no unsubscribe")
	}
	requireDomainError(t, fixture.service.Unwatch(ctx, watcher, fixture.scope), domain.ErrNotFound)
	requireDomainError(t, fixture.service.AuthorizeWatch(ctx, watcher, fixture.scope), domain.ErrNotFound)
	fixture.watch(t, watcher)
	if err := fixture.service.AuthorizeWatch(ctx, watcher, fixture.scope); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.AuthorizeWatch(ctx, fixture.host, fixture.scope); err != nil {
		t.Fatalf("a member can't read what watchers read: %v", err)
	}
}

func TestRetentionDeletesALobbyWithItsWatchers(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	watcher := fixture.player(t, "Robin")
	fixture.watch(t, watcher)
	fixture.clock.Set(fixture.origin.Add(48 * time.Hour))
	fixture.retention(t, fixture.policy).RunIfDue(context.Background())
	if fixture.roomExists(t, fixture.scope.LobbyID) {
		t.Fatal("a watched lobby outlived its retention")
	}
	var rows int64
	if err := fixture.db.Model(&postgres.WatcherRecord{}).Where(&postgres.WatcherRecord{LobbyID: fixture.scope.LobbyID}).Count(&rows).Error; err != nil || rows != 0 {
		t.Fatalf("watcher rows = %d, %v", rows, err)
	}
}

func TestALobbyHasAtMostMaxWatchersPerLobbyWatchers(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	ctx := context.Background()
	request := domain.WatchRequest{GameKey: fixture.scope.GameKey, JoinCode: fixture.initial.Lobby.JoinCode}
	watchers := make([]domain.Principal, domain.MaxWatchersPerLobby)
	for index := range watchers {
		watchers[index] = fixture.player(t, "Watcher")
		fixture.watch(t, watchers[index])
	}
	late := fixture.player(t, "Late")
	_, err := fixture.service.Watch(ctx, late, request)
	requireDomainError(t, err, domain.ErrWatchersFull)
	if len(fixture.read(t).Watchers) != domain.MaxWatchersPerLobby {
		t.Fatal("a refused watcher got a row")
	}
	// A watcher who already has a row, and a member, are not refused.
	fixture.watch(t, watchers[0])
	if access := fixture.watch(t, fixture.host); !access.Member {
		t.Fatalf("member's access = %#v", access)
	}
	// Kicked and departed watchers free their place.
	if err := fixture.service.KickWatcher(ctx, fixture.host, fixture.scope, watchers[1].PlayerID); err != nil {
		t.Fatal(err)
	}
	fixture.watch(t, late)
	_, err = fixture.service.Watch(ctx, fixture.player(t, "Later"), request)
	requireDomainError(t, err, domain.ErrWatchersFull)
	if err := fixture.service.Unwatch(ctx, watchers[2], fixture.scope); err != nil {
		t.Fatal(err)
	}
	fixture.watch(t, fixture.player(t, "Later"))
	snapshot, err := fixture.service.Snapshot(ctx, fixture.host, fixture.scope)
	if err != nil || snapshot.WatcherCount != domain.MaxWatchersPerLobby {
		t.Fatalf("watcher count = %d, %v", snapshot.WatcherCount, err)
	}
}

// watcherVersions returns each watcher row's xmin, which changes whenever the
// row is written.
func (fixture *serviceFixture) watcherVersions(t *testing.T) map[string]string {
	t.Helper()
	var rows []struct {
		PlayerID string
		Version  string
	}
	if err := fixture.db.Raw("SELECT player_id::text AS player_id, xmin::text AS version FROM lobby_watchers WHERE lobby_id = ?", fixture.scope.LobbyID).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{}
	for _, row := range rows {
		versions[row.PlayerID] = row.Version
	}
	return versions
}

func TestMutationsWriteOnlyTheWatcherRowsThatChanged(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	ctx := context.Background()
	kicked, left, stays := fixture.player(t, "Kicked"), fixture.player(t, "Left"), fixture.player(t, "Stays")
	for _, watcher := range []domain.Principal{kicked, left, stays} {
		fixture.watch(t, watcher)
	}
	if err := fixture.service.KickWatcher(ctx, fixture.host, fixture.scope, kicked.PlayerID); err != nil {
		t.Fatal(err)
	}
	before := fixture.watcherVersions(t)
	if len(before) != 3 {
		t.Fatalf("watcher rows = %v", before)
	}
	// Heartbeats, and watchers finding their rows again, write none.
	for range 3 {
		if _, err := fixture.service.Heartbeat(ctx, fixture.host, fixture.scope, domain.HeartbeatRequest{ClientInstanceID: fixture.clientID, MembershipGeneration: 1}); err != nil {
			t.Fatal(err)
		}
	}
	fixture.watch(t, stays)
	if after := fixture.watcherVersions(t); !reflect.DeepEqual(after, before) {
		t.Fatalf("a heartbeat wrote watcher rows: %v -> %v", before, after)
	}
	// Another watcher's change leaves the others' rows alone.
	if err := fixture.service.Unwatch(ctx, left, fixture.scope); err != nil {
		t.Fatal(err)
	}
	newcomer := fixture.player(t, "Newcomer")
	fixture.watch(t, newcomer)
	after := fixture.watcherVersions(t)
	if _, found := after[left.PlayerID]; found || after[kicked.PlayerID] != before[kicked.PlayerID] || after[stays.PlayerID] != before[stays.PlayerID] || after[newcomer.PlayerID] == "" {
		t.Fatalf("watcher rows after a leave and a new watcher: %v -> %v", before, after)
	}
}

func TestAWatcherWhoJoinsIsAMemberAndNoLongerAWatcher(t *testing.T) {
	fixture := newServiceFixture(t, 3)
	ctx := context.Background()
	joiner, kicked := fixture.player(t, "Joiner"), fixture.player(t, "Kicked")
	fixture.watch(t, joiner)
	fixture.watch(t, kicked)
	if err := fixture.service.KickWatcher(ctx, fixture.host, fixture.scope, kicked.PlayerID); err != nil {
		t.Fatal(err)
	}
	fixture.join(t, joiner)
	// The kick bars the session from watching, not from joining a waiting lobby.
	fixture.join(t, kicked)
	snapshot, err := fixture.service.Snapshot(ctx, fixture.host, fixture.scope)
	if err != nil || snapshot.WatcherCount != 0 || len(snapshot.Members) != 3 {
		t.Fatalf("snapshot after the joins: watchers %d, members %d, %v", snapshot.WatcherCount, len(snapshot.Members), err)
	}
	if listed, err := fixture.service.Watchers(ctx, fixture.host, fixture.scope); err != nil || len(listed) != 0 {
		t.Fatalf("watchers after the joins = %#v, %v", listed, err)
	}
	rows := fixture.read(t).Watchers
	if len(rows) != 1 || rows[0].PlayerID != kicked.PlayerID || rows[0].Watching() {
		t.Fatalf("watcher rows = %#v, want only the kick", rows)
	}
	if err := fixture.service.AuthorizeWatch(ctx, joiner, fixture.scope); err != nil {
		t.Fatalf("a member can't read what watchers read: %v", err)
	}
}

func TestWatcherForeignKeysCascadeSoOlderBuildsCanDeleteLobbiesAndPlayers(t *testing.T) {
	fixture := newServiceFixture(t, 2)
	var kinds []string
	if err := fixture.db.Raw("SELECT confdeltype::text FROM pg_constraint WHERE conname IN ('fk_lobby_watchers_room', 'fk_lobby_watchers_player')").Scan(&kinds).Error; err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 2 || kinds[0] != "c" || kinds[1] != "c" {
		t.Fatalf("watcher foreign key delete actions = %v, want cascade", kinds)
	}
}

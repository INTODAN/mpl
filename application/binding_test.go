package application_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"islandBattles/lobby/application"
	"islandBattles/lobby/domain"
)

type bindingReader struct {
	snapshot domain.Snapshot
	err      error
	calls    int
}

func (reader *bindingReader) Snapshot(context.Context, domain.Principal, domain.Scope) (domain.Snapshot, error) {
	reader.calls++
	return reader.snapshot, reader.err
}

func bindingSnapshot() (domain.Principal, domain.Scope, domain.Snapshot) {
	host := "11111111-1111-4111-8111-111111111111"
	room := "22222222-2222-4222-8222-222222222222"
	first := "33333333-3333-4333-8333-333333333333"
	second := "44444444-4444-4444-8444-444444444444"
	scope := domain.Scope{GameKey: "quiz-demo", LobbyID: room}
	snapshot := domain.Snapshot{
		Lobby:       domain.Lobby{ID: room, GameKey: scope.GameKey, Phase: domain.PhaseStarted, HostPlayerID: &host},
		Teams:       []domain.Team{{ID: second, Order: 2}, {ID: first, Order: 1}},
		StartRoster: domain.StartRoster{{PlayerID: host, TeamID: first, TeamOrder: 1}},
	}
	return domain.Principal{PlayerID: host}, scope, snapshot
}

func TestGameBindingSupportsOtherGamesAndReauthorizesReplay(t *testing.T) {
	principal, scope, snapshot := bindingSnapshot()
	reader := &bindingReader{snapshot: snapshot}
	type quizResult struct{ MatchID string }
	calls := 0
	binding, err := application.NewGameBinding("quiz-demo", reader, func(_ context.Context, started domain.Snapshot) (quizResult, error) {
		calls++
		if len(started.Teams) != 2 || started.Teams[0].Order != 1 || started.Teams[1].Order != 2 {
			t.Fatalf("expected ordered teams, including empty teams: %#v", started.Teams)
		}
		if !reflect.DeepEqual(started.StartRoster, snapshot.StartRoster) {
			t.Fatal("frozen roster changed")
		}
		return quizResult{MatchID: "quiz-match"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, err := binding.Bind(context.Background(), principal, scope)
		if err != nil || result.MatchID != "quiz-match" {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	}
	if snapshot.Teams[0].Order != 2 {
		t.Fatal("sorting mutated reader's team slice")
	}
	reader.err = domain.ErrNotFound // Membership expired or left before a replay.
	if _, err := binding.Bind(context.Background(), principal, scope); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("replay error=%v", err)
	}
	if calls != 2 || reader.calls != 3 {
		t.Fatalf("writes=%d authorizations=%d", calls, reader.calls)
	}
}

func TestGameBindingRejectsInvalidSnapshotsBeforePersistence(t *testing.T) {
	tests := []struct {
		name   string
		change func(*domain.Principal, *domain.Scope, *bindingReader)
		want   error
	}{
		{"other game", func(_ *domain.Principal, scope *domain.Scope, _ *bindingReader) { scope.GameKey = "other" }, domain.ErrNotFound},
		{"invalid room", func(_ *domain.Principal, scope *domain.Scope, _ *bindingReader) { scope.LobbyID = "invalid" }, domain.ErrNotFound},
		{"unauthorized", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) { reader.err = domain.ErrNotFound }, domain.ErrNotFound},
		{"wrong snapshot scope", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) {
			reader.snapshot.Lobby.GameKey = "other"
		}, domain.ErrNotFound},
		{"waiting", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) {
			reader.snapshot.Lobby.Phase = domain.PhaseWaiting
		}, domain.ErrPhaseConflict},
		{"closed", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) {
			reader.snapshot.Lobby.Phase = domain.PhaseClosed
		}, domain.ErrPhaseConflict},
		{"nonhost", func(principal *domain.Principal, _ *domain.Scope, _ *bindingReader) { principal.PlayerID = "other" }, domain.ErrNotHost},
		{"missing host", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) {
			reader.snapshot.Lobby.HostPlayerID = nil
		}, domain.ErrNotHost},
		{"empty roster", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) { reader.snapshot.StartRoster = nil }, domain.ErrPhaseConflict},
		{"empty teams", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) { reader.snapshot.Teams = nil }, domain.ErrPhaseConflict},
		{"invalid team ID", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) {
			reader.snapshot.Teams[0].ID = "invalid"
		}, domain.ErrUnavailable},
		{"duplicate team", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) {
			reader.snapshot.Teams[0].ID = reader.snapshot.Teams[1].ID
		}, domain.ErrUnavailable},
		{"duplicate order", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) { reader.snapshot.Teams[0].Order = 1 }, domain.ErrUnavailable},
		{"order gap", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) { reader.snapshot.Teams[0].Order = 3 }, domain.ErrUnavailable},
		{"roster mismatch", func(_ *domain.Principal, _ *domain.Scope, reader *bindingReader) {
			reader.snapshot.StartRoster[0].TeamOrder = 2
		}, domain.ErrUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			principal, scope, snapshot := bindingSnapshot()
			reader := &bindingReader{snapshot: snapshot}
			test.change(&principal, &scope, reader)
			binding, err := application.NewGameBinding("quiz-demo", reader, func(context.Context, domain.Snapshot) (string, error) {
				t.Fatal("persistence invoked for rejected request")
				return "", nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := binding.Bind(context.Background(), principal, scope); !errors.Is(err, test.want) {
				t.Fatalf("error=%v want=%v", err, test.want)
			}
		})
	}
}

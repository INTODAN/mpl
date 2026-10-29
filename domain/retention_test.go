package domain_test

import (
	"testing"
	"time"

	"islandBattles/lobby/domain"
)

func TestDefaultRetention(t *testing.T) {
	retention := domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"}).Retention
	want := domain.RetentionPolicy{
		CreateRequests:  24 * time.Hour,
		ExpiredSessions: time.Hour,
		Lobbies:         domain.LobbyRetention{IdlePeriod: time.Hour, MaximumAge: 24 * time.Hour},
		BatchSize:       50,
		PassInterval:    time.Minute,
	}
	if retention != want {
		t.Fatalf("default retention = %+v, want %+v", retention, want)
	}
}

func TestRetentionValidation(t *testing.T) {
	valid := domain.DefaultPolicy(domain.GameRegistration{Key: "island-battles"})
	// A member may reconnect until their lease and its grace run out.
	reconnectWindow := valid.LeaseDuration + valid.GraceDuration
	for name, mutate := range map[string]func(*domain.Policy){
		"no create request window":     func(policy *domain.Policy) { policy.Retention.CreateRequests = 0 },
		"no expired session grace":     func(policy *domain.Policy) { policy.Retention.ExpiredSessions = 0 },
		"negative expired grace":       func(policy *domain.Policy) { policy.Retention.ExpiredSessions = -time.Hour },
		"no idle period":               func(policy *domain.Policy) { policy.Retention.Lobbies.IdlePeriod = 0 },
		"idle within reconnect window": func(policy *domain.Policy) { policy.Retention.Lobbies.IdlePeriod = reconnectWindow },
		"no maximum age":               func(policy *domain.Policy) { policy.Retention.Lobbies.MaximumAge = 0 },
		"no batch":                     func(policy *domain.Policy) { policy.Retention.BatchSize = 0 },
		"batch too large":              func(policy *domain.Policy) { policy.Retention.BatchSize = domain.MaxRetentionBatchSize + 1 },
		"no pass interval":             func(policy *domain.Policy) { policy.Retention.PassInterval = 0 },
		"game idle within reconnect window": func(policy *domain.Policy) {
			policy.Games[0].Retention.IdlePeriod = reconnectWindow
		},
		"negative game idle":        func(policy *domain.Policy) { policy.Games[0].Retention.IdlePeriod = -time.Hour },
		"negative game maximum age": func(policy *domain.Policy) { policy.Games[0].Retention.MaximumAge = -time.Hour },
	} {
		invalid := valid
		invalid.Games = []domain.GameRegistration{{Key: "island-battles"}}
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, invalid.Retention)
		}
	}
	for name, mutate := range map[string]func(*domain.Policy){
		"shortest idle period": func(policy *domain.Policy) { policy.Retention.Lobbies.IdlePeriod = reconnectWindow + time.Second },
		"largest batch":        func(policy *domain.Policy) { policy.Retention.BatchSize = domain.MaxRetentionBatchSize },
		"game override": func(policy *domain.Policy) {
			policy.Games[0].Retention = domain.LobbyRetention{IdlePeriod: 10 * time.Minute}
		},
		"game maximum age only": func(policy *domain.Policy) {
			policy.Games[0].Retention = domain.LobbyRetention{MaximumAge: time.Minute}
		},
	} {
		changed := valid
		changed.Games = []domain.GameRegistration{{Key: "island-battles"}}
		mutate(&changed)
		if err := changed.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLobbyRetentionOverridesEachPeriodSeparately(t *testing.T) {
	policy := domain.DefaultPolicy(
		domain.GameRegistration{Key: "defaults"},
		domain.GameRegistration{Key: "short-idle", Retention: domain.LobbyRetention{IdlePeriod: 10 * time.Minute}},
		domain.GameRegistration{Key: "long-games", Retention: domain.LobbyRetention{IdlePeriod: 3 * time.Hour, MaximumAge: 7 * 24 * time.Hour}},
	)
	for key, want := range map[string]domain.LobbyRetention{
		"defaults":   {IdlePeriod: time.Hour, MaximumAge: 24 * time.Hour},
		"short-idle": {IdlePeriod: 10 * time.Minute, MaximumAge: 24 * time.Hour},
		"long-games": {IdlePeriod: 3 * time.Hour, MaximumAge: 7 * 24 * time.Hour},
		"retired":    {IdlePeriod: time.Hour, MaximumAge: 24 * time.Hour},
	} {
		if got := policy.LobbyRetention(key); got != want {
			t.Errorf("%s retention = %+v, want %+v", key, got, want)
		}
	}
}

func TestRetentionCutoffs(t *testing.T) {
	policy := domain.DefaultPolicy(
		domain.GameRegistration{Key: "island-battles"},
		domain.GameRegistration{Key: "quiz-demo", Retention: domain.LobbyRetention{IdlePeriod: 10 * time.Minute, MaximumAge: 2 * time.Hour}},
	)
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	cutoffs := policy.RetentionCutoffs(now)
	utc := now.UTC()
	if cutoffs.BatchSize != 50 || !cutoffs.CreateRequestsBefore.Equal(utc.Add(-24*time.Hour)) || cutoffs.CreateRequestsBefore.Location() != time.UTC {
		t.Fatalf("create request cutoff = %v, batch %d", cutoffs.CreateRequestsBefore, cutoffs.BatchSize)
	}
	if !cutoffs.SessionsExpiredBefore.Equal(utc.Add(-time.Hour)) {
		t.Fatalf("session cutoff = %v", cutoffs.SessionsExpiredBefore)
	}
	// A session lasts SessionDuration, so its player was created that much
	// before it expired.
	if !cutoffs.PlayersCreatedBefore.Equal(utc.Add(-time.Hour - policy.SessionDuration)) {
		t.Fatalf("player cutoff = %v", cutoffs.PlayersCreatedBefore)
	}
	want := []domain.LobbyCutoffs{
		{GameKey: "island-battles", IdleBefore: utc.Add(-time.Hour), CreatedBefore: utc.Add(-24 * time.Hour)},
		{GameKey: "quiz-demo", IdleBefore: utc.Add(-10 * time.Minute), CreatedBefore: utc.Add(-2 * time.Hour)},
	}
	if len(cutoffs.Games) != len(want) {
		t.Fatalf("game cutoffs = %+v", cutoffs.Games)
	}
	for index := range want {
		got := cutoffs.Games[index]
		if got.GameKey != want[index].GameKey || !got.IdleBefore.Equal(want[index].IdleBefore) || !got.CreatedBefore.Equal(want[index].CreatedBefore) {
			t.Errorf("game cutoffs[%d] = %+v, want %+v", index, got, want[index])
		}
	}
	if other := cutoffs.OtherGames; other.GameKey != "" || !other.IdleBefore.Equal(utc.Add(-time.Hour)) || !other.CreatedBefore.Equal(utc.Add(-24*time.Hour)) {
		t.Fatalf("other games = %+v", other)
	}
}

func TestRetentionResultBacklog(t *testing.T) {
	if (domain.RetentionResult{CreateRequests: 49, Sessions: 3, Lobbies: 49, Players: 0}).Backlog(50) {
		t.Fatal("partial batches reported a backlog")
	}
	for _, result := range []domain.RetentionResult{{CreateRequests: 50}, {Sessions: 50}, {Lobbies: 50}, {Players: 50}} {
		if !result.Backlog(50) {
			t.Errorf("%+v filled a batch without a backlog", result)
		}
	}
}

func TestMarkConnectedNeverMovesBack(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	room := domain.Lobby{}
	room.MarkConnected(now)
	room.MarkConnected(now.Add(-time.Second))
	if !room.LastConnectedAt.Equal(now) {
		t.Fatalf("last connection = %v, want %v", room.LastConnectedAt, now)
	}
	room.MarkConnected(now.Add(time.Second))
	if !room.LastConnectedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("last connection = %v", room.LastConnectedAt)
	}
}

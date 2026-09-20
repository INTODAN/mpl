package testutil_test

import (
	"testing"
	"time"

	"islandBattles/lobby/internal/testutil"
	lobbypostgres "islandBattles/lobby/postgres"

	"github.com/google/uuid"
)

func TestFixtureIsolation(t *testing.T) {
	first := testutil.NewPostgresFixture(t)
	second := testutil.NewPostgresFixture(t)
	room := lobbypostgres.RoomRecord{
		ID: uuid.NewString(), GameKey: "island-battles", Name: "isolated",
		JoinCode: "ISOLATE1", Capacity: 2, Phase: "waiting", Version: 1,
		CreatedAt: time.Now().UTC(), NextJoinSequence: 1,
	}
	if err := first.Create(&room).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := second.Model(&lobbypostgres.RoomRecord{}).Where(&lobbypostgres.RoomRecord{ID: room.ID}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("fixtures share room %s", room.ID)
	}
}

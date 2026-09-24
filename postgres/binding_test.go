package postgres_test

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"islandBattles/lobby/domain"
	"islandBattles/lobby/internal/testutil"
	"islandBattles/lobby/postgres"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

type bindingGame struct {
	ID      uint   `gorm:"primaryKey"`
	LobbyID string `gorm:"uniqueIndex:binding_lobby_link"`
	Name    string `gorm:"uniqueIndex:binding_name"`
}

func TestBindOncePostgres(t *testing.T) {
	database := testutil.NewPostgresFixture(t)
	if err := database.AutoMigrate(&bindingGame{}); err != nil {
		t.Fatal(err)
	}
	load := func(lobbyID string) func(*gorm.DB) (*bindingGame, error) {
		return func(db *gorm.DB) (*bindingGame, error) {
			var games []bindingGame
			if err := db.Where(&bindingGame{LobbyID: lobbyID}).Limit(1).Find(&games).Error; err != nil {
				return nil, err
			}
			if len(games) == 0 {
				return nil, nil
			}
			return &games[0], nil
		}
	}
	create := func(lobbyID, name string) func(*gorm.DB) (bindingGame, error) {
		return func(db *gorm.DB) (bindingGame, error) {
			game := bindingGame{LobbyID: lobbyID, Name: name}
			err := db.Create(&game).Error
			return game, err
		}
	}
	t.Run("concurrent creation and replay", func(t *testing.T) {
		// Force both initial reads to observe absence before either can insert.
		ready := make(chan struct{}, 2)
		release := make(chan struct{})
		var reads atomic.Int32
		lookup := func(db *gorm.DB) (*bindingGame, error) {
			game, err := load("concurrent")(db)
			if reads.Add(1) <= 2 {
				ready <- struct{}{}
				select {
				case <-release:
				case <-time.After(10 * time.Second):
					return nil, errors.New("concurrent read barrier timed out")
				}
			}
			return game, err
		}
		type outcome struct {
			game bindingGame
			err  error
		}
		results := make(chan outcome, 2)
		for contender := range 2 {
			go func() {
				game, err := postgres.BindOnce(database, "binding_lobby_link", lookup, create("concurrent", fmt.Sprintf("candidate-%d", contender)))
				results <- outcome{game, err}
			}()
		}
		for range 2 {
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				close(release)
				t.Fatal("initial reads did not reach barrier")
			}
		}
		close(release)
		first, second := <-results, <-results
		if first.err != nil || second.err != nil || first.game.ID == 0 || first.game != second.game {
			t.Fatalf("concurrent results: %#v %#v", first, second)
		}
		if reads.Load() != 3 {
			t.Fatalf("expected conflict recovery read; reads=%d", reads.Load())
		}
		replay, err := postgres.BindOnce(database, "binding_lobby_link", load("concurrent"), func(*gorm.DB) (bindingGame, error) {
			t.Fatal("replay attempted creation")
			return bindingGame{}, nil
		})
		if err != nil || replay != first.game {
			t.Fatalf("replay=%#v err=%v", replay, err)
		}
		var count int64
		if err := database.Model(&bindingGame{}).Where(&bindingGame{LobbyID: "concurrent"}).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("committed games=%d err=%v", count, err)
		}
	})
	t.Run("rollback all game writes", func(t *testing.T) {
		failure := errors.New("mapping write failed")
		result, err := postgres.BindOnce(database, "binding_lobby_link", load("rollback"), func(db *gorm.DB) (bindingGame, error) {
			game, err := create("rollback", "rollback")(db)
			if err != nil {
				return game, err
			}
			return game, failure
		})
		if !errors.Is(err, failure) || result.ID != 0 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		if game, err := load("rollback")(database); err != nil || game != nil {
			t.Fatalf("write survived rollback: %#v %v", game, err)
		}
	})
	t.Run("unrelated unique failure is not replayed", func(t *testing.T) {
		if _, err := create("reserved", "reserved")(database); err != nil {
			t.Fatal(err)
		}
		reads := 0
		lookup := func(db *gorm.DB) (*bindingGame, error) { reads++; return load("unrelated")(db) }
		_, err := postgres.BindOnce(database, "binding_lobby_link", lookup, create("unrelated", "reserved"))
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || conflict.ConstraintName != "binding_name" || reads != 1 {
			t.Fatalf("error=%v reads=%d", err, reads)
		}
	})
	t.Run("incomplete binding after conflict is rejected", func(t *testing.T) {
		reads := 0
		_, err := postgres.BindOnce(database, "binding_lobby_link", func(*gorm.DB) (*bindingGame, error) {
			reads++
			if reads == 1 {
				return nil, nil
			}
			return nil, domain.ErrUnavailable
		}, create("concurrent", "different-name"))
		if !errors.Is(err, domain.ErrUnavailable) || reads != 2 {
			t.Fatalf("error=%v reads=%d", err, reads)
		}
	})
}

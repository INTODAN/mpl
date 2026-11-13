package domain

import (
	"bytes"
	"sort"

	"islandBattles/lobby/internal/optional"
)

// DeepCopy detaches every slice/pointer and normalizes nil lists to empty lists.
func (aggregate Aggregate) DeepCopy() Aggregate {
	result := aggregate
	result.Lobby.HostPlayerID = optional.Copy(aggregate.Lobby.HostPlayerID)
	result.Lobby.GameSettings = bytes.Clone(aggregate.Lobby.GameSettings)
	result.Teams = append([]Team{}, aggregate.Teams...)
	result.Members = append([]Member{}, aggregate.Members...)
	for index := range result.Members {
		result.Members[index].TeamID = optional.Copy(aggregate.Members[index].TeamID)
		result.Members[index].DisconnectedUntil = optional.Copy(aggregate.Members[index].DisconnectedUntil)
		result.Members[index].SeatSettings = bytes.Clone(aggregate.Members[index].SeatSettings)
		result.Members[index].MemberSettings = bytes.Clone(aggregate.Members[index].MemberSettings)
	}
	result.Leases = append([]Lease{}, aggregate.Leases...)
	for index := range result.Leases {
		result.Leases[index].ReleasedAt = optional.Copy(aggregate.Leases[index].ReleasedAt)
	}
	result.StartRoster = append(StartRoster{}, aggregate.StartRoster...)
	result.Watchers = append([]Watcher{}, aggregate.Watchers...)
	for index := range result.Watchers {
		result.Watchers[index].KickedAt = optional.Copy(aggregate.Watchers[index].KickedAt)
	}
	return result
}

// MergeChanges preserves first-seen intent order while deduplicating exactly by
// scope, player and generation; distinct departures are never merged together.
func MergeChanges(changes ...Change) Change {
	result := Change{Revocations: []RevocationIntent{}}
	seen := map[RevocationIntent]bool{}
	for _, change := range changes {
		result.PublicChanged = result.PublicChanged || change.PublicChanged
		result.PrivateChanged = result.PrivateChanged || change.PrivateChanged
		for _, intent := range change.Revocations {
			if !seen[intent] {
				seen[intent] = true
				result.Revocations = append(result.Revocations, intent)
			}
		}
	}
	return result
}

// BuildSnapshot constructs a detached, ordered authorized view from the committed
// aggregate. Deadline normalization must precede this helper under the store lock.
func BuildSnapshot(aggregate Aggregate, playerID string) (Snapshot, error) {
	member := FindMember(&aggregate, playerID)
	if member == nil || !member.Active {
		return Snapshot{}, ErrNotFound
	}
	snapshot, err := BuildGameSnapshot(aggregate)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.MembershipGeneration = member.Generation
	return snapshot, nil
}

// BuildGameSnapshot is the snapshot that a lobby's game reads, through the
// start hook or the game API: the view of every member, with no player
// behind it, so MembershipGeneration is 0. Callers apply deadline
// normalization first, as for BuildSnapshot.
func BuildGameSnapshot(aggregate Aggregate) (Snapshot, error) {
	channel, err := RoomChannel(aggregate.Lobby.Scope())
	if err != nil {
		return Snapshot{}, err
	}
	detached := aggregate.DeepCopy()
	members := make([]Member, 0, len(detached.Members))
	for _, member := range detached.Members {
		if member.Active {
			members = append(members, member)
		}
	}
	sort.Slice(members, func(first, second int) bool {
		if members[first].JoinSequence == members[second].JoinSequence {
			return members[first].ID < members[second].ID
		}
		return members[first].JoinSequence < members[second].JoinSequence
	})
	sort.Slice(detached.Teams, func(first, second int) bool {
		if detached.Teams[first].Order == detached.Teams[second].Order {
			return detached.Teams[first].ID < detached.Teams[second].ID
		}
		return detached.Teams[first].Order < detached.Teams[second].Order
	})
	return Snapshot{Lobby: detached.Lobby, Teams: detached.Teams, Members: members, StartRoster: detached.StartRoster, Channel: channel, WatcherCount: WatcherCount(detached.Watchers)}, nil
}

// WatcherCount counts the watchers who may watch.
func WatcherCount(watchers []Watcher) int {
	count := 0
	for _, watcher := range watchers {
		if watcher.Watching() {
			count++
		}
	}
	return count
}

// ActiveWatchers counts the watchers of the aggregate who may watch.
func ActiveWatchers(aggregate *Aggregate) int { return WatcherCount(aggregate.Watchers) }

// FindWatcher returns a pointer into the aggregate for locked mutation, or
// nil when the player has no watcher row.
func FindWatcher(aggregate *Aggregate, playerID string) *Watcher {
	if aggregate == nil {
		return nil
	}
	for index := range aggregate.Watchers {
		if aggregate.Watchers[index].PlayerID == playerID {
			return &aggregate.Watchers[index]
		}
	}
	return nil
}

// CanWatch reports whether a player may watch the lobby: an active member,
// or a watcher the host hasn't kicked.
func CanWatch(aggregate *Aggregate, playerID string) bool {
	if member := FindMember(aggregate, playerID); member != nil && member.Active {
		return true
	}
	watcher := FindWatcher(aggregate, playerID)
	return watcher != nil && watcher.Watching()
}

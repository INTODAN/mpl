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
	}
	result.Leases = append([]Lease{}, aggregate.Leases...)
	for index := range result.Leases {
		result.Leases[index].ReleasedAt = optional.Copy(aggregate.Leases[index].ReleasedAt)
	}
	result.StartRoster = append(StartRoster{}, aggregate.StartRoster...)
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
	return Snapshot{Lobby: detached.Lobby, Teams: detached.Teams, Members: members, StartRoster: detached.StartRoster, MembershipGeneration: member.Generation, Channel: channel}, nil
}

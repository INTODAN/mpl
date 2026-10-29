-- The retention pass deletes a lobby once nobody has been connected to it for
-- the idle period, so each room records when a client last was. Rooms created
-- before this get the latest end of one of their leases, or their creation
-- time, so no lobby that is in use now looks idle.

ALTER TABLE lobby_rooms ADD COLUMN last_connected_at timestamp with time zone;
UPDATE lobby_rooms SET last_connected_at = greatest(
    created_at,
    (SELECT max(least(lobby_leases.released_at, lobby_leases.expires_at))
     FROM lobby_leases WHERE lobby_leases.lobby_id = lobby_rooms.id)
);
ALTER TABLE lobby_rooms ALTER COLUMN last_connected_at SET NOT NULL;

-- The pass selects expired rows by these times.
CREATE INDEX ix_lobby_rooms_last_connected_at ON lobby_rooms (last_connected_at);
CREATE INDEX ix_lobby_rooms_created_at ON lobby_rooms (created_at);
CREATE INDEX ix_lobby_create_requests_created_at ON lobby_create_requests (created_at);
CREATE INDEX ix_lobby_players_created_at ON lobby_players (created_at);

-- A player is deleted only when nothing references it. These indexes serve
-- that check and the foreign key checks of the delete.
CREATE INDEX ix_lobby_rooms_host_player_id ON lobby_rooms (host_player_id);
CREATE INDEX ix_lobby_memberships_player_id ON lobby_memberships (player_id);
CREATE INDEX ix_lobby_start_roster_player_id ON lobby_start_roster (player_id);

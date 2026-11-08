-- People outside a lobby's members can watch it with its join code. A watcher
-- takes no seat and holds no lease; the row lives until they leave, or the
-- lobby is deleted. A watcher the host kicked keeps the row, with kicked_at
-- set, so the code can't let them back in. Builds that don't know the table
-- yet, which run while a deploy rolls out, never read or write it.

CREATE TABLE lobby_watchers (
    lobby_id uuid NOT NULL,
    player_id uuid NOT NULL,
    display_name text NOT NULL,
    joined_at timestamp with time zone NOT NULL,
    kicked_at timestamp with time zone,
    PRIMARY KEY (lobby_id, player_id)
);
ALTER TABLE lobby_watchers ADD CONSTRAINT fk_lobby_watchers_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id);
ALTER TABLE lobby_watchers ADD CONSTRAINT fk_lobby_watchers_player FOREIGN KEY (player_id) REFERENCES lobby_players (id);
-- A player is deleted only when nothing references it.
CREATE INDEX ix_lobby_watchers_player_id ON lobby_watchers (player_id);

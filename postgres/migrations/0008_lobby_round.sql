-- A lobby can play several games: once a game ends, a member reopens the
-- lobby, which goes back to waiting and plays its next round. round numbers
-- them from 1, and a game binds each of its games to the lobby and round.
-- Existing rooms are in their first round. Builds from before this migration
-- never read or write the column, and their inserts get the default. The
-- stored responses of create requests, which a retry replays, are snapshots
-- of a new lobby, so they get round 1 too.

ALTER TABLE lobby_rooms ADD COLUMN round bigint NOT NULL DEFAULT 1;
ALTER TABLE lobby_rooms ADD CONSTRAINT ck_lobby_rooms_round CHECK (round > 0);

UPDATE lobby_create_requests
SET response_json = jsonb_set(response_json, '{lobby,round}', '1'::jsonb)
WHERE jsonb_typeof(response_json -> 'lobby') = 'object' AND response_json -> 'lobby' -> 'round' IS NULL;

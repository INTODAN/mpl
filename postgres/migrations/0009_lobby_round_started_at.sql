-- A lobby is deleted once it is older than the maximum age, which now runs
-- from the start of its current round instead of from its creation, so a
-- group that plays again in one lobby is not cut off in the middle of a
-- round. round_started_at is the lobby's creation for round 1, and the time
-- of the last reopen for later rounds. Existing rooms get their creation
-- time. Builds from before this migration never read or write the column,
-- and their inserts get the default.

ALTER TABLE lobby_rooms ADD COLUMN round_started_at timestamptz NOT NULL DEFAULT now();

UPDATE lobby_rooms SET round_started_at = created_at;

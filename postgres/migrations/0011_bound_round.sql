-- A game that the lobby calls over HTTP confirms the game it created for a
-- started lobby's round. bound_round is the last round it confirmed, or 0 for
-- none, so a start request on a started lobby whose round has no game yet
-- calls the game again. Existing rooms have none. Builds from before this
-- migration never read or write the column, and their inserts get the
-- default.

ALTER TABLE lobby_rooms ADD COLUMN bound_round bigint NOT NULL DEFAULT 0;

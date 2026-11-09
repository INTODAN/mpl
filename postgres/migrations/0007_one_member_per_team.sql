-- A team holds at most one member, so it no longer has a capacity, and a
-- room's capacity is its number of teams. Rooms created before this held a
-- capacity the client chose, from 2 to 64; each gets its number of teams,
-- which was always from 2 to 16, and the check narrows to that range. The lobby
-- creates every room with its teams; a room without teams, which only a test
-- can make, keeps its capacity.
-- Memberships stay as they are: a waiting room with two members on one team
-- refuses to start until one of them moves. Builds from before this migration
-- read and write lobby_teams.capacity, so they fail on lobby requests once it
-- runs.

ALTER TABLE lobby_teams DROP CONSTRAINT ck_lobby_teams_capacity;
ALTER TABLE lobby_teams DROP COLUMN capacity;

UPDATE lobby_rooms SET capacity = (
    SELECT count(*) FROM lobby_teams WHERE lobby_teams.lobby_id = lobby_rooms.id
) WHERE EXISTS (SELECT 1 FROM lobby_teams WHERE lobby_teams.lobby_id = lobby_rooms.id);
ALTER TABLE lobby_rooms DROP CONSTRAINT ck_lobby_rooms_capacity;
ALTER TABLE lobby_rooms ADD CONSTRAINT ck_lobby_rooms_capacity CHECK (capacity BETWEEN 2 AND 16);

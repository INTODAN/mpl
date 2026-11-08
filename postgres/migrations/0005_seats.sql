-- A lobby can hold seats that no person plays, such as a game's bot, which
-- the host adds to a team. A seat is a membership of kind 'seat' with the
-- game's settings for it, and its own lobby_players row without a session, so
-- the start roster, leases' and games' references to players keep working.
-- Existing memberships are people. The default serves builds that don't know
-- the column yet, which run while a deploy rolls out.

ALTER TABLE lobby_memberships ADD COLUMN kind text NOT NULL DEFAULT 'human';
ALTER TABLE lobby_memberships ADD COLUMN seat_settings json;
ALTER TABLE lobby_memberships ADD CONSTRAINT ck_lobby_memberships_kind CHECK (kind IN ('human', 'seat'));
ALTER TABLE lobby_memberships ADD CONSTRAINT ck_lobby_memberships_seat_settings CHECK ((kind = 'seat') = (seat_settings IS NOT NULL));

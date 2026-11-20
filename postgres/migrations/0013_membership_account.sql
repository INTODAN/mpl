-- A membership records the account of a person who had signed in when they
-- joined, so the lobby can tell its game which account each member is. Null
-- for a guest, for a seat, and for memberships from before this migration.
-- It has no foreign key: deleting an account leaves a lobby's history as it
-- was. Builds from before this migration never read or write the column.

ALTER TABLE lobby_memberships ADD COLUMN account_id uuid;

-- A person sets their own settings for the lobby's game, such as what plays
-- for them, which the game checks and the lobby stores without reading. Null
-- until they set some. Builds from before this migration never read or write
-- the column.

ALTER TABLE lobby_memberships ADD COLUMN member_settings jsonb;

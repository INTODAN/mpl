-- The game settings a host chose when creating the lobby, written once and
-- never read by the lobby. json, unlike jsonb, keeps the canonical text the
-- lobby stores exactly. The default gives existing rooms an empty object.

ALTER TABLE lobby_rooms ADD COLUMN IF NOT EXISTS game_settings json NOT NULL DEFAULT '{}';

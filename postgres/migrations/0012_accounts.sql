-- A person who signs in, such as with Google, has an account: the lobby then
-- knows them across sessions and devices. An account holds the identity
-- provider's subject and the display name the person chose, unique among
-- accounts without regard to case (display_name_key is the case-folded
-- name), and nothing else of the provider's. Each sign-in creates a new
-- player of the account, with its one session, as a guest session does, so
-- a player's account_id names the account it belongs to; guests have none.
-- Deleting an account leaves its players, without an account. Retention never
-- deletes an account. Builds from before this migration never read or write
-- the new table or column.

CREATE TABLE lobby_accounts (
    id uuid PRIMARY KEY,
    google_subject text NOT NULL,
    display_name text NOT NULL,
    display_name_key text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX ux_lobby_accounts_google_subject ON lobby_accounts (google_subject);
CREATE UNIQUE INDEX ux_lobby_accounts_display_name_key ON lobby_accounts (display_name_key);

ALTER TABLE lobby_players ADD COLUMN account_id uuid;
ALTER TABLE lobby_players ADD CONSTRAINT fk_lobby_players_account FOREIGN KEY (account_id) REFERENCES lobby_accounts (id) ON DELETE SET NULL;
CREATE INDEX ix_lobby_players_account ON lobby_players (account_id);

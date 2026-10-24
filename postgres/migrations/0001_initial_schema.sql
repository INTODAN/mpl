-- The generic lobby schema as the code created it before migrations were
-- versioned. Databases created by that code are adopted at this version
-- without running it.

CREATE TABLE lobby_players (
    id uuid PRIMARY KEY,
    display_name text NOT NULL,
    created_at timestamp with time zone NOT NULL
);

CREATE TABLE lobby_sessions (
    id uuid PRIMARY KEY,
    player_id uuid NOT NULL,
    bearer_hash bytea NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_lobby_sessions_bearer_hash CHECK (octet_length(bearer_hash) = 32)
);

CREATE TABLE lobby_rooms (
    id uuid PRIMARY KEY,
    game_key text NOT NULL,
    name text NOT NULL,
    join_code text NOT NULL,
    capacity bigint NOT NULL,
    phase text NOT NULL,
    version bigint NOT NULL,
    host_player_id uuid,
    created_at timestamp with time zone NOT NULL,
    next_join_sequence bigint NOT NULL,
    CONSTRAINT ck_lobby_rooms_capacity CHECK (capacity BETWEEN 2 AND 64),
    CONSTRAINT ck_lobby_rooms_phase CHECK (phase IN ('waiting', 'started', 'closed')),
    CONSTRAINT ck_lobby_rooms_version CHECK (version > 0),
    CONSTRAINT ck_lobby_rooms_next_join_sequence CHECK (next_join_sequence > 0)
);

CREATE TABLE lobby_teams (
    id uuid PRIMARY KEY,
    lobby_id uuid NOT NULL,
    name text NOT NULL,
    normalized_name text NOT NULL,
    capacity bigint NOT NULL,
    team_order bigint NOT NULL,
    CONSTRAINT ck_lobby_teams_capacity CHECK (capacity BETWEEN 1 AND 64),
    CONSTRAINT ck_lobby_teams_order CHECK (team_order > 0)
);

CREATE TABLE lobby_memberships (
    id uuid PRIMARY KEY,
    lobby_id uuid NOT NULL,
    player_id uuid NOT NULL,
    display_name text NOT NULL,
    generation bigint NOT NULL,
    active boolean NOT NULL,
    team_id uuid,
    ready boolean NOT NULL,
    joined_at timestamp with time zone NOT NULL,
    join_sequence bigint NOT NULL,
    disconnected_until timestamp with time zone,
    CONSTRAINT ck_lobby_memberships_generation CHECK (generation > 0),
    CONSTRAINT ck_lobby_memberships_join_sequence CHECK (join_sequence > 0)
);

CREATE TABLE lobby_leases (
    lobby_id uuid NOT NULL,
    player_id uuid NOT NULL,
    generation bigint NOT NULL,
    client_instance_id uuid NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    released_at timestamp with time zone,
    PRIMARY KEY (lobby_id, player_id, generation, client_instance_id),
    CONSTRAINT ck_lobby_leases_generation CHECK (generation > 0)
);

CREATE TABLE lobby_start_roster (
    lobby_id uuid NOT NULL,
    player_id uuid NOT NULL,
    team_id uuid NOT NULL,
    member_id uuid NOT NULL,
    team_order bigint NOT NULL,
    PRIMARY KEY (lobby_id, member_id),
    CONSTRAINT ck_lobby_start_roster_team_order CHECK (team_order > 0)
);

CREATE TABLE lobby_create_requests (
    player_id uuid NOT NULL,
    request_id uuid NOT NULL,
    payload_hash bytea NOT NULL,
    response_json jsonb NOT NULL,
    created_at timestamp with time zone NOT NULL,
    PRIMARY KEY (player_id, request_id),
    CONSTRAINT ck_lobby_create_requests_hash CHECK (octet_length(payload_hash) = 32)
);

CREATE TABLE lobby_outbox (
    id uuid PRIMARY KEY,
    kind text NOT NULL,
    event_id uuid NOT NULL,
    game_key text NOT NULL,
    lobby_id uuid,
    player_id uuid,
    generation bigint,
    version bigint,
    attempts bigint NOT NULL,
    next_attempt_at timestamp with time zone NOT NULL,
    claim_token uuid,
    claim_deadline timestamp with time zone,
    acknowledged_at timestamp with time zone,
    last_error text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT ck_lobby_outbox_kind CHECK (kind IN ('invalidation', 'revocation')),
    CONSTRAINT ck_lobby_outbox_attempts CHECK (attempts >= 0),
    CONSTRAINT ck_lobby_outbox_payload CHECK (
        (kind = 'invalidation' AND lobby_id IS NOT NULL AND version IS NOT NULL AND player_id IS NULL AND generation IS NULL)
        OR (kind = 'revocation' AND lobby_id IS NOT NULL AND player_id IS NOT NULL AND generation IS NOT NULL)
    )
);

CREATE UNIQUE INDEX ux_lobby_sessions_player ON lobby_sessions (player_id);
CREATE INDEX ix_lobby_sessions_expires_at ON lobby_sessions (expires_at);
CREATE UNIQUE INDEX ux_lobby_rooms_game_join_code ON lobby_rooms (game_key, join_code);
CREATE UNIQUE INDEX ux_lobby_teams_lobby_id_id ON lobby_teams (lobby_id, id);
CREATE UNIQUE INDEX ux_lobby_teams_lobby_order ON lobby_teams (lobby_id, team_order);
CREATE UNIQUE INDEX ux_lobby_teams_lobby_normalized_name ON lobby_teams (lobby_id, normalized_name);
CREATE UNIQUE INDEX ux_lobby_memberships_lobby_player ON lobby_memberships (lobby_id, player_id);
CREATE UNIQUE INDEX ux_lobby_memberships_lobby_id_id ON lobby_memberships (lobby_id, id);
CREATE INDEX ix_lobby_memberships_scope ON lobby_memberships (lobby_id, active, join_sequence);
CREATE INDEX ix_lobby_leases_deadline ON lobby_leases (lobby_id, expires_at);
CREATE INDEX ix_lobby_leases_scope ON lobby_leases (lobby_id, player_id, generation);
CREATE UNIQUE INDEX ux_lobby_start_roster_lobby_player ON lobby_start_roster (lobby_id, player_id);
CREATE UNIQUE INDEX ux_lobby_outbox_event_id ON lobby_outbox (event_id);
CREATE UNIQUE INDEX ux_lobby_outbox_invalidation_version ON lobby_outbox (lobby_id, version) WHERE kind = 'invalidation';
CREATE INDEX ix_lobby_outbox_pending_claim ON lobby_outbox (next_attempt_at, claim_deadline) WHERE acknowledged_at IS NULL;
CREATE INDEX ix_lobby_outbox_scope ON lobby_outbox (lobby_id, kind, acknowledged_at);

ALTER TABLE lobby_sessions ADD CONSTRAINT fk_lobby_sessions_player FOREIGN KEY (player_id) REFERENCES lobby_players (id);
ALTER TABLE lobby_rooms ADD CONSTRAINT fk_lobby_rooms_host FOREIGN KEY (host_player_id) REFERENCES lobby_players (id);
ALTER TABLE lobby_teams ADD CONSTRAINT fk_lobby_teams_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id);
ALTER TABLE lobby_memberships ADD CONSTRAINT fk_lobby_memberships_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id);
ALTER TABLE lobby_memberships ADD CONSTRAINT fk_lobby_memberships_player FOREIGN KEY (player_id) REFERENCES lobby_players (id);
ALTER TABLE lobby_memberships ADD CONSTRAINT fk_lobby_memberships_team FOREIGN KEY (lobby_id, team_id) REFERENCES lobby_teams (lobby_id, id);
ALTER TABLE lobby_leases ADD CONSTRAINT fk_lobby_leases_membership FOREIGN KEY (lobby_id, player_id) REFERENCES lobby_memberships (lobby_id, player_id);
ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id);
ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_player FOREIGN KEY (player_id) REFERENCES lobby_players (id);
ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_team FOREIGN KEY (lobby_id, team_id) REFERENCES lobby_teams (lobby_id, id);
ALTER TABLE lobby_start_roster ADD CONSTRAINT fk_lobby_start_roster_member FOREIGN KEY (lobby_id, member_id) REFERENCES lobby_memberships (lobby_id, id);
ALTER TABLE lobby_create_requests ADD CONSTRAINT fk_lobby_create_requests_player FOREIGN KEY (player_id) REFERENCES lobby_players (id);
ALTER TABLE lobby_outbox ADD CONSTRAINT fk_lobby_outbox_room FOREIGN KEY (lobby_id) REFERENCES lobby_rooms (id);
ALTER TABLE lobby_outbox ADD CONSTRAINT fk_lobby_outbox_player FOREIGN KEY (player_id) REFERENCES lobby_players (id);

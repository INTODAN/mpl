-- Committed changes are pushed to Centrifugo by the request that made them, so
-- no table holds messages waiting to be sent. The outbox held no lobby state.

DROP TABLE IF EXISTS lobby_outbox;

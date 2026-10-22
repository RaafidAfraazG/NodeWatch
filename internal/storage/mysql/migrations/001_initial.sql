CREATE TABLE IF NOT EXISTS snapshots (
    host_id VARBINARY(128) NOT NULL,
    collected_ns BIGINT NOT NULL,
    payload_hash BINARY(32) NOT NULL,
    payload JSON NOT NULL,
    PRIMARY KEY (host_id, collected_ns)
) ENGINE=InnoDB;

CREATE TABLE IF NOT EXISTS hosts (
    host_id VARBINARY(128) NOT NULL,
    latest_ns BIGINT NOT NULL,
    last_seen_ns BIGINT NOT NULL,
    PRIMARY KEY (host_id)
) ENGINE=InnoDB;

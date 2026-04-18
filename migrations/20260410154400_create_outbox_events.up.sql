CREATE TABLE outbox_events (
    id UUID PRIMARY KEY,

    aggregate_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(100) NOT NULL,

    payload JSONB NOT NULL,

    status VARCHAR(20) NOT NULL,

    retry_count INT DEFAULT 0,
    next_retry_at TIMESTAMP,

    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMP
);

CREATE INDEX idx_outbox_status_retry 
ON outbox_events (status, next_retry_at);
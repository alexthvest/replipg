CREATE TABLE runtime_cursors (
    source TEXT NOT NULL PRIMARY KEY CHECK (length(source) > 0),
    last_completed_id INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE processed_updates (
    update_id INTEGER NOT NULL PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('completed', 'ignored', 'decode_failed', 'handler_failed')),
    correlation_id TEXT NOT NULL CHECK (length(correlation_id) > 0),
    failure_code TEXT,
    completed_at INTEGER NOT NULL,
    CHECK (
        (status IN ('completed', 'ignored') AND failure_code IS NULL)
        OR
        (status IN ('decode_failed', 'handler_failed') AND failure_code IS NOT NULL AND length(failure_code) > 0)
    )
) STRICT;

CREATE TABLE outbound_deliveries (
    id TEXT NOT NULL PRIMARY KEY CHECK (length(id) > 0),
    kind TEXT NOT NULL CHECK (length(kind) > 0),
    content_version INTEGER NOT NULL CHECK (content_version > 0),
    content BLOB NOT NULL CHECK (typeof(content) = 'blob' AND length(content) > 0),
    destination_chat_id INTEGER NOT NULL CHECK (destination_chat_id < 0),
    target_user_id INTEGER,
    authorization_kind TEXT,
    callback_query_id TEXT CHECK (callback_query_id IS NULL OR length(callback_query_id) > 0),
    incoming_ephemeral_message_id INTEGER CHECK (incoming_ephemeral_message_id IS NULL OR incoming_ephemeral_message_id > 0),
    authorization_expires_at INTEGER,
    replace_callback_message INTEGER NOT NULL CHECK (replace_callback_message IN (0, 1)),
    state TEXT NOT NULL CHECK (state IN (
        'pending',
        'attempting',
        'retrying',
        'reconciliation_required',
        'completed',
        'permanent_failure'
    )),
    max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
    initial_delay_ms INTEGER NOT NULL CHECK (initial_delay_ms > 0),
    max_delay_ms INTEGER NOT NULL CHECK (max_delay_ms >= initial_delay_ms),
    attempt_count INTEGER NOT NULL CHECK (attempt_count >= 0 AND attempt_count <= max_attempts),
    next_attempt_at INTEGER,
    reason_code TEXT CHECK (reason_code IS NULL OR length(reason_code) > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    terminal_at INTEGER,
    CHECK (
        (state IN ('pending', 'retrying') AND next_attempt_at IS NOT NULL)
        OR
        (state NOT IN ('pending', 'retrying') AND next_attempt_at IS NULL)
    ),
    CHECK (state != 'permanent_failure' OR reason_code IS NOT NULL),
    CHECK (state != 'reconciliation_required' OR reason_code IS NOT NULL),
    CHECK (state NOT IN ('pending', 'retrying') OR attempt_count < max_attempts),
    CHECK (
        (
            target_user_id IS NULL
            AND authorization_kind IS NULL
            AND callback_query_id IS NULL
            AND incoming_ephemeral_message_id IS NULL
            AND authorization_expires_at IS NULL
            AND replace_callback_message = 0
        )
        OR
        (
            target_user_id IS NOT NULL
            AND
            target_user_id > 0
            AND authorization_kind IS NOT NULL
            AND (
                (
                    authorization_kind = 'callback'
                    AND callback_query_id IS NOT NULL
                    AND incoming_ephemeral_message_id IS NULL
                    AND authorization_expires_at IS NOT NULL
                )
                OR
                (
                    authorization_kind = 'incoming_ephemeral'
                    AND callback_query_id IS NULL
                    AND incoming_ephemeral_message_id IS NOT NULL
                    AND authorization_expires_at IS NOT NULL
                    AND replace_callback_message = 0
                )
                OR
                (
                    authorization_kind = 'administrator'
                    AND callback_query_id IS NULL
                    AND incoming_ephemeral_message_id IS NULL
                    AND authorization_expires_at IS NULL
                    AND replace_callback_message = 0
                )
            )
        )
    ),
    CHECK (
        (state IN ('completed', 'permanent_failure') AND terminal_at IS NOT NULL)
        OR
        (state NOT IN ('completed', 'permanent_failure') AND terminal_at IS NULL)
    )
) STRICT;

CREATE TABLE outbound_attempts (
    id TEXT NOT NULL PRIMARY KEY CHECK (length(id) > 0),
    delivery_id TEXT NOT NULL,
    correlation_id TEXT NOT NULL CHECK (length(correlation_id) > 0),
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    outcome TEXT CHECK (outcome IN ('accepted', 'retryable_failure', 'permanent_failure', 'indeterminate', 'cancelled')),
    reason_code TEXT CHECK (reason_code IS NULL OR length(reason_code) > 0),
    CHECK (
        (finished_at IS NULL AND outcome IS NULL AND reason_code IS NULL)
        OR
        (finished_at IS NOT NULL AND finished_at >= started_at AND outcome IS NOT NULL)
    ),
    CHECK (outcome NOT IN ('retryable_failure', 'permanent_failure', 'indeterminate') OR reason_code IS NOT NULL),
    CHECK (outcome != 'accepted' OR reason_code IS NULL),
    FOREIGN KEY (delivery_id) REFERENCES outbound_deliveries(id) ON DELETE RESTRICT
) STRICT;

CREATE TABLE scheduled_jobs (
    id TEXT NOT NULL PRIMARY KEY CHECK (length(id) > 0),
    job_type TEXT NOT NULL CHECK (length(job_type) > 0),
    payload_version INTEGER NOT NULL CHECK (payload_version > 0),
    payload BLOB NOT NULL CHECK (typeof(payload) = 'blob' AND length(payload) > 0),
    due_at INTEGER NOT NULL,
    state TEXT NOT NULL CHECK (state IN (
        'pending',
        'claimed',
        'retrying',
        'completed',
        'cancelled',
        'permanent_failure'
    )),
    max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
    initial_delay_ms INTEGER NOT NULL CHECK (initial_delay_ms > 0),
    max_delay_ms INTEGER NOT NULL CHECK (max_delay_ms >= initial_delay_ms),
    attempt_count INTEGER NOT NULL CHECK (attempt_count >= 0 AND attempt_count <= max_attempts),
    next_attempt_at INTEGER,
    claim_owner TEXT CHECK (claim_owner IS NULL OR length(claim_owner) > 0),
    claim_correlation_id TEXT CHECK (claim_correlation_id IS NULL OR length(claim_correlation_id) > 0),
    lease_until INTEGER,
    reason_code TEXT CHECK (reason_code IS NULL OR length(reason_code) > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    terminal_at INTEGER,
    CHECK (
        (state = 'retrying' AND next_attempt_at IS NOT NULL)
        OR
        (state != 'retrying' AND next_attempt_at IS NULL)
    ),
    CHECK (
        (state = 'claimed' AND claim_owner IS NOT NULL AND claim_correlation_id IS NOT NULL AND lease_until IS NOT NULL)
        OR
        (state != 'claimed' AND claim_owner IS NULL AND claim_correlation_id IS NULL AND lease_until IS NULL)
    ),
    CHECK (state != 'permanent_failure' OR reason_code IS NOT NULL),
    CHECK (state NOT IN ('pending', 'retrying') OR attempt_count < max_attempts),
    CHECK (
        (state IN ('completed', 'cancelled', 'permanent_failure') AND terminal_at IS NOT NULL)
        OR
        (state NOT IN ('completed', 'cancelled', 'permanent_failure') AND terminal_at IS NULL)
    )
) STRICT;

CREATE INDEX outbound_deliveries_pending_due_idx
    ON outbound_deliveries (state, next_attempt_at)
    WHERE state = 'pending';

CREATE INDEX outbound_deliveries_retry_due_idx
    ON outbound_deliveries (state, next_attempt_at)
    WHERE state = 'retrying';

CREATE INDEX outbound_attempts_delivery_idx
    ON outbound_attempts (delivery_id, started_at);

CREATE INDEX scheduled_jobs_pending_due_idx
    ON scheduled_jobs (state, due_at)
    WHERE state = 'pending';

CREATE INDEX scheduled_jobs_retry_due_idx
    ON scheduled_jobs (state, next_attempt_at)
    WHERE state = 'retrying';

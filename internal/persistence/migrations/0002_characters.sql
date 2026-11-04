CREATE TABLE characters (
    id TEXT NOT NULL PRIMARY KEY CHECK (
        length(id) = 32
        AND length(CAST(id AS BLOB)) = 32
        AND id NOT GLOB '*[^0-9a-f]*'
    ),
    user_id INTEGER NOT NULL UNIQUE CHECK (user_id > 0),
    max_health INTEGER NOT NULL CHECK (max_health > 0),
    health INTEGER NOT NULL CHECK (health >= 0 AND health <= max_health),
    attack INTEGER NOT NULL CHECK (attack >= 0),
    defense INTEGER NOT NULL CHECK (defense >= 0),
    recover_at_ms INTEGER,
    recovery_health INTEGER,
    revision INTEGER NOT NULL CHECK (revision >= 1),
    CHECK (
        (health > 0 AND recover_at_ms IS NULL AND recovery_health IS NULL)
        OR
        (health = 0 AND recover_at_ms IS NOT NULL AND recovery_health IS NOT NULL
            AND recovery_health >= 1 AND recovery_health <= max_health)
    )
) STRICT;

CREATE TABLE character_operations (
    operation_id TEXT NOT NULL PRIMARY KEY CHECK (length(CAST(operation_id AS BLOB)) BETWEEN 1 AND 128),
    character_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('damage', 'heal')),
    amount INTEGER NOT NULL CHECK (amount >= 0),
    applied_revision INTEGER NOT NULL CHECK (applied_revision >= 1),
    FOREIGN KEY (character_id) REFERENCES characters(id) ON DELETE RESTRICT
) STRICT;

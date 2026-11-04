CREATE TABLE group_settings (
 chat_id INTEGER PRIMARY KEY CHECK(chat_id<0),
 locale TEXT NOT NULL CHECK(locale IN ('en','ru')),
 revision INTEGER NOT NULL CHECK(revision>=1),
 desired_menu_hash TEXT NOT NULL CHECK(length(desired_menu_hash)=64 AND desired_menu_hash NOT GLOB '*[^0-9a-f]*'),
 applied_menu_hash TEXT CHECK(applied_menu_hash IS NULL OR (length(applied_menu_hash)=64 AND applied_menu_hash NOT GLOB '*[^0-9a-f]*')),
 applied_revision INTEGER CHECK(applied_revision IS NULL OR applied_revision>=1),
 menu_next_attempt_at INTEGER NOT NULL,
 menu_attempts INTEGER NOT NULL CHECK(menu_attempts>=0),
 menu_failure_code TEXT CHECK(menu_failure_code IS NULL OR menu_failure_code IN ('transient','rate_limited','forbidden','invalid','indeterminate')),
 updated_at INTEGER NOT NULL,
 CHECK((applied_menu_hash IS NULL)=(applied_revision IS NULL))
) STRICT;
CREATE INDEX group_settings_menu_due ON group_settings(menu_next_attempt_at,chat_id);
CREATE TABLE settings_controls (
 token TEXT PRIMARY KEY NOT NULL CHECK(length(token)=32 AND token NOT GLOB '*[^0-9a-f]*'),
 chat_id INTEGER NOT NULL REFERENCES group_settings(chat_id),
 actor_id INTEGER NOT NULL CHECK(actor_id>0),
 expected_revision INTEGER NOT NULL CHECK(expected_revision>=1),
 selection TEXT NOT NULL CHECK(selection IN ('en','ru')),
 expires_at INTEGER NOT NULL,
 consumed INTEGER NOT NULL CHECK(consumed IN (0,1))
) STRICT;
CREATE INDEX settings_controls_expiry ON settings_controls(expires_at);

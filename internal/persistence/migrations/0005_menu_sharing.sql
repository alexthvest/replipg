CREATE TABLE sharing_views (
 token TEXT PRIMARY KEY NOT NULL CHECK(length(token)=32 AND token NOT GLOB '*[^0-9a-f]*'),
 owner_id INTEGER NOT NULL CHECK(owner_id>0),
 chat_id INTEGER NOT NULL CHECK(chat_id<0),
 chat_kind TEXT NOT NULL CHECK(chat_kind IN ('group','supergroup')),
 view_kind TEXT NOT NULL CHECK(view_kind IN ('character_summary','inventory_page','item_details')),
 locale TEXT NOT NULL CHECK(locale IN ('en','ru')),
 fingerprint_version INTEGER NOT NULL CHECK(fingerprint_version=1),
 retired INTEGER NOT NULL DEFAULT 0 CHECK(retired IN (0,1)),
 body_hash BLOB,
 bound_message_id INTEGER CHECK(bound_message_id IS NULL OR bound_message_id>0),
 usable_until INTEGER,
 created_at INTEGER NOT NULL,
 last_success_at INTEGER,
 unknown_until INTEGER,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),
 CHECK((retired=0 AND body_hash IS NOT NULL AND length(body_hash)=32) OR (retired=1 AND body_hash IS NULL))
) STRICT;
CREATE INDEX sharing_views_expiry ON sharing_views(usable_until,token) WHERE retired=0 AND usable_until IS NOT NULL;
CREATE TABLE sharing_actions (
 update_id INTEGER PRIMARY KEY CHECK(update_id>=0),
 view_token TEXT NOT NULL REFERENCES sharing_views(token),
 public_delivery_id TEXT NOT NULL UNIQUE CHECK(length(public_delivery_id)>0),
 public_state TEXT NOT NULL DEFAULT 'pending' CHECK(public_state IN ('pending','succeeded','failed','unknown')),
 accepted_at INTEGER NOT NULL,
 public_outcome_at INTEGER,
 next_check_at INTEGER,
 callback_query_id TEXT CHECK(callback_query_id IS NULL OR length(callback_query_id)>0),
 callback_deadline INTEGER,
 feedback_delivery_id TEXT UNIQUE CHECK(feedback_delivery_id IS NULL OR length(feedback_delivery_id)>0),
 feedback_state TEXT NOT NULL DEFAULT 'waiting' CHECK(feedback_state IN ('waiting','queued','skipped','completed','rejected','unknown')),
 feedback_outcome_at INTEGER,
 public_purge_after INTEGER,
 feedback_purge_after INTEGER,
 public_purged INTEGER NOT NULL DEFAULT 0 CHECK(public_purged IN (0,1)),
 feedback_purged INTEGER NOT NULL DEFAULT 0 CHECK(feedback_purged IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),
 CHECK((public_state='pending' AND public_outcome_at IS NULL AND public_purge_after IS NULL AND public_purged=0) OR (public_state<>'pending' AND public_outcome_at IS NOT NULL AND public_purge_after IS NOT NULL AND public_purge_after>=public_outcome_at+86400000)),
 CHECK((callback_query_id IS NULL)=(callback_deadline IS NULL)),
 CHECK(feedback_state='waiting' OR (callback_query_id IS NULL AND callback_deadline IS NULL)),
 CHECK((feedback_state IN ('waiting','skipped') AND feedback_delivery_id IS NULL AND feedback_outcome_at IS NULL AND feedback_purge_after IS NULL) OR (feedback_state='queued' AND feedback_delivery_id IS NOT NULL AND feedback_outcome_at IS NULL AND feedback_purge_after IS NULL AND feedback_purged=0) OR (feedback_state IN ('completed','rejected','unknown') AND feedback_delivery_id IS NOT NULL AND feedback_outcome_at IS NOT NULL AND feedback_purge_after IS NOT NULL AND feedback_purge_after>=feedback_outcome_at+86400000)),
 CHECK(feedback_purged=0 OR feedback_state IN ('skipped','completed','rejected','unknown')),
 CHECK(next_check_at IS NOT NULL OR (public_purged=1 AND (feedback_purged=1 OR feedback_state='skipped')))
) STRICT;
CREATE UNIQUE INDEX sharing_actions_pending_view ON sharing_actions(view_token) WHERE public_state='pending';
CREATE INDEX sharing_actions_due ON sharing_actions(next_check_at,update_id) WHERE next_check_at IS NOT NULL;
CREATE INDEX sharing_actions_view ON sharing_actions(view_token,update_id);

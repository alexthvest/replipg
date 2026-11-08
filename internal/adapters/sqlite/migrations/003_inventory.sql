CREATE TABLE item_snapshots (
 id TEXT PRIMARY KEY CHECK (length(id)>0),
 catalog_id TEXT NOT NULL,
 canonical_json BLOB NOT NULL UNIQUE,
 digest TEXT NOT NULL
);
CREATE INDEX snapshots_by_digest ON item_snapshots(digest);
CREATE TABLE inventory_entries (
 id TEXT PRIMARY KEY CHECK (length(id)>0),
 group_id INTEGER NOT NULL,
 character_id TEXT NOT NULL,
 snapshot_id TEXT NOT NULL REFERENCES item_snapshots(id),
 mode TEXT NOT NULL CHECK (mode IN ('stackable','individual')),
 quantity INTEGER NOT NULL CHECK (typeof(quantity)='integer' AND quantity>0),
 CHECK (mode='stackable' OR quantity=1),
 FOREIGN KEY(group_id,character_id) REFERENCES characters(group_id,id)
);
CREATE UNIQUE INDEX unique_stacks ON inventory_entries(group_id,character_id,snapshot_id) WHERE mode='stackable';
CREATE TABLE grant_operations (
 bot_id INTEGER NOT NULL CHECK (bot_id>0),
 update_id INTEGER NOT NULL CHECK (update_id>0),
 group_id INTEGER NOT NULL REFERENCES groups(group_id),
 result_json BLOB NOT NULL,
 PRIMARY KEY(bot_id,update_id)
);

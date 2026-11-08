CREATE TABLE characters (
 id TEXT PRIMARY KEY CHECK (length(id)>0),
 group_id INTEGER NOT NULL REFERENCES groups(group_id),
 user_id INTEGER NOT NULL CHECK (user_id > 0),
 name TEXT NOT NULL CHECK (length(name)>0),
 UNIQUE(group_id,user_id),
 UNIQUE(group_id,id)
);

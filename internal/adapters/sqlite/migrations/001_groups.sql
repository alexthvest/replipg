CREATE TABLE groups (
 group_id INTEGER PRIMARY KEY CHECK (group_id < 0),
 language TEXT NOT NULL DEFAULT 'en' CHECK (language IN ('en','ru'))
);
CREATE TABLE game_admins (
 group_id INTEGER NOT NULL REFERENCES groups(group_id),
 user_id INTEGER NOT NULL CHECK (user_id > 0),
 PRIMARY KEY (group_id,user_id)
);

CREATE TABLE crafting_recipes (
 id TEXT PRIMARY KEY CHECK(length(id)>0),
 digest TEXT NOT NULL,
 canonical BLOB NOT NULL UNIQUE,
 ingredients BLOB NOT NULL,
 outcome BLOB
);
CREATE INDEX crafting_recipe_digest ON crafting_recipes(digest);
CREATE TRIGGER crafting_outcome_immutable BEFORE UPDATE OF outcome ON crafting_recipes
WHEN OLD.outcome IS NOT NULL
BEGIN SELECT RAISE(ABORT,'crafting outcome is immutable'); END;
CREATE TRIGGER crafting_recipe_immutable BEFORE UPDATE OF id,digest,canonical,ingredients ON crafting_recipes
BEGIN SELECT RAISE(ABORT,'crafting identity is immutable'); END;
CREATE TABLE crafting_attempts (
 id TEXT PRIMARY KEY CHECK(length(id)>0),
 confirmation TEXT NOT NULL UNIQUE CHECK(length(confirmation)>0),
 recipe_id TEXT NOT NULL REFERENCES crafting_recipes(id),
 group_id INTEGER NOT NULL,
 character_id TEXT NOT NULL,
 user_id INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','success','uncraftable','unavailable','technical-failure')),
 intent BLOB NOT NULL,
 result BLOB NOT NULL,
 FOREIGN KEY(group_id,character_id,user_id) REFERENCES characters(group_id,id,user_id)
);
CREATE TRIGGER crafting_attempt_intent BEFORE UPDATE OF id,confirmation,recipe_id,group_id,character_id,user_id,intent ON crafting_attempts
BEGIN SELECT RAISE(ABORT,'crafting attempt intent is immutable'); END;
CREATE TRIGGER crafting_attempt_terminal BEFORE UPDATE ON crafting_attempts WHEN OLD.state!='pending'
BEGIN SELECT RAISE(ABORT,'crafting attempt is terminal'); END;
CREATE TABLE crafting_jobs (
 recipe_id TEXT PRIMARY KEY REFERENCES crafting_recipes(id),
 token TEXT NOT NULL DEFAULT '',
 expires INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','done','failed'))
);

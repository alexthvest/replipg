-- Old rejected attempts remain historical; their recipes can be discovered anew.
DROP TRIGGER crafting_outcome_immutable;

UPDATE crafting_jobs SET state='pending',token='',expires=0
WHERE recipe_id IN (
 SELECT id FROM crafting_recipes
 WHERE json_extract(CAST(outcome AS TEXT),'$.kind')='uncraftable'
);

UPDATE crafting_recipes SET outcome=NULL
WHERE json_extract(CAST(outcome AS TEXT),'$.kind')='uncraftable';

CREATE TRIGGER crafting_outcome_immutable BEFORE UPDATE OF outcome ON crafting_recipes
WHEN OLD.outcome IS NOT NULL
BEGIN SELECT RAISE(ABORT,'crafting outcome is immutable'); END;

CREATE TRIGGER crafting_success_insert BEFORE INSERT ON crafting_recipes
WHEN NEW.outcome IS NOT NULL AND
 (NOT json_valid(CAST(NEW.outcome AS TEXT)) OR
 COALESCE(json_extract(CAST(NEW.outcome AS TEXT),'$.kind'),'')!='success')
BEGIN SELECT RAISE(ABORT,'crafting requires a successful outcome'); END;

CREATE TRIGGER crafting_success_update BEFORE UPDATE OF outcome ON crafting_recipes
WHEN NEW.outcome IS NOT NULL AND
 (NOT json_valid(CAST(NEW.outcome AS TEXT)) OR
 COALESCE(json_extract(CAST(NEW.outcome AS TEXT),'$.kind'),'')!='success')
BEGIN SELECT RAISE(ABORT,'crafting requires a successful outcome'); END;

CREATE TRIGGER crafting_success_attempt_insert BEFORE INSERT ON crafting_attempts
WHEN NEW.state='uncraftable'
BEGIN SELECT RAISE(ABORT,'crafting cannot reject ingredients'); END;

CREATE TRIGGER crafting_success_attempt_update BEFORE UPDATE OF state ON crafting_attempts
WHEN NEW.state='uncraftable'
BEGIN SELECT RAISE(ABORT,'crafting cannot reject ingredients'); END;

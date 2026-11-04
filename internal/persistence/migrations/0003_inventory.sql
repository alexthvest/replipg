CREATE TABLE inventory_catalog (
 item_id TEXT NOT NULL PRIMARY KEY CHECK(length(CAST(item_id AS BLOB)) BETWEEN 1 AND 64 AND item_id NOT GLOB '*[^A-Za-z0-9_-]*' AND instr(item_id,char(0))=0),
 category TEXT NOT NULL CHECK(category IN ('consumable','weapon','armor','inert')),
 attack_bonus INTEGER NOT NULL CHECK(attack_bonus>=0),
 defense_bonus INTEGER NOT NULL CHECK(defense_bonus>=0),
 healing_hp INTEGER NOT NULL CHECK(healing_hp>=0),
 CHECK((category='consumable' AND healing_hp>0 AND attack_bonus=0 AND defense_bonus=0)
 OR (category='weapon' AND defense_bonus=0 AND healing_hp=0)
 OR (category='armor' AND attack_bonus=0 AND healing_hp=0)
 OR (category='inert' AND attack_bonus=0 AND defense_bonus=0 AND healing_hp=0))
) STRICT;
CREATE TABLE inventories (
 character_id TEXT NOT NULL PRIMARY KEY REFERENCES characters(id) ON DELETE RESTRICT,
 revision INTEGER NOT NULL CHECK(revision>=1),
 starter_granted INTEGER NOT NULL CHECK(starter_granted IN (0,1))
) STRICT;
CREATE TABLE inventory_items (
 character_id TEXT NOT NULL REFERENCES inventories(character_id) ON DELETE RESTRICT,
 item_id TEXT NOT NULL REFERENCES inventory_catalog(item_id) ON DELETE RESTRICT,
 quantity INTEGER NOT NULL CHECK(quantity>0),
 PRIMARY KEY(character_id,item_id)
) STRICT;
CREATE TABLE inventory_equipment (
 character_id TEXT NOT NULL,
 slot TEXT NOT NULL CHECK(slot IN ('weapon','armor')),
 item_id TEXT NOT NULL,
 PRIMARY KEY(character_id,slot),
 FOREIGN KEY(character_id,item_id) REFERENCES inventory_items(character_id,item_id) ON DELETE RESTRICT
) STRICT;
CREATE TRIGGER inventory_equipment_insert BEFORE INSERT ON inventory_equipment
WHEN NOT EXISTS(SELECT 1 FROM inventory_catalog WHERE item_id=NEW.item_id AND category=NEW.slot)
BEGIN SELECT RAISE(ABORT,'incompatible equipment'); END;
CREATE TRIGGER inventory_equipment_update BEFORE UPDATE ON inventory_equipment
WHEN NOT EXISTS(SELECT 1 FROM inventory_catalog WHERE item_id=NEW.item_id AND category=NEW.slot)
BEGIN SELECT RAISE(ABORT,'incompatible equipment'); END;
CREATE TABLE inventory_operations (
 operation_id TEXT NOT NULL PRIMARY KEY CHECK(length(CAST(operation_id AS BLOB)) BETWEEN 1 AND 128),
 character_id TEXT NOT NULL REFERENCES characters(id) ON DELETE RESTRICT,
 kind TEXT NOT NULL CHECK(kind IN ('starter','grant','equipment','consume')),
 canonical_request TEXT NOT NULL CHECK(length(canonical_request)>0),
 applied_revision INTEGER NOT NULL CHECK(applied_revision>=1),
 effect_kind TEXT,
 healing_hp INTEGER,
 CHECK((kind='consume' AND effect_kind IS NOT NULL AND effect_kind='heal_self_fixed_hp' AND healing_hp IS NOT NULL AND healing_hp>0)
 OR (kind<>'consume' AND effect_kind IS NULL AND healing_hp IS NULL))
) STRICT;
CREATE TABLE inventory_controls (
 token TEXT NOT NULL PRIMARY KEY CHECK(length(CAST(token AS BLOB))=22 AND token NOT GLOB '*[^A-Za-z0-9_-]*' AND instr(token,char(0))=0),
 user_id INTEGER NOT NULL CHECK(user_id>0),
 chat_id INTEGER NOT NULL CHECK(chat_id<0),
 character_id TEXT NOT NULL REFERENCES characters(id) ON DELETE RESTRICT,
 action TEXT NOT NULL CHECK(action IN ('page','detail','equip','unequip')),
 item_id TEXT REFERENCES inventory_catalog(item_id) ON DELETE RESTRICT,
 slot TEXT CHECK(slot IN ('weapon','armor')),
 page INTEGER NOT NULL CHECK(page>=0),
 expected_revision INTEGER NOT NULL CHECK(expected_revision>=1),
 expires_at_ms INTEGER NOT NULL CHECK(expires_at_ms>0),
 CHECK((action='page' AND item_id IS NULL AND slot IS NULL)
 OR (action='detail' AND item_id IS NOT NULL AND slot IS NULL)
 OR (action IN ('equip','unequip') AND item_id IS NOT NULL AND slot IS NOT NULL))
) STRICT;
CREATE INDEX inventory_controls_expiry ON inventory_controls(expires_at_ms);

CREATE UNIQUE INDEX character_shop_ownership ON characters(group_id,id,user_id);
CREATE UNIQUE INDEX inventory_shop_ownership ON inventory_entries(group_id,character_id,id);

CREATE TABLE shop_orders (
 id TEXT PRIMARY KEY CHECK (length(id)>0),
 confirmation TEXT NOT NULL UNIQUE CHECK (length(confirmation)>0),
 group_id INTEGER NOT NULL,
 character_id TEXT NOT NULL,
 user_id INTEGER NOT NULL CHECK (user_id>0),
 direction TEXT NOT NULL CHECK (direction IN ('buy','sell')),
 quote_json BLOB NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('created','requesting','awaiting-payment','paid','reserved','payout-requesting','payout-confirmed','completed','rejected','failed','reconciliation-required')),
 diagnostic TEXT NOT NULL DEFAULT '',
 bank_id INTEGER NOT NULL CHECK (bank_id>0),
 treasury_id INTEGER NOT NULL CHECK (treasury_id>0),
 bot_id INTEGER NOT NULL CHECK (bot_id>0),
 update_id INTEGER NOT NULL CHECK (update_id>0),
 next_attempt INTEGER NOT NULL,
 UNIQUE(bot_id,update_id),
 UNIQUE(group_id,character_id,id),
 FOREIGN KEY(group_id,character_id,user_id) REFERENCES characters(group_id,id,user_id)
);
CREATE INDEX shop_orders_due ON shop_orders(state,next_attempt);
CREATE INDEX shop_orders_owner ON shop_orders(group_id,user_id,id);

CREATE TABLE financial_requests (
 transport_id TEXT PRIMARY KEY CHECK (length(transport_id)>0),
 order_id TEXT NOT NULL REFERENCES shop_orders(id),
 financial_key TEXT NOT NULL CHECK (length(financial_key)>0),
 kind TEXT NOT NULL,
 method TEXT NOT NULL,
 params BLOB NOT NULL,
 next_attempt INTEGER NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts>=0),
 unknown INTEGER NOT NULL DEFAULT 0 CHECK (unknown IN (0,1)),
 payment_id TEXT NOT NULL DEFAULT '',
 sender_account_id INTEGER NOT NULL DEFAULT 0 CHECK (sender_account_id>=0),
 outcome TEXT NOT NULL DEFAULT '',
 response BLOB,
 UNIQUE(order_id,transport_id)
);
CREATE INDEX financial_requests_order ON financial_requests(order_id);
CREATE INDEX financial_requests_due ON financial_requests(next_attempt);

CREATE TABLE bank_account_bindings (
 bank_id INTEGER NOT NULL CHECK (bank_id>0),
 user_id INTEGER NOT NULL CHECK (user_id>0),
 account_id INTEGER NOT NULL CHECK (account_id BETWEEN 1 AND 2147483647),
 PRIMARY KEY(bank_id,user_id),
 UNIQUE(bank_id,account_id)
);

CREATE TRIGGER bank_account_binding_immutable BEFORE UPDATE ON bank_account_bindings
BEGIN SELECT RAISE(ABORT,'learned bank account binding is immutable'); END;

CREATE TABLE financial_attempts (
 id INTEGER PRIMARY KEY,
 transport_id TEXT NOT NULL REFERENCES financial_requests(transport_id),
 attempted_at INTEGER NOT NULL,
 outcome TEXT NOT NULL,
 diagnostic TEXT NOT NULL DEFAULT ''
);

CREATE TABLE financial_proofs (
 bank_id INTEGER NOT NULL CHECK (bank_id>0),
 transaction_id TEXT NOT NULL CHECK (length(transaction_id)>0),
 order_id TEXT NOT NULL UNIQUE REFERENCES shop_orders(id),
 transport_id TEXT NOT NULL,
 response BLOB NOT NULL,
 PRIMARY KEY(bank_id,transaction_id),
 FOREIGN KEY(order_id,transport_id) REFERENCES financial_requests(order_id,transport_id)
);

CREATE TABLE inventory_reservations (
 order_id TEXT PRIMARY KEY,
 group_id INTEGER NOT NULL,
 character_id TEXT NOT NULL,
 entry_id TEXT NOT NULL,
 quantity INTEGER NOT NULL CHECK (typeof(quantity)='integer' AND quantity BETWEEN 1 AND 1000),
 FOREIGN KEY(group_id,character_id,order_id) REFERENCES shop_orders(group_id,character_id,id),
 FOREIGN KEY(group_id,character_id,entry_id) REFERENCES inventory_entries(group_id,character_id,id)
);
CREATE INDEX reservations_entry ON inventory_reservations(entry_id);

CREATE TRIGGER shop_order_intent_immutable BEFORE UPDATE OF confirmation,group_id,character_id,user_id,direction,quote_json,bank_id,treasury_id,bot_id,update_id ON shop_orders
BEGIN SELECT RAISE(ABORT,'shop order intent is immutable'); END;

CREATE TRIGGER financial_request_intent_immutable BEFORE UPDATE OF transport_id,order_id,financial_key,kind,method,params ON financial_requests
BEGIN SELECT RAISE(ABORT,'financial request intent is immutable'); END;

CREATE TRIGGER financial_key_intent_insert BEFORE INSERT ON financial_requests
WHEN EXISTS(SELECT 1 FROM financial_requests WHERE financial_key=NEW.financial_key
 AND (order_id!=NEW.order_id OR (method=NEW.method AND params!=NEW.params)))
BEGIN SELECT RAISE(ABORT,'financial key already binds another intent'); END;

CREATE TRIGGER reservation_quantity_insert BEFORE INSERT ON inventory_reservations
WHEN NEW.quantity + COALESCE((SELECT SUM(quantity) FROM inventory_reservations WHERE entry_id=NEW.entry_id),0)
 > COALESCE((SELECT quantity FROM inventory_entries WHERE id=NEW.entry_id AND group_id=NEW.group_id AND character_id=NEW.character_id),0)
BEGIN SELECT RAISE(ABORT,'inventory quantity already reserved'); END;

CREATE TRIGGER reservation_immutable BEFORE UPDATE ON inventory_reservations
BEGIN SELECT RAISE(ABORT,'inventory reservation is immutable'); END;

CREATE TRIGGER inventory_reservation_quantity BEFORE UPDATE OF quantity ON inventory_entries
WHEN NEW.quantity < COALESCE((SELECT SUM(quantity) FROM inventory_reservations WHERE entry_id=OLD.id),0)
BEGIN SELECT RAISE(ABORT,'inventory quantity is reserved'); END;

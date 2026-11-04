package sharing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/inventory"
	inventorycontract "github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/telegram"
)

// This fixture composes production handlers, source mapping and sharing storage.
// Only the delivery ledger and personal display are test-owned boundaries.
type inventorySourceFixture struct {
	db         *persistence.DB
	reg        *interaction.Registry
	router     *interaction.Router
	service    *Service
	items      *inventory.Service
	characters *character.Service
	ledger     *callbackLedger
	now        time.Time
	locale     localization.Locale
	next       int64
}

func newInventorySourceFixture(t *testing.T, resources *localization.Catalog) *inventorySourceFixture {
	t.Helper()
	f := &inventorySourceFixture{db: sharingTestDB(t), now: time.Unix(1000, 0).UTC(), locale: localization.English, ledger: &callbackLedger{evidence: map[runtimecontract.DeliveryID]Evidence{}}}
	if resources == nil {
		var err error
		resources, err = localization.Load()
		if err != nil {
			t.Fatal(err)
		}
	}
	clock := func() time.Time { return f.now }
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	f.characters, err = character.NewService(cfg, clock)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := inventory.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	f.items, err = inventory.NewService(catalog, f.characters, clock)
	if err != nil {
		t.Fatal(err)
	}
	token := 0
	f.service, err = NewService(resources, ServiceOptions{Now: clock, NewToken: func() (string, error) { token++; return fmt.Sprintf("%032x", token), nil }})
	if err != nil {
		t.Fatal(err)
	}
	resolve := func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return f.locale, nil
	}
	projector, err := NewProjector(f.db, f.ledger, resources, resolve, clock)
	if err != nil {
		t.Fatal(err)
	}
	f.reg = interaction.NewRegistry()
	if err = inventory.Register(f.reg, f.items, clock, inventory.Presentation{Catalog: resources, Resolve: resolve, Sharing: f.service}); err != nil {
		t.Fatal(err)
	}
	if err = Register(f.reg, f.service, f.ledger, projector.Refresh, resolve); err != nil {
		t.Fatal(err)
	}
	f.reg.Seal()
	f.router = interaction.NewRouter(f.reg, f.db, f.db, f.ledger, nil, interaction.RouterOptions{Now: clock, Diagnostics: inventorySourceDiagnostics{}})
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE callback_deliveries(id INTEGER PRIMARY KEY,kind TEXT,payload BLOB)`); err != nil {
			return err
		}
		return f.items.Prepare(ctx, tx)
	})
	return f
}
func (f *inventorySourceFixture) dispatch(t *testing.T, event interaction.Event) interaction.HandlerResult {
	t.Helper()
	result, err := f.router.Dispatch(context.Background(), event)
	if err != nil || result.Status != persistence.UpdateCompleted {
		t.Fatalf("production dispatch: status=%s error=%v", result.Status, err)
	}
	return interaction.HandlerResult{ImmediateResponses: result.ImmediateResponses}
}

type inventorySourceDiagnostics struct{}

func (inventorySourceDiagnostics) Emit(context.Context, observability.Diagnostic) {}

type inventorySourceDisplay struct {
	visible     runtimecontract.MessageContent
	attempt     runtimecontract.EphemeralResponse
	ambiguous   bool
	publicCalls int
}

func (d *inventorySourceDisplay) SendPublic(context.Context, runtimecontract.PublicResponse) (runtimecontract.DeliveryReceipt, error) {
	d.publicCalls++
	return runtimecontract.DeliveryReceipt{}, errors.New("unexpected public fallback")
}
func (d *inventorySourceDisplay) AcknowledgeCallback(context.Context, runtimecontract.CallbackAcknowledgement) error {
	return nil
}
func (d *inventorySourceDisplay) SendEphemeral(_ context.Context, response runtimecontract.EphemeralResponse) (runtimecontract.DeliveryReceipt, error) {
	d.attempt = response
	if d.ambiguous {
		d.visible = response.Content
		return runtimecontract.DeliveryReceipt{}, &telegram.APIError{Kind: telegram.ErrorIndeterminate}
	}
	return runtimecontract.DeliveryReceipt{}, &telegram.APIError{Kind: telegram.ErrorPermanent}
}
func (f *inventorySourceFixture) personal(t *testing.T, event interaction.Event) runtimecontract.EphemeralResponse {
	t.Helper()
	r := f.dispatch(t, event)
	if len(r.ImmediateResponses) != 1 {
		t.Fatalf("expected one personal view: %+v", r)
	}
	p, ok := r.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
	if !ok {
		t.Fatal("inventory became public")
	}
	if err := runtimecontract.ValidateResponseAt(p, f.now); err != nil {
		t.Fatal(err)
	}
	if p.TargetUserID != 42 || p.Destination.ChatID != -1 {
		t.Fatal("personal ownership changed")
	}
	if len(f.publics(t)) != 0 {
		t.Fatal("browsing published a snapshot")
	}
	return p
}
func (f *inventorySourceFixture) open(t *testing.T) runtimecontract.EphemeralResponse {
	t.Helper()
	f.next++
	return f.personal(t, interaction.Command{Identity: interaction.Identity{Update: runtimecontract.UpdateID(f.next), User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "inventory", EphemeralMessageID: 23, SentAt: f.now})
}
func inventorySourceButton(t *testing.T, body runtimecontract.MessageContent, label string) string {
	t.Helper()
	for _, row := range body.Buttons {
		for _, b := range row {
			if b.Text == label || (label == "share" && strings.HasPrefix(b.CallbackData, sharePrefix)) {
				return b.CallbackData
			}
		}
	}
	t.Fatalf("missing button %q", label)
	return ""
}
func (f *inventorySourceFixture) callback(t *testing.T, body runtimecontract.MessageContent, data string, change func(map[string]any)) telegram.DecodedUpdate {
	t.Helper()
	f.next++
	entities := []map[string]any{}
	for _, e := range body.Entities {
		entities = append(entities, map[string]any{"type": string(e.Kind), "offset": e.Offset, "length": e.Length, "url": e.URL})
	}
	keyboard := [][]map[string]any{}
	for _, row := range body.Buttons {
		buttons := []map[string]any{}
		for _, b := range row {
			buttons = append(buttons, map[string]any{"text": b.Text, "callback_data": b.CallbackData})
		}
		keyboard = append(keyboard, buttons)
	}
	message := map[string]any{"ephemeral_message_id": 900, "date": f.now.Unix(), "chat": map[string]any{"id": -1, "type": "supergroup"}, "receiver_user": map[string]any{"id": 42}, "text": body.Text, "entities": entities, "reply_markup": map[string]any{"inline_keyboard": keyboard}}
	query := map[string]any{"id": fmt.Sprintf("query-%d", f.next), "from": map[string]any{"id": 42}, "data": data, "message": message}
	if change != nil {
		change(query)
	}
	raw, err := json.Marshal(map[string]any{"update_id": f.next, "callback_query": query})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := telegram.DecodeUpdate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}
func (f *inventorySourceFixture) navigate(t *testing.T, body runtimecontract.MessageContent, label string) runtimecontract.EphemeralResponse {
	t.Helper()
	d := f.callback(t, body, inventorySourceButton(t, body, label), nil)
	return f.personal(t, d.Event)
}
func (f *inventorySourceFixture) publics(t *testing.T) []runtimecontract.PublicResponse {
	t.Helper()
	var result []runtimecontract.PublicResponse
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		rows, err := tx.QueryContext(ctx, `SELECT payload FROM callback_deliveries WHERE kind=? ORDER BY id`, PublicKind)
		if err != nil {
			return err
		}
		defer func() {
			if err := rows.Close(); err != nil {
				t.Error(err)
			}
		}()
		for rows.Next() {
			var data []byte
			if err = rows.Scan(&data); err != nil {
				return err
			}
			p, err := runtimecontract.NewVersionedPayload(1, data)
			if err != nil {
				return err
			}
			r, err := DecodePublic(p)
			if err != nil {
				return err
			}
			result = append(result, r.(runtimecontract.PublicResponse))
		}
		return rows.Err()
	})
	return result
}

// Capture every gameplay row, including operation/recovery and starter metadata,
// without calling an observing service which might itself normalize recovery.
func (f *inventorySourceFixture) gameplay(t *testing.T) string {
	t.Helper()
	state := map[string][][]any{}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for _, table := range []string{"characters", "character_operations", "inventories", "inventory_items", "inventory_equipment", "inventory_operations", "inventory_controls"} {
			rows, err := tx.QueryContext(ctx, "SELECT * FROM "+table+" ORDER BY 1,2")
			if err != nil {
				return err
			}
			cols, err := rows.Columns()
			if err != nil {
				_ = rows.Close()
				return err
			}
			for rows.Next() {
				values := make([]any, len(cols))
				dest := make([]any, len(cols))
				for i := range values {
					dest[i] = &values[i]
				}
				if err = rows.Scan(dest...); err != nil {
					_ = rows.Close()
					return err
				}
				state[table] = append(state[table], values)
			}
			err = errors.Join(rows.Err(), rows.Close())
			if err != nil {
				return err
			}
		}
		return nil
	})
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func (f *inventorySourceFixture) shareExact(t *testing.T, body runtimecontract.MessageContent, locale localization.Locale) {
	t.Helper()
	before := f.gameplay(t)
	count := len(f.publics(t))
	d := f.callback(t, body, inventorySourceButton(t, body, "share"), nil)
	if d.Status != telegram.DecodeMapped {
		t.Fatalf("source mapping=%s", d.Status)
	}
	f.dispatch(t, d.Event)
	got := f.publics(t)
	if len(got) != count+1 {
		t.Fatal("selected source did not produce exactly one snapshot")
	}
	p := got[len(got)-1]
	footer, err := Attribution(f.service.catalog, locale, 42)
	if err != nil {
		t.Fatal(err)
	}
	if p.Content.Text != body.Text+"\n\n"+footer || !reflect.DeepEqual(p.Content.Entities, body.Entities) || len(p.Content.Buttons) != 0 || p.Destination.ChatID != -1 {
		t.Fatalf("snapshot differs from selected source: %+v", p)
	}
	if before != f.gameplay(t) {
		t.Fatal("sharing changed gameplay")
	}
}

func TestInventorySourceSelectedViewsAndFrozenState(t *testing.T) {
	f := newInventorySourceFixture(t, nil)
	page := f.open(t)
	// Item ordering is defined by the real catalog/store; select Stick through
	// the actual persisted control rather than assuming the numbered position.
	var number string
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		return tx.QueryRowContext(ctx, `SELECT token FROM inventory_controls WHERE item_id='stick' AND action='detail'`).Scan(&number)
	})
	d := f.callback(t, page.Content, "inv:"+number, nil)
	detail := f.personal(t, d.Event)
	equipped := f.navigate(t, detail.Content, "Equip")
	if equipped.EditMessageID != 900 || !strings.Contains(equipped.Content.Text, "Equipped") {
		t.Fatal("equipment edit was not rendered")
	}
	back := f.navigate(t, equipped.Content, "Back")
	if back.Content.Text == page.Content.Text {
		t.Fatal("Back did not show changed equipment")
	}
	// Later game and locale changes must not replace any selected old source.
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		opened, err := f.items.Open(ctx, tx, 42)
		if err != nil {
			return err
		}
		_, err = f.items.Grant(ctx, tx, inventorycontract.GrantRequest{OperationID: "test:later-grant", CharacterID: opened.Current.CharacterID, Items: []inventorycontract.GrantLine{{ItemID: "coin", Quantity: 17}}})
		return err
	})
	f.locale = localization.Russian
	for _, view := range []runtimecontract.MessageContent{page.Content, detail.Content, equipped.Content, back.Content} {
		f.shareExact(t, view, localization.English)
	}
}

func TestInventorySourceFailedEditsKeepEachTokenBoundToItsBody(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprintf("ambiguous=%t", ambiguous), func(t *testing.T) {
			f := newInventorySourceFixture(t, nil)
			old := f.open(t)
			display := &inventorySourceDisplay{visible: old.Content, ambiguous: ambiguous}
			processor, err := telegram.NewProcessor(f.router, display, telegram.ProcessorOptions{Diagnostics: inventorySourceDiagnostics{}})
			if err != nil {
				t.Fatal(err)
			}
			update := f.callback(t, old.Content, inventorySourceButton(t, old.Content, "1"), nil)
			if err = processor.ProcessUpdate(context.Background(), update); err != nil {
				t.Fatal(err)
			}
			replacement := display.attempt
			if replacement.EditMessageID != 900 {
				t.Fatal("failed delivery did not attempt original personal edit")
			}
			if replacement.Content.Text == old.Content.Text {
				t.Fatal("edit did not change source")
			}
			// The handler transaction commits before transport attempts the edit.
			// A definite rejection leaves the old display; a lost acknowledgement may
			// leave either display. Model both observable client results explicitly.
			visible := display.visible
			if len(f.publics(t)) != 0 || display.publicCalls != 0 {
				t.Fatal("failed edit published fallback")
			}
			chosen := inventorySourceButton(t, visible, "share")
			wrong := replacement.Content
			if ambiguous {
				wrong = old.Content
			}
			before := f.gameplay(t)
			rejected := f.callback(t, wrong, chosen, func(q map[string]any) {
				m := q["message"].(map[string]any)
				m["reply_markup"] = map[string]any{"inline_keyboard": [][]map[string]any{{{"text": "Share", "callback_data": chosen}}}}
			})
			f.dispatch(t, rejected.Event)
			if len(f.publics(t)) != 0 || before != f.gameplay(t) {
				t.Fatal("token accepted the other edit's body")
			}
			f.shareExact(t, old.Content, localization.English)
			f.shareExact(t, replacement.Content, localization.English)
		})
	}
}

func TestInventorySourceMapperAuthorizationAndExpiry(t *testing.T) {
	for _, name := range []string{"keyboard", "body", "format-fingerprint", "actor", "bot", "group", "receiver", "message", "unsupported-format", "expired", "inaccessible"} {
		t.Run(name, func(t *testing.T) {
			f := newInventorySourceFixture(t, nil)
			page := f.open(t)
			// Edited details have a known message binding rather than first-use binding.
			view := f.navigate(t, page.Content, "1").Content
			if name == "expired" {
				f.now = f.now.Add(5 * time.Minute)
				f.locale = localization.Russian
			}
			before := f.gameplay(t)
			decoded := f.callback(t, view, inventorySourceButton(t, view, "share"), func(q map[string]any) {
				m := q["message"].(map[string]any)
				switch name {
				case "keyboard":
					delete(m, "reply_markup")
				case "body":
					m["text"] = view.Text + " changed"
				case "format-fingerprint":
					m["entities"] = []map[string]any{{"type": "bold", "offset": 0, "length": 1}}
				case "actor":
					q["from"] = map[string]any{"id": 43}
				case "bot":
					q["from"] = map[string]any{"id": 42, "is_bot": true}
				case "group":
					m["chat"] = map[string]any{"id": -2, "type": "supergroup"}
				case "receiver":
					m["receiver_user"] = map[string]any{"id": 43}
				case "message":
					m["ephemeral_message_id"] = 901
				case "unsupported-format":
					m["entities"] = []map[string]any{{"type": "underline", "offset": 0, "length": 1}}
				case "inaccessible":
					m["date"] = 0
				}
			})
			if name == "inaccessible" {
				if decoded.Status != telegram.DecodeIgnored || decoded.Event != nil {
					t.Fatal("inaccessible source reached routing")
				}
				return
			}
			if decoded.Status != telegram.DecodeMapped {
				t.Fatalf("callback mapping=%s", decoded.Status)
			}
			result := f.dispatch(t, decoded.Event)
			if len(f.publics(t)) != 0 || before != f.gameplay(t) {
				t.Fatal("invalid source published or mutated gameplay")
			}
			foreign := name == "actor" || name == "bot" || name == "group" || name == "receiver"
			if foreign {
				if len(result.ImmediateResponses) != 0 {
					t.Fatal("foreign actor received identifying guidance")
				}
				return
			}
			text := feedbackText(t, result)
			if name == "expired" {
				want, _ := f.service.catalog.Text(localization.Russian, localization.SharingReopen, nil)
				if text != want {
					t.Fatalf("expired guidance=%q want=%q", text, want)
				}
				auth := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Authorization.(runtimecontract.CallbackAuthorization)
				if !auth.ValidUntil.Equal(f.now.Add(15 * time.Second)) {
					t.Fatal("expired view did not use fresh callback authorization")
				}
			}
		})
	}
}

func inventorySourceResources(t *testing.T, description string, padding ...int) *localization.Catalog {
	t.Helper()
	en, err := os.ReadFile("../localization/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	ru, err := os.ReadFile("../localization/locales/ru.json")
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]string
	if err = json.Unmarshal(en, &values); err != nil {
		t.Fatal(err)
	}
	values["items.coin.description"] = description
	if len(padding) > 0 {
		values["inventory.detail"] += strings.Repeat("x", padding[0])
	}
	en, err = json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := localization.Parse(en, ru)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
func TestInventorySourceChangedItemPresentation(t *testing.T) {
	resources := inventorySourceResources(t, "Original coin description.")
	f := newInventorySourceFixture(t, resources)
	page := f.open(t)
	detail := f.item(t, page.Content, "coin")
	f.service.catalog = inventorySourceResources(t, "Replacement coin description.")
	// Recompose the inventory presentation to represent a catalog reload, and
	// establish that subsequent production renders really have different text.
	resolve := func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return f.locale, nil
	}
	reg := interaction.NewRegistry()
	if err := inventory.Register(reg, f.items, func() time.Time { return f.now }, inventory.Presentation{Catalog: f.service.catalog, Resolve: resolve, Sharing: f.service}); err != nil {
		t.Fatal(err)
	}
	reg.Seal()
	// The original router retains the registered Share handler and service.
	var latest interaction.HandlerResult
	var token string
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		return tx.QueryRowContext(ctx, `SELECT token FROM inventory_controls WHERE item_id='coin' AND action='detail' ORDER BY rowid DESC LIMIT 1`).Scan(&token)
	})
	data := f.callback(t, page.Content, "inv:"+token, nil)
	h, ok := reg.Resolve(data.Event)
	if !ok {
		t.Fatal("missing reloaded inventory route")
	}
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error
		latest, err = h.Handle(ctx, shareScope{tx}, data.Event)
		return err
	})
	if len(latest.ImmediateResponses) != 1 {
		t.Fatal("missing reloaded inventory view")
	}
	if !strings.Contains(latest.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Text, "Replacement coin description.") {
		t.Fatal("new production view did not use changed item presentation")
	}
	f.shareExact(t, detail.Content, localization.English)
	if !strings.Contains(f.publics(t)[0].Content.Text, "Original coin description.") {
		t.Fatal("changed catalog replaced displayed presentation")
	}
}

func (f *inventorySourceFixture) item(t *testing.T, body runtimecontract.MessageContent, id string) runtimecontract.EphemeralResponse {
	t.Helper()
	var token string
	mustTx(t, f.db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		return tx.QueryRowContext(ctx, `SELECT token FROM inventory_controls WHERE item_id=? AND action='detail' ORDER BY rowid DESC LIMIT 1`, id).Scan(&token)
	})
	return f.personal(t, f.callback(t, body, "inv:"+token, nil).Event)
}
func TestInventorySourceOversizedAttributedSnapshot(t *testing.T) {
	description := strings.Repeat("Coin description. ", 10)
	baseline := newInventorySourceFixture(t, inventorySourceResources(t, description))
	page := baseline.open(t)
	detail := baseline.item(t, page.Content, "coin")
	// A valid real inventory detail leaves insufficient space for attribution.
	resources := inventorySourceResources(t, description, 4090-len(detail.Content.Text))
	f := newInventorySourceFixture(t, resources)
	page = f.open(t)
	detail = f.item(t, page.Content, "coin")
	if len(detail.Content.Text) != 4090 {
		t.Fatalf("fixture body length=%d", len(detail.Content.Text))
	}
	before := f.gameplay(t)
	result := f.dispatch(t, f.callback(t, detail.Content, inventorySourceButton(t, detail.Content, "share"), nil).Event)
	if len(f.publics(t)) != 0 || before != f.gameplay(t) {
		t.Fatal("oversized attributed view published or changed gameplay")
	}
	want, _ := resources.Text(localization.English, localization.SharingUnavailable, nil)
	if feedbackText(t, result) != want {
		t.Fatal("missing personal oversize guidance")
	}
}

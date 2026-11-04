package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

type characterReply struct{ text, receiver, chat, authorization string }
type characterMenuEntry struct {
	Description string `json:"description"`
	Command     string `json:"command"`
	Ephemeral   bool   `json:"is_ephemeral"`
}
type characterRuntimeFixture struct {
	updates        chan string
	replies        chan characterReply
	runtime        *app.Runtime
	db             *persistence.DB
	cancel         context.CancelFunc
	done           chan error
	server         *httptest.Server
	rejectDelivery bool
	stopping       chan struct{}
	menus          chan []characterMenuEntry
	diagnostics    bytes.Buffer
	probes         bytes.Buffer
	healthURL      string
}

func startCharacterRuntime(t *testing.T, dir string, options app.Options, reject bool) *characterRuntimeFixture {
	t.Helper()
	h := &characterRuntimeFixture{updates: make(chan string, 1), replies: make(chan characterReply, 8), done: make(chan error, 1), rejectDelivery: reject, stopping: make(chan struct{}), menus: make(chan []characterMenuEntry, 4)}
	h.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "getMe":
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123,"is_bot":true}}`)
		case "setMyCommands":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if !strings.Contains(r.FormValue("commands"), `"is_ephemeral":true`) {
				t.Error("incorrect group menu")
			}
			var menu []characterMenuEntry
			if err := json.Unmarshal([]byte(r.FormValue("commands")), &menu); err != nil {
				t.Error("invalid command menu payload")
			}
			if r.FormValue("scope") == `{"type":"all_group_chats"}` {
				h.menus <- menu
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "deleteWebhook", "deleteMyCommands":
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "getUpdates":
			var request struct {
				Offset int64 `json:"offset"`
				Limit  int   `json:"limit"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Offset < 1 || request.Limit != 1 {
				t.Error("invalid polling request")
			}
			select {
			case update := <-h.updates:
				_, _ = fmt.Fprintf(w, `{"ok":true,"result":[%s]}`, update)
			case <-r.Context().Done():
			case <-h.stopping:
				_, _ = io.WriteString(w, `{"ok":true,"result":[]}`)
			}
		case "sendMessage":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			reply := characterReply{r.FormValue("text"), r.FormValue("ephemeral_message_parameters"), r.FormValue("chat_id"), r.FormValue("reply_parameters")}
			if !strings.Contains(reply.receiver, `"receiver_user_id":`) || reply.authorization == "" || !strings.HasPrefix(reply.chat, "-") {
				t.Error("unrestricted personal data delivery")
			}
			h.replies <- reply
			if h.rejectDelivery {
				_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"private delivery fixture"}`)
			} else {
				_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"ephemeral_message_id":900,"chat":{"id":%s,"type":"supergroup"}}}`, reply.chat)
			}
		default:
			t.Error("unexpected fallback or API method")
			w.WriteHeader(400)
		}
	}))
	env := commandEnvironment()
	env[config.EnvStorageDir] = dir
	env[config.EnvPollTimeout] = "1s"
	env[config.EnvShutdownDeadline] = "2s"
	cfg, err := config.NewLoader(commandLookup(env)).Load()
	if err != nil {
		t.Fatal(err)
	}
	options.TelegramBaseURL = h.server.URL
	options.HTTPClient = h.server.Client()
	options.Diagnostics = observability.NewJSONSink(&h.diagnostics)
	options.Listen = func(string, string) (net.Listener, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err == nil {
			h.healthURL = "http://" + listener.Addr().String()
		}
		return listener, err
	}
	options.Open = func(ctx context.Context, o persistence.Options) (*persistence.DB, error) {
		db, err := persistence.Open(ctx, o)
		h.db = db
		return db, err
	}
	h.runtime, err = app.New(cfg, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() { h.done <- h.runtime.Run(ctx) }()
	select {
	case <-h.runtime.Ready():
	case err := <-h.done:
		cancel()
		h.server.Close()
		t.Fatalf("startup failed: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("runtime startup timeout")
	}
	t.Cleanup(func() { h.stop(t) })
	return h
}
func (h *characterRuntimeFixture) stop(t *testing.T) {
	t.Helper()
	if h.cancel == nil {
		return
	}
	h.cancel()
	h.cancel = nil
	close(h.stopping)
	select {
	case err := <-h.done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(5 * time.Second):
		t.Error("runtime did not stop")
	}
	h.server.CloseClientConnections()
	h.server.Close()
}
func (h *characterRuntimeFixture) command(t *testing.T, id, user, chat int64, text string) characterReply {
	t.Helper()
	name, _, _ := strings.Cut(text, " ")
	update := fmt.Sprintf(`{"update_id":%d,"message":{"ephemeral_message_id":%d,"date":%d,"from":{"id":%d},"chat":{"id":%d,"type":"supergroup"},"text":%q,"entities":[{"type":"bot_command","offset":0,"length":%d}]}}`, id, id, time.Now().Unix(), user, chat, text, len(name))
	h.submit(t, update)
	select {
	case reply := <-h.replies:
		return reply
	case <-time.After(5 * time.Second):
		t.Fatal("personal reply timeout")
		return characterReply{}
	}
}

func (h *characterRuntimeFixture) submit(t *testing.T, update string) {
	t.Helper()
	select {
	case h.updates <- update:
	case <-time.After(5 * time.Second):
		t.Fatal("update admission timeout")
	}
}

func (h *characterRuntimeFixture) waitCursor(t *testing.T, want runtimecontract.UpdateID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		cursor, err := h.db.CompletedUpdate(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if cursor >= want {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("durable cursor barrier timeout")
		}
	}
}

func (h *characterRuntimeFixture) assertMenu(t *testing.T, names ...string) {
	t.Helper()
	select {
	case menu := <-h.menus:
		if len(menu) != len(names) {
			t.Fatal("startup did not replace the complete managed menu")
		}
		for i, name := range names {
			if menu[i].Command != name || !menu[i].Ephemeral {
				t.Fatal("incorrect ephemeral menu declaration")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("menu publication timeout")
	}
}

func (h *characterRuntimeFixture) captureProbes(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/livez", "/readyz", "/statusz"} {
		response, err := client.Get(h.healthURL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 32<<10))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK {
			t.Fatal("health probe failed")
		}
		h.probes.Write(body)
	}
}

func (h *characterRuntimeFixture) assertNoSensitiveDiagnostics(t *testing.T, sensitive ...string) {
	t.Helper()
	if h.cancel != nil {
		t.Fatal("diagnostics must be read after runtime shutdown")
	}
	if h.diagnostics.Len() == 0 || h.probes.Len() == 0 {
		t.Fatal("missing diagnostic or probe evidence")
	}
	for _, content := range sensitive {
		if content != "" && (strings.Contains(h.diagnostics.String(), content) || strings.Contains(h.probes.String(), content)) {
			t.Fatal("sensitive fixture appeared in diagnostics or health probes")
		}
	}
}
func productionCharacterOptions(t *testing.T) app.Options {
	t.Helper()
	options, err := productionOptions(config.Config{}, commandLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	return options
}
func TestCharacterRuntimeCreationIsolationRestart(t *testing.T) {
	dir := t.TempDir()
	h := startCharacterRuntime(t, dir, productionCharacterOptions(t), false)
	first := h.command(t, 1, 7, -100, "/character")
	if !strings.Contains(first.text, "Character created") || !strings.Contains(first.text, "100/100") {
		t.Fatal(first.text)
	}
	var id string
	if err := h.db.QueryRowContext(context.Background(), "SELECT id FROM characters WHERE user_id=7").Scan(&id); err != nil {
		t.Fatal(err)
	}
	second := h.command(t, 2, 7, -100, "/character")
	if strings.Contains(second.text, "created") {
		t.Fatal("recreated character")
	}
	cross := h.command(t, 3, 7, -200, "/character")
	if cross.text != second.text || cross.chat != "-200" {
		t.Fatal("cross-group state differs")
	}
	other := h.command(t, 4, 8, -100, "/character")
	if !strings.Contains(other.text, "created") || !strings.Contains(other.receiver, `"receiver_user_id":8`) {
		t.Fatal("other actor not isolated")
	}
	var count int
	if err := h.db.QueryRowContext(context.Background(), "SELECT count(*) FROM characters").Scan(&count); err != nil || count != 2 {
		t.Fatalf("owners count=%d err=%v", count, err)
	}
	h.stop(t)
	h = startCharacterRuntime(t, dir, productionCharacterOptions(t), false)
	repeat := h.command(t, 5, 7, -100, "/character")
	if repeat.text != second.text {
		t.Fatal("restart reset")
	}
	var reopened string
	if err := h.db.QueryRowContext(context.Background(), "SELECT id FROM characters WHERE user_id=7").Scan(&reopened); err != nil || reopened != id {
		t.Fatal("identity changed")
	}
	var cursor int64
	if err := h.db.QueryRowContext(context.Background(), "SELECT last_completed_id FROM runtime_cursors WHERE source='telegram'").Scan(&cursor); err != nil || cursor != 5 {
		t.Fatalf("cursor=%d err=%v", cursor, err)
	}
}
func TestCharacterRejectedDeliveryRetainsState(t *testing.T) {
	dir := t.TempDir()
	tuning, err := character.LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	options, err := characterModeOptions(tuning, time.Now, true)
	if err != nil {
		t.Fatal(err)
	}
	h := startCharacterRuntime(t, dir, options, true)
	failedReply := h.command(t, 1, 7, -100, "/dev_character_damage 10")
	if !strings.Contains(failedReply.text, "90/100") {
		t.Fatal("mutation was not applied before rejected delivery")
	}
	var originalID, originalOperation string
	if err := h.db.QueryRowContext(context.Background(), `SELECT id FROM characters WHERE user_id=7`).Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRowContext(context.Background(), `SELECT operation_id FROM character_operations WHERE character_id=?`, originalID).Scan(&originalOperation); err != nil {
		t.Fatal(err)
	}
	// Processing this next update proves the first rejected delivery completed.
	h.command(t, 2, 7, -100, "/character")
	h.captureProbes(t)
	h.stop(t)
	h.assertNoSensitiveDiagnostics(t, failedReply.text, "/dev_character_damage 10", "private delivery fixture", commandEnvironment()[config.EnvBotToken])
	var sawRejectedDelivery bool
	for _, line := range strings.Split(h.diagnostics.String(), "\n") {
		var diagnostic struct{ Category, Operation, Outcome string }
		if json.Unmarshal([]byte(line), &diagnostic) == nil && diagnostic.Category == "delivery" && diagnostic.Operation == "send_ephemeral" && diagnostic.Outcome == "failed" {
			sawRejectedDelivery = true
		}
	}
	if !sawRejectedDelivery {
		t.Fatal("missing evidence that restricted delivery was rejected")
	}
	select {
	case <-h.replies:
		t.Fatal("rejected delivery attempted a fallback")
	default:
	}
	h = startCharacterRuntime(t, dir, productionCharacterOptions(t), false)
	got := h.command(t, 3, 7, -100, "/character")
	if strings.Contains(got.text, "created") || !strings.Contains(got.text, "90/100") || !strings.Contains(got.receiver, `"receiver_user_id":7`) {
		t.Fatal("delivery failure reset committed character")
	}
	var reopenedID, reopenedOperation string
	var health, revision, amount, appliedRevision, receipts int64
	if err := h.db.QueryRowContext(context.Background(), `SELECT id,health,revision FROM characters WHERE user_id=7`).Scan(&reopenedID, &health, &revision); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRowContext(context.Background(), `SELECT operation_id,amount,applied_revision FROM character_operations WHERE character_id=?`, reopenedID).Scan(&reopenedOperation, &amount, &appliedRevision); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRowContext(context.Background(), `SELECT count(*) FROM character_operations`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if reopenedID != originalID || reopenedOperation != originalOperation || health != 90 || revision != 2 || amount != 10 || appliedRevision != 2 || receipts != 1 {
		t.Fatal("rejected delivery lost or repeated committed mutation/receipt after restart")
	}
}

func TestCharacterRuntimeHealthRecoveryAndProductionTransition(t *testing.T) {
	dir := t.TempDir()
	clock := testsupport.NewManualClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	tuning, err := character.LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	options, err := characterModeOptions(tuning, clock.Now, true)
	if err != nil {
		t.Fatal(err)
	}
	h := startCharacterRuntime(t, dir, options, false)
	h.assertMenu(t, "character", "dev_character_damage", "dev_character_defeat")
	damaged := h.command(t, 1, 7, -100, "/dev_character_damage 10")
	if !strings.Contains(damaged.text, "90/100") {
		t.Fatal(damaged.text)
	}
	defeated := h.command(t, 2, 7, -100, "/dev_character_defeat")
	if !strings.Contains(defeated.text, "0/100") || !strings.Contains(defeated.text, "incapacitated") {
		t.Fatal(defeated.text)
	}
	var deadline int64
	if err := h.db.QueryRowContext(context.Background(), "SELECT recover_at_ms FROM characters WHERE user_id=7").Scan(&deadline); err != nil {
		t.Fatal(err)
	}
	blocked := h.command(t, 3, 7, -100, "/dev_character_damage 5")
	if !strings.Contains(blocked.text, "incapacitated") {
		t.Fatal("pre-deadline action accepted")
	}
	var receipts int
	if err := h.db.QueryRowContext(context.Background(), "SELECT count(*) FROM character_operations").Scan(&receipts); err != nil || receipts != 2 {
		t.Fatal("rejected action consumed identity")
	}
	h.stop(t)
	options, err = characterModeOptions(tuning, clock.Now, true)
	if err != nil {
		t.Fatal(err)
	}
	h = startCharacterRuntime(t, dir, options, false)
	before := h.command(t, 4, 7, -100, "/character")
	if !strings.Contains(before.text, "incapacitated") {
		t.Fatal("restart recovered early")
	}
	clock.Advance(5 * time.Second)
	recovered := h.command(t, 5, 7, -100, "/character")
	if !strings.Contains(recovered.text, "25/100") || !strings.Contains(recovered.text, "State: active") {
		t.Fatal(recovered.text)
	}
	damaged = h.command(t, 6, 7, -100, "/dev_character_damage 1")
	if !strings.Contains(damaged.text, "24/100") {
		t.Fatal(damaged.text)
	}
	clock.Advance(time.Hour)
	observed := h.command(t, 7, 7, -100, "/character")
	if !strings.Contains(observed.text, "24/100") {
		t.Fatal("passive regeneration")
	}
	h.stop(t)
	h = startCharacterRuntime(t, dir, productionCharacterOptions(t), false)
	h.assertMenu(t, "character", "inventory", "settings")
	// Telegram clients may retain a removed menu entry. The stale command must
	// have no registered owner, no response, and no health or receipt effect.
	h.submit(t, fmt.Sprintf(`{"update_id":8,"message":{"ephemeral_message_id":8,"date":%d,"from":{"id":7},"chat":{"id":-100,"type":"supergroup"},"text":"/dev_character_damage 10","entities":[{"type":"bot_command","offset":0,"length":21}]}}`, time.Now().Unix()))
	h.waitCursor(t, 8)
	select {
	case <-h.replies:
		t.Fatal("cached development command produced a response in production")
	default:
	}
	var storedHealth, storedReceipts int
	if err := h.db.QueryRowContext(context.Background(), `SELECT health FROM characters WHERE user_id=7`).Scan(&storedHealth); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRowContext(context.Background(), `SELECT count(*) FROM character_operations`).Scan(&storedReceipts); err != nil {
		t.Fatal(err)
	}
	if storedHealth != 24 || storedReceipts != 3 {
		t.Fatal("cached development command mutated production state")
	}
	persisted := h.command(t, 9, 7, -100, "/character")
	if !strings.Contains(persisted.text, "24/100") {
		t.Fatal("mode transition reset state")
	}
	production, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	service, err := character.NewService(production, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	err = h.db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		current, found, err := service.Lookup(ctx, tx, 7)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("missing production character")
		}
		result, err := service.ApplyHealth(ctx, tx, contract.HealthChange{OperationID: "test:production:future", CharacterID: current.ID, Kind: contract.HealthDamage, Amount: 24})
		if err != nil {
			return err
		}
		if !result.Current.RecoverAt.Equal(clock.Now().Add(5 * time.Minute)) {
			t.Error("future defeat did not use production duration")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if deadline != time.Date(2026, 10, 27, 12, 0, 5, 0, time.UTC).UnixMilli() {
		t.Fatal("development deadline changed")
	}
}

func TestCharacterRuntimeSensitiveInputsAndActorIsolation(t *testing.T) {
	tuning, err := character.LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	options, err := characterModeOptions(tuning, time.Now, true)
	if err != nil {
		t.Fatal(err)
	}
	h := startCharacterRuntime(t, t.TempDir(), options, false)
	first := h.command(t, 1, 7, -100, "/dev_character_damage 10")
	other := h.command(t, 2, 8, -100, "/dev_character_damage 33")
	if !strings.Contains(first.text, "90/100") || !strings.Contains(other.text, "67/100") {
		t.Fatal("missing sensitive response fixtures")
	}
	const commandSecret = "PRIVATE_CHARACTER_COMMAND_FIXTURE"
	const profileName = "PRIVATE_CHARACTER_PROFILE_NAME"
	const profileUsername = "PRIVATE_CHARACTER_PROFILE_USERNAME"
	const chatName = "PRIVATE_CHARACTER_SENDER_CHAT_TITLE"
	usage := h.command(t, 3, 7, -100, "/character 8 "+commandSecret)
	if usage.text != "Usage: /character" || !strings.Contains(usage.receiver, `"receiver_user_id":7`) {
		t.Fatal("spoofed target inspected another character")
	}
	invalidDamage := h.command(t, 4, 7, -100, "/dev_character_damage 1 8 "+commandSecret)
	if invalidDamage.text != "Usage: /dev_character_damage <whole HP>" {
		t.Fatal("malicious arguments bypassed self-only command validation")
	}
	for _, input := range []struct {
		id, user        int64
		bot, chatSender bool
	}{
		{5, 9, true, false}, {6, 10, false, true},
	} {
		message := map[string]any{
			"ephemeral_message_id": input.id, "date": time.Now().Unix(),
			"from":     map[string]any{"id": input.user, "is_bot": input.bot, "first_name": profileName, "username": profileUsername},
			"chat":     map[string]any{"id": -100, "type": "supergroup"},
			"text":     "/character " + commandSecret,
			"entities": []map[string]any{{"type": "bot_command", "offset": 0, "length": len("/character")}},
		}
		if input.chatSender {
			message["sender_chat"] = map[string]any{"id": -100, "type": "supergroup", "title": chatName}
		}
		update, err := json.Marshal(map[string]any{"update_id": input.id, "message": message})
		if err != nil {
			t.Fatal(err)
		}
		h.submit(t, string(update))
		h.waitCursor(t, runtimecontract.UpdateID(input.id))
		select {
		case <-h.replies:
			t.Fatal("bot or anonymous/chat sender received a personal response")
		default:
		}
	}
	// A normal command after unsupported senders is both a processing barrier and
	// evidence that their commands neither selected another actor nor reset state.
	observed := h.command(t, 7, 7, -100, "/character")
	if !strings.Contains(observed.text, "90/100") || !strings.Contains(observed.receiver, `"receiver_user_id":7`) {
		t.Fatal("unsupported actor polluted a later personal interaction")
	}
	var characters, operations, otherHealth int
	if err := h.db.QueryRowContext(context.Background(), `SELECT count(*) FROM characters`).Scan(&characters); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRowContext(context.Background(), `SELECT count(*) FROM character_operations`).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRowContext(context.Background(), `SELECT health FROM characters WHERE user_id=8`).Scan(&otherHealth); err != nil {
		t.Fatal(err)
	}
	if characters != 2 || operations != 2 || otherHealth != 67 {
		t.Fatal("untrusted actor or target arguments created or mutated a character")
	}
	h.captureProbes(t)
	h.stop(t)
	h.assertNoSensitiveDiagnostics(t, commandSecret, profileName, profileUsername, chatName, first.text, other.text, observed.text, usage.text, invalidDamage.text, commandEnvironment()[config.EnvBotToken])
	select {
	case <-h.replies:
		t.Fatal("unsupported actor produced a delayed response")
	default:
	}
}

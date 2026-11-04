package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
)

type inventoryBinaryReply struct {
	text, chat string
	editing    bool
	receiver   int64
	buttons    [][]struct {
		Text string `json:"text"`
		Data string `json:"callback_data"`
	}
}

func (r inventoryBinaryReply) button(t *testing.T, label string) string {
	t.Helper()
	for _, row := range r.buttons {
		for _, b := range row {
			if b.Text == label {
				return b.Data
			}
		}
	}
	t.Fatalf("missing inventory button %q in %q", label, r.text)
	return ""
}

type inventoryBinaryFixture struct {
	ctx          context.Context
	updates      chan string
	polls        chan int64
	replies      chan inventoryBinaryReply
	cmd          *exec.Cmd
	diagnostics  bytes.Buffer
	stopped      bool
	id           int64
	callback     bool
	rejectEdit   atomic.Bool
	rejectMenus  atomic.Bool
	denyAdmin    atomic.Bool
	menus        chan []characterMenuEntry
	menuFailures chan struct{}
}

// startInventoryBinary runs the real entry point. The child trusts only the
// fixture's certificate and its HTTPS proxy cannot reach an external host.
func startInventoryBinary(t *testing.T, ctx context.Context, binary, dir string, catalogSize int, fresh bool, overrides ...map[string]string) *inventoryBinaryFixture {
	t.Helper()
	h := &inventoryBinaryFixture{ctx: ctx, updates: make(chan string, 1), polls: make(chan int64, 32), replies: make(chan inventoryBinaryReply, 16), menus: make(chan []characterMenuEntry, 64), menuFailures: make(chan struct{}, 8)}
	identity := make(chan struct{}, 1)
	release := make(chan struct{})
	menus := make(chan []characterMenuEntry, 1)
	proxy, trust := smokeTelegramProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:] {
		case "getMe":
			select {
			case identity <- struct{}{}:
			case <-r.Context().Done():
				return
			}
			select {
			case <-release:
				_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123,"is_bot":true,"username":"test_bot"}}`)
			case <-r.Context().Done():
			}
		case "setMyCommands":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			var menu []characterMenuEntry
			if json.Unmarshal([]byte(r.FormValue("commands")), &menu) != nil {
				t.Error("invalid complete group menu")
			}
			if r.FormValue("scope") == `{"type":"all_group_chats"}` {
				menus <- menu
			} else {
				if h.rejectMenus.Load() {
					select {
					case h.menuFailures <- struct{}{}:
					default:
					}
					_, _ = io.WriteString(w, `{"ok":false,"error_code":429,"description":"fixture","parameters":{"retry_after":1}}`)
					return
				}
				select {
				case h.menus <- menu:
				default:
				}
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "getChatMember":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			id, _ := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
			status := "member"
			if id == 123 || id == 7 && !h.denyAdmin.Load() {
				status = "administrator"
			}
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"status":%q,"user":{"id":%d,"is_bot":%t}}}`, status, id, id == 123)
		case "deleteWebhook", "answerCallbackQuery", "deleteMyCommands":
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "getUpdates":
			var request struct {
				Offset int64 `json:"offset"`
				Limit  int   `json:"limit"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Limit != 1 {
				t.Error("invalid production polling")
				return
			}
			select {
			case h.polls <- request.Offset:
			case <-r.Context().Done():
				return
			}
			select {
			case update := <-h.updates:
				_, _ = fmt.Fprintf(w, `{"ok":true,"result":[%s]}`, update)
			case <-r.Context().Done():
			}
		case "sendMessage", "editEphemeralMessageText":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			reply := inventoryBinaryReply{text: r.FormValue("text"), chat: r.FormValue("chat_id")}
			var auth struct {
				Receiver int64 `json:"receiver_user_id"`
			}
			var markup struct {
				Buttons [][]struct {
					Text string `json:"text"`
					Data string `json:"callback_data"`
				} `json:"inline_keyboard"`
			}
			editing := strings.HasSuffix(r.URL.Path, "/editEphemeralMessageText")
			reply.editing = editing
			if editing {
				auth.Receiver, _ = strconv.ParseInt(r.FormValue("receiver_user_id"), 10, 64)
				if r.FormValue("ephemeral_message_id") != "900" {
					t.Error("inventory edited a different message")
				}
			} else if json.Unmarshal([]byte(r.FormValue("ephemeral_message_parameters")), &auth) != nil {
				t.Error("invalid personal visibility parameters")
			}
			if auth.Receiver <= 0 {
				t.Error("inventory response lost personal visibility")
			}
			if raw := r.FormValue("reply_markup"); raw != "" && json.Unmarshal([]byte(raw), &markup) != nil {
				t.Error("invalid inventory keyboard")
			}
			reply.receiver, reply.buttons = auth.Receiver, markup.Buttons
			rejectEdit := h.rejectEdit.Load()
			h.replies <- reply
			if editing {
				if rejectEdit {
					_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"edit rejected"}`)
					return
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
				return
			}
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"ephemeral_message_id":900,"chat":{"id":%s,"type":"supergroup"}}}`, reply.chat)
		default:
			t.Error("unexpected production Telegram operation")
			w.WriteHeader(400)
		}
	}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	env := commandEnvironment()
	env[config.EnvStorageDir] = dir
	env[config.EnvHealthAddress] = address
	env[config.EnvPollTimeout] = "1s"
	env[config.EnvShutdownDeadline] = "2s"
	for _, override := range overrides {
		for key, value := range override {
			env[key] = value
		}
	}
	h.cmd = exec.CommandContext(ctx, binary)
	for key, value := range env {
		h.cmd.Env = append(h.cmd.Env, key+"="+value)
	}
	h.cmd.Env = append(h.cmd.Env, "HTTPS_PROXY="+proxy, "SSL_CERT_FILE="+trust, "SSL_CERT_DIR="+t.TempDir())
	h.cmd.Stdout, h.cmd.Stderr = &h.diagnostics, &h.diagnostics
	if err = h.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !h.stopped {
			_ = h.cmd.Process.Kill()
			_ = h.cmd.Wait()
		}
	})
	startupReceive(t, ctx, identity)
	probeStartup(t, ctx, address, "/readyz", http.StatusServiceUnavailable)
	// This observation precedes Telegram identity completion, menu publication and
	// admission, proving preparation committed independently of any inventory open.
	db := inventoryBinaryDB(t, dir)
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_catalog`).Scan(&count); err != nil || count != catalogSize {
		t.Fatalf("catalog not prepared before Telegram setup: count=%d err=%v", count, err)
	}
	if fresh {
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM inventories`).Scan(&count); err != nil || count != 0 {
			t.Fatal("startup granted inventory before first open")
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case menu := <-menus:
		expected := []string{"character"}
		if env[envDevCharacter] == "true" {
			expected = append(expected, "dev_character_damage", "dev_character_defeat")
		}
		expected = append(expected, "inventory")
		if env[envDevInventory] == "true" {
			expected = append(expected, "dev_inventory_grant", "dev_inventory_heal")
		}
		expected = append(expected, "settings")
		if len(menu) != len(expected) {
			t.Fatalf("incomplete production menu: %+v", menu)
		}
		for index, name := range expected {
			if menu[index].Command != name || !menu[index].Ephemeral {
				t.Fatalf("invalid production menu entry: %+v", menu[index])
			}
		}
	case <-ctx.Done():
		t.Fatal("production menu never published")
	}
	waitSmokeReady(t, ctx, address)
	return h
}
func inventoryBinaryDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "replipg.db"))
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestInventoryProductionBinaryEditFailurePreservesEquipment(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h := startInventoryBinary(t, ctx, buildInventoryBinary(t, ctx, false), t.TempDir(), 4, true)
	call := func(data string, callback bool) inventoryBinaryReply {
		t.Helper()
		h.submit(t, 7, -100, "", data, callback)
		return h.reply(t, 7, -100)
	}
	page := call("/inventory", false)
	// Item ordering follows stable IDs, independently of translated display names.
	detail := call(page.button(t, "4"), true)
	h.rejectEdit.Store(true)
	call(detail.button(t, "Equip"), true)
	h.rejectEdit.Store(false)
	page = call("/inventory", false)
	select {
	case <-h.replies:
		t.Fatal("rejected edit sent a fallback response")
	default:
	}
	if !strings.Contains(page.text, "Bonuses: +1 attack, +0 defense") {
		t.Fatal("rejected edit lost committed equipment")
	}
	detail = call(page.button(t, "4"), true)
	call(detail.button(t, "Unequip"), true)
	page = call("/inventory", false)
	if !strings.Contains(page.text, "Bonuses: +0 attack, +0 defense") {
		t.Fatal("unequip did not persist")
	}
	h.stop(t)
}
func (h *inventoryBinaryFixture) stop(t *testing.T) {
	t.Helper()
	if h.stopped {
		return
	}
	if err := h.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err := h.cmd.Wait()
	h.stopped = true
	if err != nil {
		t.Fatalf("production shutdown: %v\n%s", err, h.diagnostics.String())
	}
	for _, private := range []string{"Inventory •", "Coin •", "inv:", commandEnvironment()[config.EnvBotToken]} {
		if strings.Contains(h.diagnostics.String(), private) {
			t.Fatal("inventory diagnostics exposed personal content")
		}
	}
}
func (h *inventoryBinaryFixture) submit(t *testing.T, user, chat int64, label, data string, callback bool, ordinary ...bool) {
	t.Helper()
	h.id++
	h.callback = callback
	actor := map[string]any{"id": user}
	if label != "" {
		actor["username"] = label
	}
	message := map[string]any{"ephemeral_message_id": h.id, "date": time.Now().Unix(), "chat": map[string]any{"id": chat, "type": "supergroup"}}
	update := map[string]any{"update_id": h.id}
	if callback {
		message["ephemeral_message_id"] = 900
		update["callback_query"] = map[string]any{"id": fmt.Sprint(h.id), "from": actor, "message": message, "data": data}
	} else {
		if len(ordinary) > 0 && ordinary[0] {
			delete(message, "ephemeral_message_id")
			message["message_id"] = h.id
		}
		message["from"] = actor
		message["text"] = data
		message["entities"] = []map[string]any{{"type": "bot_command", "offset": 0, "length": len(strings.Fields(data)[0])}}
		update["message"] = message
	}
	encoded, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case h.updates <- string(encoded):
	case <-h.ctx.Done():
		t.Fatal("production update stalled")
	}
	for {
		select {
		case offset := <-h.polls:
			if offset >= h.id+1 {
				return
			}
		case <-h.ctx.Done():
			t.Fatal("production update not durably completed")
		}
	}
}
func (h *inventoryBinaryFixture) reply(t *testing.T, user, chat int64) inventoryBinaryReply {
	t.Helper()
	select {
	case reply := <-h.replies:
		if reply.editing != h.callback {
			t.Fatal("inventory must send on open and edit on button presses")
		}
		if reply.receiver != user || reply.chat != strconv.FormatInt(chat, 10) {
			t.Fatal("cross-user/group inventory delivery")
		}
		return reply
	case <-h.ctx.Done():
		t.Fatal("missing inventory response")
	}
	return inventoryBinaryReply{}
}
func buildInventoryBinary(t *testing.T, ctx context.Context, extra bool) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "replipg")
	args := []string{"build", "-o", binary}
	if extra {
		// A build overlay supplies test tuning to the real embedded catalog without
		// changing version-controlled files or adding production configuration.
		source, err := filepath.Abs("../../internal/inventory/items.json")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		var catalog struct {
			Items   []map[string]any `json:"items"`
			Starter []map[string]any `json:"starter"`
		}
		if err = json.Unmarshal(raw, &catalog); err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 6; n++ {
			id := fmt.Sprintf("extra_%d", n)
			catalog.Items = append(catalog.Items, map[string]any{"id": id, "name": fmt.Sprintf("Extra %d", n), "description": "Pagination fixture.", "category": "inert"})
			catalog.Starter = append(catalog.Starter, map[string]any{"item_id": id, "quantity": 1})
		}
		raw, err = json.Marshal(catalog)
		if err != nil {
			t.Fatal(err)
		}
		replacement := filepath.Join(t.TempDir(), "items.json")
		if err = os.WriteFile(replacement, raw, 0600); err != nil {
			t.Fatal(err)
		}
		replacements := localizedPaginationOverlay(t, source, replacement)
		overlay, err := json.Marshal(map[string]any{"Replace": replacements})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "overlay.json")
		if err = os.WriteFile(path, overlay, 0600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-overlay", path)
	}
	args = append(args, ".")
	if output, err := exec.CommandContext(ctx, "go", args...).CombinedOutput(); err != nil {
		t.Fatalf("build inventory executable: %v\n%s", err, output)
	}
	return binary
}
func TestInventoryProductionBinaryInspectionRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := buildInventoryBinary(t, ctx, false)
	dir := t.TempDir()
	h := startInventoryBinary(t, ctx, binary, dir, 4, true)
	openedAfter := time.Now()
	h.submit(t, 7, -100, "owner_one", "/character", false)
	h.reply(t, 7, -100)
	h.submit(t, 7, -100, "owner_one", "/inventory", false)
	first := h.reply(t, 7, -100)
	for _, text := range []string{"Inventory • owner_one", "1. Leather armor (1)", "2. Coin (10)", "3. Healing potion (3)", "4. Stick (1)", "Weapon: Empty", "Armor: Empty", "Bonuses: +0 attack, +0 defense"} {
		if !strings.Contains(first.text, text) {
			t.Fatalf("missing starter UI %q in %q", text, first.text)
		}
	}
	coin := first.button(t, "2")
	h.submit(t, 7, -100, "owner_one", coin, true)
	detail := h.reply(t, 7, -100)
	if !strings.Contains(detail.text, "Coin • owner_one") || !strings.Contains(detail.text, "Category: inert") || !strings.Contains(detail.text, "Owned: 10") || len(detail.buttons) != 1 || detail.buttons[0][0].Text != "Back" {
		t.Fatal("inert detail gained mechanics or lost owned quantity")
	}
	h.submit(t, 7, -100, "owner_one", detail.button(t, "Back"), true)
	back := h.reply(t, 7, -100)
	if back.text != first.text {
		t.Fatal("Back changed inventory contents")
	}
	h.submit(t, 7, -100, "owner_one", first.button(t, "3"), true)
	potion := h.reply(t, 7, -100)
	if !strings.Contains(potion.text, "Usable during battle") || len(potion.buttons) != 1 || potion.buttons[0][0].Text != "Back" {
		t.Fatal("healing detail exposed standalone consumption")
	}
	h.submit(t, 7, -200, "", "/inventory", false)
	otherGroup := h.reply(t, 7, -200)
	if otherGroup.text != strings.Replace(first.text, "owner_one", "you", 1) {
		t.Fatal("cross-group open changed shared kit or missing-label fallback")
	}
	h.submit(t, 8, -100, "owner_two", "/inventory", false)
	second := h.reply(t, 8, -100)
	if second.text != strings.Replace(first.text, "owner_one", "owner_two", 1) {
		t.Fatal("second actor did not receive independent starter inventory")
	}
	// Foreign controls produce acknowledgements only, never another actor's view.
	h.submit(t, 8, -100, "owner_two", coin, true)
	h.submit(t, 7, -200, "", coin, true)
	select {
	case <-h.replies:
		t.Fatal("foreign actor/group received a bound view")
	default:
	}
	h.stop(t)
	db := inventoryBinaryDB(t, dir)
	var roots, receipts, quantity int
	for query, want := range map[string]int{`SELECT count(*) FROM inventories WHERE starter_granted=1`: 2, `SELECT count(*) FROM inventory_operations WHERE kind='starter'`: 2, `SELECT count(*) FROM inventory_equipment`: 0} {
		if err := db.QueryRowContext(ctx, query).Scan(&roots); err != nil || roots != want {
			t.Fatalf("persistent starter invariant: got %d want %d err %v", roots, want, err)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*),sum(quantity) FROM inventory_items WHERE item_id='coin'`).Scan(&receipts, &quantity); err != nil || receipts != 2 || quantity != 20 {
		t.Fatal("starter quantities duplicated or shared between actors")
	}
	var expires int64
	if err := db.QueryRowContext(ctx, `SELECT expires_at_ms FROM inventory_controls WHERE token=?`, strings.TrimPrefix(coin, "inv:")).Scan(&expires); err != nil || expires < openedAfter.Add(5*time.Minute).UnixMilli() || expires > time.Now().Add(5*time.Minute).UnixMilli() {
		t.Fatal("read control did not retain its bounded five-minute lifetime")
	}
	// Move only this test-owned persisted deadline into the past, avoiding a
	// five-minute wall-clock wait while keeping production clocks unchanged.
	if _, err := db.ExecContext(ctx, `UPDATE inventory_controls SET expires_at_ms=? WHERE token=?`, time.Now().Add(-time.Second).UnixMilli(), strings.TrimPrefix(coin, "inv:")); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	next := startInventoryBinary(t, ctx, binary, dir, 4, false)
	next.id = h.id
	// Check expiry before successful navigation prunes old controls.
	next.submit(t, 7, -100, "owner_one", coin, true)
	if !strings.Contains(next.reply(t, 7, -100).text, "control expired") {
		t.Fatal("restart renewed an expired read control")
	}
	next.submit(t, 7, -100, "owner_one", first.button(t, "3"), true)
	persisted := next.reply(t, 7, -100)
	if persisted.text != potion.text {
		t.Fatal("unexpired bound read control did not survive restart")
	}
	next.submit(t, 7, -100, "owner_one", "/inventory", false)
	if next.reply(t, 7, -100).text != first.text {
		t.Fatal("repeated restart open granted a duplicate kit")
	}
	next.stop(t)
	db = inventoryBinaryDB(t, dir)
	defer func() { _ = db.Close() }()
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM inventory_operations WHERE kind='starter'`).Scan(&receipts); err != nil || receipts != 2 {
		t.Fatal("restart created additional starter receipts")
	}
}
func TestInventoryProductionBinaryPagination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := buildInventoryBinary(t, ctx, true)
	h := startInventoryBinary(t, ctx, binary, t.TempDir(), 10, true)
	h.submit(t, 7, -100, "", "/inventory", false)
	first := h.reply(t, 7, -100)
	if !strings.Contains(first.text, "8. Extra 5 (1)") || strings.Contains(first.text, "Healing potion") {
		t.Fatal("first inventory page is not bounded to eight items")
	}
	h.submit(t, 7, -100, "", first.button(t, "Next"), true)
	last := h.reply(t, 7, -100)
	if !strings.Contains(last.text, "1. Healing potion (3)") || !strings.Contains(last.text, "2. Stick (1)") || strings.Contains(last.text, "3.") {
		t.Fatal("second-page numbering/items changed")
	}
	h.submit(t, 7, -100, "", last.button(t, "1"), true)
	detail := h.reply(t, 7, -100)
	if !strings.HasPrefix(detail.text, "Healing potion • you") {
		t.Fatal("second-page button selected the wrong item")
	}
	h.submit(t, 7, -100, "", detail.button(t, "Back"), true)
	back := h.reply(t, 7, -100)
	if back.text != last.text {
		t.Fatal("detail Back lost originating page")
	}
	h.submit(t, 7, -100, "", back.button(t, "Previous"), true)
	if h.reply(t, 7, -100).text != first.text {
		t.Fatal("Previous did not restore first page")
	}
	h.stop(t)
}

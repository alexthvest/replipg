package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/health"
)

// TestProductionBinarySmoke builds and runs main, including production config,
// composition and signal handling. Its CONNECT proxy never dials an upstream:
// the only permitted hostname is routed to a test-owned loopback TLS listener.
// Trust and proxy settings belong solely to the child, not the developer shell.
func TestProductionBinarySmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "replipg")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production binary: %v\n%s", err, output)
	}

	identity := make(chan struct{})
	identitySeen := make(chan struct{}, 1)
	finished := make(chan struct{}, 1)
	var mu sync.Mutex
	var methods []string
	sends := 0
	updates := smokeUpdates()
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		switch method {
		case "getMe":
			identitySeen <- struct{}{}
			select {
			case <-identity:
				_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"fixture","username":"test_bot"}}`)
			case <-r.Context().Done():
			}
		case "setMyCommands", "deleteMyCommands":
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "deleteWebhook":
			var payload struct {
				Drop bool `json:"drop_pending_updates"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); (err != nil && !errors.Is(err, io.EOF)) || payload.Drop {
				t.Error("webhook cleanup did not preserve queued updates")
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		case "getUpdates":
			var payload struct {
				Offset int `json:"offset"`
				Limit  int `json:"limit"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Limit != 1 || payload.Offset < 1 || payload.Offset > len(updates)+1 {
				t.Error("invalid completion-controlled poll")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if payload.Offset <= len(updates) {
				_, _ = io.WriteString(w, `{"ok":true,"result":[`+updates[payload.Offset-1]+`]}`)
			} else {
				select {
				case finished <- struct{}{}:
				default:
				}
				<-r.Context().Done()
			}
		case "sendMessage", "answerCallbackQuery":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error("invalid outbound form")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			mu.Lock()
			defer mu.Unlock()
			methods = append(methods, method)
			if method == "answerCallbackQuery" {
				want := "query"
				if sends == 4 {
					want = "rejected-query"
				}
				if r.FormValue("callback_query_id") != want || r.FormValue("text") != "" {
					t.Error("callback acknowledgement identity or visibility changed")
				}
				_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
				return
			}
			sends++
			checkSmokeSend(t, r, sends)
			if sends == 5 {
				_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"PRIVATE rejection body"}`)
			} else {
				_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":9,"ephemeral_message_id":77,"chat":{"id":-100,"type":"supergroup"}}}`)
			}
		default:
			t.Error("unexpected Telegram operation")
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	proxyURL, trust := smokeTelegramProxy(t, api)

	// Production owns its health listener. Reserve a loopback address and release
	// it immediately before startup; bind conflicts fail this gate safely.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	env := commandEnvironment()
	env[config.EnvStorageDir] = dir
	env[config.EnvHealthAddress] = address
	env[config.EnvPollTimeout] = "1s"
	env[config.EnvShutdownDeadline] = "2s"
	env[config.EnvDevSmoke] = "true"
	cmd := exec.CommandContext(ctx, binary)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Env = append(cmd.Env, "HTTPS_PROXY="+proxyURL, "SSL_CERT_FILE="+trust, "SSL_CERT_DIR="+t.TempDir())
	var diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &diagnostics, &diagnostics
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Wait is called exactly once, even after a failing assertion. Waiting also
	// joins os/exec's diagnostic-copy goroutines before inspecting the buffer.
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	startupReceive(t, ctx, identitySeen)
	probeStartup(t, ctx, address, "/livez", http.StatusOK)
	probeStartup(t, ctx, address, "/readyz", http.StatusServiceUnavailable)
	close(identity)
	startupReceive(t, ctx, finished)
	waitSmokeReady(t, ctx, address)
	probeStartup(t, ctx, address, "/livez", http.StatusOK)
	probeStartup(t, ctx, address, "/readyz", http.StatusOK)
	assertSmokeStatus(t, ctx, address)

	mu.Lock()
	gotMethods := strings.Join(methods, ",")
	gotSends := sends
	mu.Unlock()
	if gotSends != 5 || gotMethods != "sendMessage,answerCallbackQuery,sendMessage,sendMessage,sendMessage,answerCallbackQuery,sendMessage" {
		t.Fatal("unexpected delivery sequence or fallback")
	}
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("production signal shutdown failed: %v", err)
	}
	if strings.Count(diagnostics.String(), `"state":"ready"`) != 1 || !strings.Contains(diagnostics.String(), `"state":"stopped"`) {
		t.Fatal("production lifecycle did not reach ready and stopped")
	}
	if strings.Count(diagnostics.String(), `"outcome":"failed"`) < 1 {
		t.Fatal("restricted delivery rejection lacked safe failure diagnostic")
	}
	for _, private := range []string{env[config.EnvBotToken], dir, "PRIVATE", "Development smoke:"} {
		if strings.Contains(diagnostics.String(), private) {
			t.Fatal("production diagnostics exposed private configuration or content")
		}
	}
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err == nil {
		_ = connection.Close()
		t.Fatal("production health listener remained open after exit")
	}
	db := openStartupDB(t, ctx, dir)
	if cursor, err := db.CompletedUpdate(ctx); err != nil || int64(cursor) != int64(len(updates)) {
		t.Fatal("production storage did not reopen with every update durably completed")
	}
}

func smokeUpdates() []string {
	return []string{
		`{"update_id":1,"message":{"message_id":1,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"/dev_smoke PRIVATE args","entities":[{"type":"bot_command","offset":0,"length":10}]}}`,
		`{"update_id":2,"callback_query":{"id":"query","from":{"id":8},"message":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}},"data":"dev_smoke:reply"}}`,
		`{"update_id":3,"message":{"message_id":0,"ephemeral_message_id":77,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"/dev_smoke_ephemeral","entities":[{"type":"bot_command","offset":0,"length":20}]}}`,
		`{"update_id":4,"message":{"message_id":4,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"PRIVATE ordinary body"}}`,
		`{"update_id":5,"message":{"message_id":5,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"/dev_smoke_ephemeral","entities":[{"type":"bot_command","offset":0,"length":20}]}}`,
		`{"update_id":6,"callback_query":{"id":"rejected-query","from":{"id":8},"message":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}},"data":"dev_smoke:reply"}}`,
	}
}

func checkSmokeSend(t *testing.T, r *http.Request, index int) {
	t.Helper()
	if r.FormValue("chat_id") != "-100" || strings.Contains(r.FormValue("text"), "PRIVATE") {
		t.Error("response left source group or echoed incoming content")
	}
	wantText := map[int]string{
		1: "Development smoke: public response.",
		2: "Development smoke: restricted callback response.",
		3: "Development smoke: restricted ephemeral reply.",
		4: "Development smoke: ordinary group message received.",
		5: "Development smoke: restricted callback response.",
	}[index]
	if wantText == "" || r.FormValue("text") != wantText {
		t.Error("development probe produced unexpected response")
	}
	if index == 1 && r.FormValue("reply_markup") == "" {
		t.Error("public response omitted callback button")
	}
	restricted := index == 2 || index == 3 || index == 5
	parameters := r.FormValue("ephemeral_message_parameters")
	if !restricted {
		if parameters != "" {
			t.Error("public response gained restricted visibility")
		}
		return
	}
	var ephemeral struct {
		Receiver int64  `json:"receiver_user_id"`
		Callback string `json:"callback_query_id"`
	}
	if err := json.Unmarshal([]byte(parameters), &ephemeral); err != nil || ephemeral.Receiver != 8 {
		t.Error("restricted delivery lost target user")
	}
	wantCallback := map[int]string{2: "query", 5: "rejected-query"}[index]
	if ephemeral.Callback != wantCallback {
		t.Error("restricted delivery lost callback authorization")
	}
	if index == 3 {
		var reply struct {
			EphemeralID int `json:"ephemeral_message_id"`
			MessageID   int `json:"message_id"`
		}
		if err := json.Unmarshal([]byte(r.FormValue("reply_parameters")), &reply); err != nil || reply.EphemeralID != 77 || reply.MessageID != 0 {
			t.Error("incoming ephemeral reply used wrong message identity")
		}
	}
}

func waitSmokeReady(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/readyz", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal("production readiness probe failed")
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusOK {
			return
		}
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatal("production readiness returned unexpected status")
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("production binary never reached its first usable state")
		}
	}
}

func assertSmokeStatus(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/statusz", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var status struct {
		health.Snapshot
		Work *health.WorkCounts `json:"work"`
	}
	if err = json.NewDecoder(response.Body).Decode(&status); err != nil || response.StatusCode != http.StatusOK || !status.Live || !status.Ready || status.Lifecycle != health.Ready || status.DeploymentMode != "single_instance" || status.MultiInstanceSupported || status.Work == nil || *status.Work != (health.WorkCounts{}) {
		t.Fatal("full runtime status did not report ready with both durable queues available")
	}
}

func smokeTelegramProxy(t *testing.T, handler http.Handler) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "api.telegram.org"}, DNSNames: []string{"api.telegram.org"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	trust := filepath.Join(t.TempDir(), "telegram-test-ca.pem")
	if err = os.WriteFile(trust, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewUnstartedServer(handler)
	backend.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	backend.StartTLS()
	t.Cleanup(backend.Close)
	var tunnels sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "api.telegram.org:443" {
			t.Error("proxy rejected non-Telegram destination")
			http.Error(w, "blocked", http.StatusForbidden)
			return
		}
		// Deliberately dial only the fixture address, never r.Host or a DNS name.
		upstream, err := net.DialTimeout("tcp", backend.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error("fixture TLS connection failed")
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = upstream.Close() }()
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("fixture proxy hijack failed")
			return
		}
		tunnels.Add(1)
		defer tunnels.Done()
		defer func() { _ = downstream.Close() }()
		if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = buffered.Flush(); err != nil {
			return
		}
		copied := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, buffered)
			_ = upstream.Close()
			close(copied)
		}()
		_, _ = io.Copy(downstream, upstream)
		_ = downstream.Close()
		<-copied
	}))
	t.Cleanup(func() {
		proxy.Close()
		tunnels.Wait()
	})
	return proxy.URL, trust
}

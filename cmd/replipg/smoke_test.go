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
		case "answerCallbackQuery":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error("invalid outbound form")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()

			mu.Lock()
			defer mu.Unlock()

			methods = append(methods, method)

			if r.FormValue("callback_query_id") != "query" && r.FormValue("callback_query_id") != "rejected-query" || r.FormValue("text") != "" {
				t.Error("callback acknowledgement identity or visibility changed")
			}
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			t.Error("unexpected Telegram operation")
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	proxyURL, trust := smokeTelegramProxy(t, api)

	// Keep the former default health port occupied throughout production startup.
	// An existing listener also satisfies this regression scenario.
	occupied, listenErr := net.Listen("tcp", "127.0.0.1:8080")
	if listenErr != nil && !errors.Is(listenErr, syscall.EADDRINUSE) {
		t.Fatal(listenErr)
	}
	if occupied != nil {
		defer func() { _ = occupied.Close() }()
	}

	dir := t.TempDir()
	env := commandEnvironment()
	env[config.EnvStorageDir] = dir
	env[config.EnvPollTimeout] = "1s"
	env[config.EnvShutdownDeadline] = "2s"
	env["REPLIPG_DEV_SMOKE"] = "true" // Obsolete configuration cannot restore retired probes.
	env["REPLIPG_HEALTH_ADDRESS"] = "127.0.0.1:8080"
	cmd := exec.CommandContext(ctx, binary)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	cmd.Env = append(cmd.Env, "HTTPS_PROXY="+proxyURL, "SSL_CERT_FILE="+trust, "SSL_CERT_DIR="+t.TempDir())
	var diagnostics bytes.Buffer

	cmd.Stdout, cmd.Stderr = &diagnostics, &diagnostics
	if err := cmd.Start(); err != nil {
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
	close(identity)
	startupReceive(t, ctx, finished)

	mu.Lock()
	gotMethods := strings.Join(methods, ",")
	mu.Unlock()

	if gotMethods != "answerCallbackQuery,answerCallbackQuery" {
		t.Fatal("retired probes produced output or lost callback acknowledgement")
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	err := cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("production signal shutdown failed: %v", err)
	}

	if strings.Count(diagnostics.String(), `"state":"ready"`) != 1 || !strings.Contains(diagnostics.String(), `"state":"stopped"`) {
		t.Fatal("production lifecycle did not reach ready and stopped")
	}

	for _, private := range []string{env[config.EnvBotToken], dir, "PRIVATE", "Development smoke:"} {
		if strings.Contains(diagnostics.String(), private) {
			t.Fatal("production diagnostics exposed private configuration or content")
		}
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

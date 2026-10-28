package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// memoryCredentials isolates the OS credential-store seam while preserving
// cross-command state; authentication itself uses a real local HTTP server.
type memoryCredentials struct {
	values map[string]string
}

// Get returns the session belonging to one installation, never another origin.
func (s *memoryCredentials) Get(server string) (string, error) {
	if value, ok := s.values[server]; ok {
		return value, nil
	}
	return "", cli.ErrNoCredential
}

// Set persists the credential for subsequent command executions.
func (s *memoryCredentials) Set(server, value string) error {
	s.values[server] = value
	return nil
}

// Delete makes subsequent authenticated commands require login again.
func (s *memoryCredentials) Delete(server string) error {
	delete(s.values, server)
	return nil
}

// authFixture models the external authentication service: it issues a CLI
// session only after browser approval and rejects that session after logout.
type authFixture struct {
	mu       sync.Mutex
	approved bool
	revoked  bool
	pending  bool
}

// handler supplies protocol responses independently of CLI implementation.
func (f *authFixture) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/auth/device/code":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Method != http.MethodPost || body["client_id"] != "melti-cli" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusBadRequest)
			return
		}
		origin := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               "device-secret",
			"user_code":                 "ABCD-EFGH",
			"verification_uri":          origin + "/device",
			"verification_uri_complete": origin + "/device?user_code=ABCD-EFGH",
			"expires_in":                30,
			"interval":                  1,
		})
	case "/api/auth/device/token":
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		validGrant := body["grant_type"] == "urn:ietf:params:oauth:grant-type:device_code"
		if !validGrant || body["device_code"] != "device-secret" || body["client_id"] != "melti-cli" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		if !f.approved || f.pending {
			f.pending = false
			http.Error(w, `{"error":"authorization_pending"}`, http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"cli-session-secret","token_type":"Bearer","expires_in":604800}`))
	case "/api/auth/get-session":
		if r.Header.Get("Authorization") != "Bearer cli-session-secret" || f.revoked {
			_, _ = w.Write([]byte(`null`))
			return
		}
		w.Header().Set("set-auth-jwt", "fresh-api-jwt")
		_, _ = w.Write([]byte(`{"session":{"expiresAt":"2099-01-01T00:00:00Z"},"user":{"id":"member-1"}}`))
	case "/api/v1/users/me":
		if r.Header.Get("Authorization") != "Bearer fresh-api-jwt" || f.revoked {
			http.Error(w, `{"error":{"code":"authentication_required","message":"Authentication is required."}}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"member-1","email":"member@example.test"}`))
	case "/api/auth/sign-out":
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer cli-session-secret" {
			http.Error(w, `{"error":"invalid_session"}`, http.StatusUnauthorized)
			return
		}
		f.revoked = true
		_, _ = w.Write([]byte(`{"success":true}`))
	default:
		http.NotFound(w, r)
	}
}

// execute runs the public command tree with isolated output for each invocation.
func execute(t *testing.T, options cli.Options, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	options.Out = &stdout
	options.Err = &stderr
	command := cli.NewCommand(options)
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), err
}

// TestOfflineCommandsNeedNoInstallation protects version and completion from
// requiring a backend origin during installation setup or shell bootstrapping.
func TestOfflineCommandsNeedNoInstallation(t *testing.T) {
	options := cli.Options{
		ConfigDir: t.TempDir(),
		Version:   cli.Version{Version: "1.2.3", Commit: "abc123", Date: "2026-01-01T00:00:00Z"},
	}
	stdout, stderr, err := execute(t, options, "version")
	if err != nil {
		t.Fatalf("version: %v; stderr: %s", err, stderr)
	}
	if !strings.Contains(stdout, `"version":"1.2.3"`) || !strings.Contains(stdout, `"commit":"abc123"`) {
		t.Fatalf("wrong version output: %s", stdout)
	}
	if stdout, _, err := execute(t, options, "completion", "bash"); err != nil || stdout == "" {
		t.Fatalf("completion required an installation: %v", err)
	}
	for _, group := range []string{"auth", "posts"} {
		if _, _, err := execute(t, options, group); err != nil {
			t.Fatalf("%s help required an installation: %v", group, err)
		}
	}
}

// TestBrowserLogin persists only the device-issued session and obtains a fresh
// JWT for later commands without exposing either credential in terminal output.
func TestBrowserLogin(t *testing.T) {
	fixture := &authFixture{pending: true}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	credentials := &memoryCredentials{values: map[string]string{}}
	options := cli.Options{
		ConfigDir:   t.TempDir(),
		Credentials: credentials,
		OpenBrowser: func(address string) error {
			if address != server.URL+"/device?user_code=ABCD-EFGH" {
				return fmt.Errorf("unexpected approval address: %s", address)
			}
			fixture.mu.Lock()
			fixture.approved = true
			fixture.mu.Unlock()
			return nil
		},
	}
	stdout, stderr, err := execute(t, options, "auth", "login", "--server", server.URL)
	if err != nil {
		t.Fatalf("login: %v; stderr: %s", err, stderr)
	}
	if !strings.Contains(stderr, "ABCD-EFGH") {
		t.Fatalf("missing matching code: %s", stderr)
	}
	if credentials.values[server.URL] != "cli-session-secret" {
		t.Fatal("device-issued session was not retained for subsequent commands")
	}
	for _, secret := range []string{"cli-session-secret", "fresh-api-jwt", "device-secret"} {
		if strings.Contains(stdout+stderr, secret) {
			t.Fatalf("login exposed %s", secret)
		}
	}
	stdout, _, err = execute(t, options, "auth", "status")
	if err != nil || !strings.Contains(stdout, "member@example.test") {
		t.Fatalf("status after login: %s, %v", stdout, err)
	}
	if _, _, err := execute(t, options, "auth", "logout"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, options, "auth", "status"); err == nil {
		t.Fatal("status succeeded after logout")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !fixture.revoked {
		t.Fatal("logout did not revoke the server session")
	}
}

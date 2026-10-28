package cli_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// TestDeviceRejectionStopsLogin checks terminal protocol states at the public
// command seam; neither a denied nor an expired grant becomes a saved session.
func TestDeviceRejectionStopsLogin(t *testing.T) {
	for _, code := range []string{"access_denied", "expired_token", "invalid_grant"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			fixture := &authFixture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/auth/device/token" {
					fixture.handler(w, r)
					return
				}
				w.WriteHeader(400)
				_, _ = fmt.Fprintf(w, `{"error":%q,"error_description":"Restart login"}`, code)
			}))
			defer server.Close()
			store := &memoryCredentials{values: map[string]string{}}
			stdout, _, err := execute(t, cli.Options{ConfigDir: t.TempDir(), Credentials: store},
				"auth", "login", "--server", server.URL, "--no-browser")
			var detail *cli.Error
			if !errors.As(err, &detail) || detail.Code != code {
				t.Fatalf("wanted %s, got %v", code, err)
			}
			if stdout != "" || len(store.values) != 0 {
				t.Fatal("failed authorization produced identity output or saved a session")
			}
		})
	}
}

// TestDeviceSlowDownHonorsCancellation proves a server's slow_down response does
// not trigger a tight loop and that cancellation interrupts the longer interval.
func TestDeviceSlowDownHonorsCancellation(t *testing.T) {
	fixture := &authFixture{}
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/device/token" {
			fixture.handler(w, r)
			return
		}
		polls.Add(1)
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"slow_down","error_description":"Poll more slowly"}`))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := cli.NewCommand(cli.Options{
		ConfigDir: t.TempDir(), Credentials: &memoryCredentials{values: map[string]string{}},
		Out: &bytes.Buffer{}, Err: &bytes.Buffer{},
	})
	command.SetArgs([]string{"auth", "login", "--server", server.URL, "--no-browser"})
	if err := command.ExecuteContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	if got := polls.Load(); got != 1 {
		t.Fatalf("slow_down allowed %d polls within three seconds", got)
	}
}

// TestUntrustedApprovalURLNeverOpensBrowser rejects a server response attempting
// to send the member to a different installation for consent.
func TestUntrustedApprovalURLNeverOpensBrowser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"secret","user_code":"ABCD-EFGH","verification_uri_complete":"https://unrelated.example/device","expires_in":30,"interval":1}`))
	}))
	defer server.Close()
	opened := false
	options := cli.Options{
		ConfigDir: t.TempDir(), Credentials: &memoryCredentials{values: map[string]string{}},
		OpenBrowser: func(string) error { opened = true; return nil },
	}
	stdout, _, err := execute(t, options, "auth", "login", "--server", server.URL)
	if err == nil || opened || stdout != "" {
		t.Fatalf("untrusted browser destination accepted: opened=%v, output=%s, error=%v", opened, stdout, err)
	}
}

// TestLogoutFailureKeepsRevocableCredential prevents a network failure from
// looking like successful revocation or destroying the user's retry capability.
func TestLogoutFailureKeepsRevocableCredential(t *testing.T) {
	fixture := &authFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/sign-out" {
			http.Error(w, `{"error":{"code":"unavailable","message":"Try again later"}}`, 503)
			return
		}
		fixture.handler(w, r)
	}))
	defer server.Close()
	store := &memoryCredentials{values: map[string]string{server.URL: "cli-session-secret"}}
	options := cli.Options{ConfigDir: t.TempDir(), Credentials: store}
	if stdout, _, err := execute(t, options, "auth", "logout", "--server", server.URL); err == nil || stdout != "" {
		t.Fatal("failed revocation reported success")
	}
	stdout, _, err := execute(t, options, "auth", "status", "--server", server.URL)
	if err != nil || !strings.Contains(stdout, "member@example.test") {
		t.Fatalf("failed logout destroyed the usable session: %s, %v", stdout, err)
	}
}

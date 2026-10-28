package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// TestRedirectNeverReceivesSession exercises the production client's redirect
// policy with two real HTTP servers, proving a credential cannot be forwarded.
func TestRedirectNeverReceivesSession(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	options := cli.Options{
		ConfigDir:   t.TempDir(),
		Credentials: &memoryCredentials{values: map[string]string{origin.URL: "cli-session-secret"}},
	}
	stdout, _, err := execute(t, options, "auth", "status", "--server", origin.URL)
	if err == nil || leaked.Load() || stdout != "" {
		t.Fatalf("redirect was followed: leak=%v, output=%s, error=%v", leaked.Load(), stdout, err)
	}
}

// TestUnsafeInstallationRejected ensures invalid origins fail before any
// credentials or HTTP traffic can be sent.
func TestUnsafeInstallationRejected(t *testing.T) {
	for _, address := range []string{
		"http://remote.example", "https://member:password@example.test", "https://example.test/api/v1",
		"ftp://example.test", "https://example.test?token=secret", "https://example.test#fragment",
		"https://example.test?",
	} {
		t.Run(address, func(t *testing.T) {
			_, _, err := execute(t, cli.Options{ConfigDir: t.TempDir()}, "auth", "status", "--server", address)
			var detail *cli.Error
			if !errors.As(err, &detail) || detail.Code != "invalid_arguments" {
				t.Fatalf("unsafe installation was not rejected as invalid input: %v", err)
			}
		})
	}
}

// TestForbiddenMutationEmitsStructuredError proves authorization failures retain
// their code and request identity without a usage wall, credentials, or retries.
func TestForbiddenMutationEmitsStructuredError(t *testing.T) {
	fixture := &authFixture{}
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/posts" {
			fixture.handler(w, r)
			return
		}
		mutations.Add(1)
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"Viewers cannot create Posts","request_id":"request-forbidden-1"}}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{
		"posts", "create", "--server", server.URL,
		"--brand-profile", "d79e80ac-a6bf-4f4b-8ba8-0f651b898d75", "--text", "Not authorized",
	}, cli.Options{
		ConfigDir: t.TempDir(), Out: &stdout, Err: &stderr,
		Credentials: &memoryCredentials{values: map[string]string{server.URL: "cli-session-secret"}},
	})
	var response struct{ Error cli.Error }
	if err := json.Unmarshal(stderr.Bytes(), &response); err != nil {
		t.Fatalf("stderr is not one JSON error: %s", stderr.String())
	}
	if code == 0 || stdout.Len() != 0 || response.Error.Code != "forbidden" {
		t.Fatalf("invalid failure result: exit=%d, output=%s, error=%s", code, stdout.String(), stderr.String())
	}
	if response.Error.RequestID != "request-forbidden-1" || response.Error.IdempotencyKey == "" || response.Error.Status != 403 {
		t.Fatalf("missing recovery context: %s", stderr.String())
	}
	if mutations.Load() != 1 || strings.Contains(stderr.String(), "cli-session-secret") {
		t.Fatal("mutation retried or credential exposed")
	}
}

// TestInvalidPageMakesNoRequest checks consumer-visible input bounds rather
// than invoking the backend with a page outside the core API contract.
func TestInvalidPageMakesNoRequest(t *testing.T) {
	for _, flags := range [][]string{{"--limit", "0"}, {"--limit", "101"}, {"--offset", "-1"}, {"--offset", "1000001"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			var contacted atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				contacted.Store(true)
			}))
			defer server.Close()
			args := append([]string{"posts", "list", "--server", server.URL}, flags...)
			_, _, err := execute(t, cli.Options{ConfigDir: t.TempDir()}, args...)
			var detail *cli.Error
			invalidArguments := errors.As(err, &detail) && detail.Code == "invalid_arguments"
			if !invalidArguments || contacted.Load() {
				t.Fatalf("invalid page was not rejected before credentials or HTTP: %v", err)
			}
		})
	}
}

// TestCanceledMutationPreservesRecoveryContext distinguishes cancellation from a
// generic failure while retaining the action key for an outcome-uncertain Post.
func TestCanceledMutationPreservesRecoveryContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := &authFixture{}
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/posts" {
			fixture.handler(w, r)
			return
		}
		cancel()
		<-release
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := cli.Run(ctx, []string{
		"posts", "create", "--server", server.URL,
		"--brand-profile", "d79e80ac-a6bf-4f4b-8ba8-0f651b898d75", "--text", "An interrupted action",
	}, cli.Options{
		ConfigDir: t.TempDir(), Out: &stdout, Err: &stderr,
		Credentials: &memoryCredentials{values: map[string]string{server.URL: "cli-session-secret"}},
	})
	close(release)
	var response struct{ Error cli.Error }
	if err := json.Unmarshal(stderr.Bytes(), &response); err != nil {
		t.Fatalf("stderr is not a JSON error: %s", stderr.String())
	}
	if code != 130 || stdout.Len() != 0 || response.Error.IdempotencyKey == "" {
		t.Fatalf("cancellation lost its exit code or recovery key: exit=%d, stderr=%s", code, stderr.String())
	}
}

// TestServerOverrideCannotReuseAnotherInstallationSession verifies origin
// isolation even when an installation is selected explicitly at invocation time.
func TestServerOverrideCannotReuseAnotherInstallationSession(t *testing.T) {
	fixture := &authFixture{}
	first := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer first.Close()
	var secondContacted atomic.Bool
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondContacted.Store(true)
	}))
	defer second.Close()
	store := &memoryCredentials{values: map[string]string{first.URL: "cli-session-secret"}}
	options := cli.Options{ConfigDir: t.TempDir(), Credentials: store}
	if _, _, err := execute(t, options, "auth", "status", "--server", first.URL); err != nil {
		t.Fatalf("original installation login unusable: %v", err)
	}
	_, _, err := execute(t, options, "auth", "status", "--server", second.URL)
	if !errors.Is(err, cli.ErrNoCredential) || secondContacted.Load() {
		t.Fatalf("another installation received an authenticated request: %v", err)
	}
}

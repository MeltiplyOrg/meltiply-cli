package cli_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
	"github.com/zalando/go-keyring"
)

// TestExplicitFileStorage protects the opt-in plaintext mode's lifecycle: a new
// command can use it, credentials stay out of config, and unsafe modes are refused.
func TestExplicitFileStorage(t *testing.T) {
	fixture := &authFixture{approved: true}
	server := httptest.NewServer(http.HandlerFunc(fixture.handler))
	defer server.Close()
	directory := t.TempDir()
	options := cli.Options{ConfigDir: directory}
	_, stderr, err := execute(t, options,
		"auth", "login", "--server", server.URL, "--no-browser", "--insecure-storage")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(stderr, "plaintext") {
		t.Fatal("plaintext storage was not disclosed")
	}
	config, err := os.ReadFile(filepath.Join(directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(config), "cli-session-secret") {
		t.Fatal("session secret was written to configuration")
	}
	stdout, _, err := execute(t, options, "auth", "status")
	if err != nil || !strings.Contains(stdout, "member@example.test") {
		t.Fatalf("saved login unusable: %s, %v", stdout, err)
	}
	if _, _, err := execute(t, options, "auth", "login", "--no-browser"); err != nil {
		t.Fatalf("repeat login: %v", err)
	}
	if _, _, err := execute(t, options, "auth", "status"); err != nil {
		t.Fatalf("repeat login lost the stored credential: %v", err)
	}
	paths, err := filepath.Glob(filepath.Join(directory, "credentials", "*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("expected one credential file: %v, %v", paths, err)
	}
	info, err := os.Stat(paths[0])
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credential is not owner-only: %v, %v", info, err)
	}
	if err := os.Chmod(paths[0], 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, options, "auth", "status"); err == nil {
		t.Fatal("world-readable session was accepted")
	}
}

// TestStoragePreferenceBelongsToInstallation ensures opting one installation
// into plaintext cannot hide another installation's keyring session at logout.
func TestStoragePreferenceBelongsToInstallation(t *testing.T) {
	keyring.MockInit()
	firstFixture := &authFixture{approved: true}
	first := httptest.NewServer(http.HandlerFunc(firstFixture.handler))
	defer first.Close()
	secondFixture := &authFixture{approved: true}
	second := httptest.NewServer(http.HandlerFunc(secondFixture.handler))
	defer second.Close()
	options := cli.Options{ConfigDir: t.TempDir()}
	if _, _, err := execute(t, options, "auth", "login", "--server", first.URL, "--no-browser"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, options,
		"auth", "login", "--server", second.URL, "--no-browser", "--insecure-storage"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, options, "auth", "logout", "--server", first.URL); err != nil {
		t.Fatal(err)
	}
	firstFixture.mu.Lock()
	revoked := firstFixture.revoked
	firstFixture.mu.Unlock()
	if !revoked {
		t.Fatal("logout reported success but left the first installation's keyring session live")
	}
	if _, _, err := execute(t, options, "auth", "status", "--server", second.URL); err != nil {
		t.Fatalf("another installation's logout damaged the file-backed session: %v", err)
	}
}

// TestExistingLoginKeepsItsStorage prevents an explicit fallback flag from
// abandoning a live keyring session during an otherwise idempotent login.
func TestExistingLoginKeepsItsStorage(t *testing.T) {
	keyring.MockInit()
	fixture := &authFixture{approved: true}
	grants := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/device/code" {
			grants++
		}
		fixture.handler(w, r)
	}))
	defer server.Close()
	options := cli.Options{ConfigDir: t.TempDir()}
	if _, _, err := execute(t, options, "auth", "login", "--server", server.URL, "--no-browser"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, options,
		"auth", "login", "--no-browser", "--insecure-storage"); err != nil {
		t.Fatal(err)
	}
	if grants != 1 {
		t.Fatalf("repeated login abandoned its session and issued %d device requests", grants)
	}
	if _, _, err := execute(t, options, "auth", "logout"); err != nil {
		t.Fatal(err)
	}
}

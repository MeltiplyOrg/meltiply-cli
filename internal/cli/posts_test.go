package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// TestCreatePostReusesActionIdentity models an idempotent server, proving a
// caller can repeat an uncertain creation without creating a second Post.
func TestCreatePostReusesActionIdentity(t *testing.T) {
	const brandProfile = "d79e80ac-a6bf-4f4b-8ba8-0f651b898d75"
	fixture := &authFixture{}
	created := 0
	actions := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/posts" {
			fixture.handler(w, r)
			return
		}
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fresh-api-jwt" {
			http.Error(w, `{"error":{"code":"forbidden","message":"Not allowed"}}`, 403)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := map[string]any{"brand_profile_id": brandProfile, "default_text": "A real draft Post"}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("Post payload = %#v, want %#v", body, want)
		}
		key := r.Header.Get("Idempotency-Key")
		if key != "repeatable-action-001" {
			t.Errorf("action key = %q", key)
		}
		if _, exists := actions[key]; !exists {
			created++
			actions[key] = "post-001"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": actions[key], "status": "draft"})
	}))
	defer server.Close()
	options := cli.Options{
		ConfigDir:   t.TempDir(),
		Credentials: &memoryCredentials{values: map[string]string{server.URL: "cli-session-secret"}},
	}
	args := []string{
		"posts", "create", "--server", server.URL,
		"--brand-profile", brandProfile, "--text", "A real draft Post", "--idempotency-key", "repeatable-action-001",
	}
	for range 2 {
		stdout, _, err := execute(t, options, args...)
		if err != nil {
			t.Fatal(err)
		}
		var post struct{ ID, Status string }
		if json.Unmarshal([]byte(stdout), &post) != nil || post.ID != "post-001" || post.Status != "draft" {
			t.Fatalf("unexpected created Post: %s", stdout)
		}
	}
	if created != 1 {
		t.Fatalf("created %d Posts for one action", created)
	}
}

type failingOutput struct{}

// Write models a full destination filesystem without failing the independent
// stderr stream used to recover the action identity.
func (failingOutput) Write([]byte) (int, error) { return 0, errors.New("destination is full") }

// TestCreatedPostOutputFailureRetainsActionKey protects a successful mutation's
// recovery identity when its response cannot reach the user's stdout destination.
func TestCreatedPostOutputFailureRetainsActionKey(t *testing.T) {
	fixture := &authFixture{}
	acceptedKey := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/posts" {
			fixture.handler(w, r)
			return
		}
		acceptedKey = r.Header.Get("Idempotency-Key")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"post-created","status":"draft"}`))
	}))
	defer server.Close()
	var stderr bytes.Buffer
	code := cli.Run(context.Background(), []string{
		"posts", "create", "--server", server.URL,
		"--brand-profile", "d79e80ac-a6bf-4f4b-8ba8-0f651b898d75", "--text", "Already persisted",
	}, cli.Options{
		ConfigDir: t.TempDir(), Out: failingOutput{}, Err: &stderr,
		Credentials: &memoryCredentials{values: map[string]string{server.URL: "cli-session-secret"}},
	})
	var result struct{ Error cli.Error }
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("missing machine-readable recovery: %s", stderr.String())
	}
	if code == 0 || acceptedKey == "" || result.Error.IdempotencyKey != acceptedKey {
		t.Fatalf("successful mutation lost its recovery identity: key=%q, error=%s", acceptedKey, stderr.String())
	}
}

package cli_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// TestListPostsSelectsPage proves pagination and Brand Profile selection against
// a service with an independently defined dataset, not a response echo.
func TestListPostsSelectsPage(t *testing.T) {
	const selectedBrand = "d79e80ac-a6bf-4f4b-8ba8-0f651b898d75"
	fixture := &authFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/posts" {
			fixture.handler(w, r)
			return
		}
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer fresh-api-jwt" {
			http.Error(w, "unauthorized", 401)
			return
		}
		if r.URL.Query().Get("brand_profile_id") != selectedBrand {
			_, _ = w.Write([]byte(`{"posts":[{"id":"wrong-brand"}],"total":1}`))
			return
		}
		dataset := []map[string]string{{"id": "post-first"}, {"id": "post-second"}, {"id": "post-third"}}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit < 1 || offset < 0 || offset+limit > len(dataset) {
			http.Error(w, "invalid page", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"posts": dataset[offset : offset+limit], "total": 3})
	}))
	defer server.Close()
	options := cli.Options{
		ConfigDir:   t.TempDir(),
		Credentials: &memoryCredentials{values: map[string]string{server.URL: "cli-session-secret"}},
	}
	stdout, _, err := execute(t, options,
		"posts", "list", "--server", server.URL, "--brand-profile", selectedBrand, "--limit", "1", "--offset", "1")
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Posts []struct{ ID string }
		Total int
	}
	if err := json.Unmarshal([]byte(stdout), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Posts) != 1 || response.Posts[0].ID != "post-second" || response.Total != 3 {
		t.Fatalf("wrong page: %s", stdout)
	}
}

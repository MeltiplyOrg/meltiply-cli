package cli_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MeltiplyOrg/meltiply-cli/internal/cli"
)

// TestSessionRenewalRetainsReplacement proves that the credential returned by
// renewal is used on the next command, while each API call uses a fresh JWT.
func TestSessionRenewalRetainsReplacement(t *testing.T) {
	activeSession := "original-session"
	generation := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/auth/get-session":
			if r.Header.Get("Authorization") != "Bearer "+activeSession {
				http.Error(w, `{"code":"INVALID_SESSION","message":"Session is no longer valid"}`, http.StatusUnauthorized)
				return
			}
			generation++
			activeSession = fmt.Sprintf("renewed-session-%d", generation)
			w.Header().Set("set-auth-token", activeSession)
			w.Header().Set("set-auth-jwt", fmt.Sprintf("api-jwt-%d", generation))
			_, _ = w.Write([]byte(`{"session":{"expiresAt":"2099-01-01T00:00:00Z"},"user":{"id":"member-1"}}`))
		case "/api/v1/users/me":
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer api-jwt-%d", generation) {
				http.Error(w, `{"error":{"code":"authentication_required"}}`, http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":"member-1"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	options := cli.Options{
		ConfigDir:   t.TempDir(),
		Credentials: &memoryCredentials{values: map[string]string{server.URL: activeSession}},
	}
	for range 2 {
		stdout, _, err := execute(t, options, "auth", "status", "--server", server.URL)
		if err != nil {
			t.Fatalf("renewal: %v", err)
		}
		if strings.Contains(stdout, "session") || strings.Contains(stdout, "api-jwt") {
			t.Fatalf("status exposed credentials: %s", stdout)
		}
	}
}

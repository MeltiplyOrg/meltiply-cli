package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// loginCommand authorizes a separate CLI session using the installation's device
// flow. The raw device code and resulting credentials never enter terminal output.
func (app *application) loginCommand() *cobra.Command {
	var noBrowser, insecureStorage bool
	command := &cobra.Command{
		Use: "login", Short: "Approve this CLI in your browser", Args: noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.login(cmd, noBrowser, insecureStorage)
		},
	}
	command.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the approval URL without opening a browser")
	command.Flags().BoolVar(&insecureStorage, "insecure-storage", false, "Explicitly store the session in an owner-only plaintext file")
	return command
}

type deviceAuthorization struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// login waits only until the server's device-code deadline or caller cancellation.
// A failed local save revokes the new server session instead of abandoning it.
func (app *application) login(cmd *cobra.Command, noBrowser, insecureStorage bool) error {
	if complete, err := app.prepareLogin(cmd, insecureStorage); complete || err != nil {
		return err
	}
	device, verificationURL, err := app.requestDeviceAuthorization(cmd.Context())
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Open %s\nConfirm this code: %s\nApprove only a request you started on this device.\n", verificationURL, device.UserCode)
	ctx, cancel := context.WithTimeout(cmd.Context(), time.Duration(device.ExpiresIn)*time.Second)
	defer cancel()
	app.openApprovalURL(cmd, verificationURL, noBrowser)
	token, err := app.pollDevice(ctx, device)
	if err != nil {
		return err
	}
	jwt, err := app.sessionJWT(ctx, &token)
	if err != nil {
		app.revokeUnstored(token)
		return err
	}
	user, err := app.currentUser(ctx, jwt)
	if err != nil {
		app.revokeUnstored(token)
		return err
	}
	return app.saveLogin(cmd, token, user, insecureStorage)
}

// prepareLogin reuses a live saved session and lets an explicit plaintext opt-in
// recover from an unavailable keyring. Other store or HTTP failures stop login.
func (app *application) prepareLogin(cmd *cobra.Command, insecureStorage bool) (bool, error) {
	complete, err := app.existingLogin(cmd)
	if complete {
		return true, err
	}
	explicitFallback := insecureStorage && errors.Is(err, errCredentialStoreUnavailable)
	if err != nil && !explicitFallback {
		return false, err
	}
	if insecureStorage && app.options.Credentials == nil {
		app.store = fileCredentials{directory: app.directory}
		return app.existingLogin(cmd)
	}
	return false, nil
}

// requestDeviceAuthorization validates the unauthenticated device grant before
// any browser interaction. The returned URL must stay on the selected origin.
func (app *application) requestDeviceAuthorization(ctx context.Context) (deviceAuthorization, string, error) {
	result, err := app.request(ctx, httpRequest{
		method: http.MethodPost, path: "/api/auth/device/code", body: map[string]string{"client_id": cliName},
	})
	if err != nil {
		return deviceAuthorization{}, "", err
	}
	var device deviceAuthorization
	if err := json.Unmarshal(result.body, &device); err != nil {
		return deviceAuthorization{}, "", errors.New("invalid device authorization response")
	}
	if device.DeviceCode == "" || device.UserCode == "" || device.ExpiresIn <= 0 || device.ExpiresIn > 3600 {
		return deviceAuthorization{}, "", errors.New("invalid device authorization response")
	}
	verificationURL := device.VerificationURIComplete
	if verificationURL == "" {
		verificationURL = device.VerificationURI
	}
	if err := app.validateApprovalURL(verificationURL, device.UserCode); err != nil {
		return deviceAuthorization{}, "", err
	}
	return device, verificationURL, nil
}

// validateApprovalURL refuses cross-origin consent pages and terminal escape
// sequences in the user-facing matching code.
func (app *application) validateApprovalURL(address, userCode string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed == nil {
		return errors.New("invalid browser authorization URL")
	}
	origin, err := installationOrigin(parsed.Scheme + "://" + parsed.Host)
	if err != nil || origin != app.server || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("browser authorization URL must belong to the selected installation")
	}
	if strings.ContainsAny(userCode, "\x1b\r\n\t") {
		return errors.New("invalid device matching code")
	}
	return nil
}

// openApprovalURL opens the already-validated consent page, falling back to the
// printed URL when the OS handler is missing or fails.
func (app *application) openApprovalURL(cmd *cobra.Command, address string, noBrowser bool) {
	if noBrowser {
		return
	}
	open := app.options.OpenBrowser
	if open == nil {
		open = openBrowser
	}
	if err := open(address); err != nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Could not open a browser; open the URL above manually.")
	}
}

// saveLogin persists a verified session and its storage preference together.
// Any local write failure revokes the newly issued server session.
func (app *application) saveLogin(
	cmd *cobra.Command,
	token string,
	user json.RawMessage,
	insecureStorage bool,
) error {
	if err := app.store.Set(app.server, token); err != nil {
		app.revokeUnstored(token)
		return err
	}
	storage := storageKeyring
	if insecureStorage || app.config.CredentialStorage[app.server] == storageFile {
		storage = storageFile
	}
	if app.config.CredentialStorage == nil {
		app.config.CredentialStorage = map[string]string{}
	}
	app.config.Server = app.server
	app.config.CredentialStorage[app.server] = storage
	if err := writePrivateJSON(filepath.Join(app.directory, "config.json"), app.config); err != nil {
		app.revokeUnstored(token)
		_ = app.store.Delete(app.server)
		return err
	}
	if storage == storageFile {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Warning: the session is stored in a plaintext file protected by owner-only permissions.")
	}
	return app.printIdentity(cmd, user)
}

// existingLogin keeps repeated login idempotent: a live saved session is reused
// rather than replaced by an untracked second session. Only a definitely invalid
// session permits a fresh device flow; network failures never discard it.
func (app *application) existingLogin(cmd *cobra.Command) (bool, error) {
	token, err := app.store.Get(app.server)
	if errors.Is(err, ErrNoCredential) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	original := token
	jwt, err := app.sessionJWT(cmd.Context(), &token)
	if err != nil {
		var detail *Error
		if errors.As(err, &detail) {
			if detail.Code == "authentication_required" || detail.Status == http.StatusUnauthorized {
				return false, nil
			}
		}
		return false, err
	}
	if token != original {
		if err := app.store.Set(app.server, token); err != nil {
			return false, err
		}
	}
	user, err := app.currentUser(cmd.Context(), jwt)
	if err != nil {
		return false, err
	}
	return true, app.printIdentity(cmd, user)
}

// pollDevice honors the advertised interval and RFC 8628's five-second slow-down
// increment. Denial, expiration, invalid grants, and transport failures stop login.
func (app *application) pollDevice(ctx context.Context, device deviceAuthorization) (string, error) {
	interval := device.Interval
	if interval <= 0 {
		interval = 5
	}
	if interval > 3600 {
		return "", errors.New("invalid device polling interval")
	}
	for {
		timer := time.NewTimer(time.Duration(interval) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
		result, err := app.request(ctx, httpRequest{
			method: http.MethodPost, path: "/api/auth/device/token",
			body: map[string]string{
				"client_id": cliName, "device_code": device.DeviceCode,
				"grant_type": "urn:ietf:params:oauth:grant-type:device_code",
			},
		})
		if err != nil {
			var detail *Error
			if errors.As(err, &detail) {
				switch detail.Code {
				case "authorization_pending":
					continue
				case "slow_down":
					interval = slowDownInterval(interval, detail.RetryAfterSeconds)
					continue
				case "rate_limited":
					// The installation's distributed polling budget, not the
					// grant state, rejected this poll. Wait at least as long as
					// it asked and keep the grant pending.
					interval = slowDownInterval(interval, detail.RetryAfterSeconds)
					continue
				}
			}
			return "", err
		}
		var grant struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
		}
		if json.Unmarshal(result.body, &grant) != nil || grant.AccessToken == "" || grant.TokenType != "Bearer" {
			return "", errors.New("invalid device token response")
		}
		return grant.AccessToken, nil
	}
}

// slowDownInterval applies RFC 8628's five-second increment and never waits less
// than Retry-After asked. It caps the wait so a misbehaving server cannot outlive
// the grant's expiry window.
func slowDownInterval(current, retryAfter int) int {
	return min(max(current+5, retryAfter), 60)
}

// sessionJWT exchanges a live CLI session for a new short-lived API JWT and
// updates token when the auth server replaces its session credential. A null
// session or missing JWT is a login failure, never anonymous API access.
func (app *application) sessionJWT(ctx context.Context, token *string) (string, error) {
	result, err := app.request(ctx, httpRequest{
		method: http.MethodGet, path: "/api/auth/get-session", credential: *token,
	})
	if err != nil {
		return "", err
	}
	var session struct {
		Session *struct {
			ExpiresAt time.Time `json:"expiresAt"`
		} `json:"session"`
	}
	if json.Unmarshal(result.body, &session) != nil || session.Session == nil {
		return "", &Error{Code: "authentication_required", Message: "session is absent or revoked; run melti-cli auth login"}
	}
	jwt := result.header.Get("set-auth-jwt")
	if jwt == "" {
		return "", errors.New("auth server did not return set-auth-jwt; it must support the melti-cli authentication protocol")
	}
	if replacement := result.header.Get("set-auth-token"); replacement != "" {
		*token = replacement
	}
	return jwt, nil
}

// authenticatedJWT renews the API credential for each command; the API never
// receives the long-lived session secret.
func (app *application) authenticatedJWT(ctx context.Context) (string, error) {
	token, err := app.store.Get(app.server)
	if err != nil {
		return "", err
	}
	original := token
	jwt, err := app.sessionJWT(ctx, &token)
	if err != nil {
		return "", err
	}
	if token != original {
		if err := app.store.Set(app.server, token); err != nil {
			return "", err
		}
	}
	return jwt, nil
}

// currentUser crosses the existing protected API seam to prove Workspace access.
func (app *application) currentUser(ctx context.Context, jwt string) (json.RawMessage, error) {
	result, err := app.request(ctx, httpRequest{
		method: http.MethodGet, path: "/api/v1/users/me", credential: jwt,
	})
	return result.body, err
}

// printIdentity emits only safe identity data, never the auth-session payload.
func (app *application) printIdentity(cmd *cobra.Command, user json.RawMessage) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
		Server        string          `json:"server"`
		Authenticated bool            `json:"authenticated"`
		User          json.RawMessage `json:"user"`
	}{Server: app.server, Authenticated: true, User: user})
}

// statusCommand proves both the saved session and current Workspace access.
func (app *application) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show the authenticated member", Args: noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			jwt, err := app.authenticatedJWT(cmd.Context())
			if err != nil {
				return err
			}
			user, err := app.currentUser(cmd.Context(), jwt)
			if err != nil {
				return err
			}
			return app.printIdentity(cmd, user)
		},
	}
}

// logoutCommand revokes the server session before removing its local credential.
// Network failures retain it so the user can retry revocation rather than assume
// that deleting a local file also revoked remote access.
func (app *application) logoutCommand() *cobra.Command {
	return &cobra.Command{
		Use: "logout", Short: "Revoke this CLI's session", Args: noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := app.store.Get(app.server)
			if err != nil && !errors.Is(err, ErrNoCredential) {
				return err
			}
			if token != "" {
				_, err := app.request(cmd.Context(), httpRequest{
					method: http.MethodPost, path: "/api/auth/sign-out", credential: token, body: struct{}{},
				})
				var detail *Error
				alreadyRevoked := errors.As(err, &detail) && detail.Status == http.StatusUnauthorized
				if err != nil && !alreadyRevoked {
					return err
				}
				if err := app.store.Delete(app.server); err != nil {
					return err
				}
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"server": app.server, "authenticated": false})
		},
	}
}

// revokeUnstored best-effort revokes a newly issued session after a failed local
// login. It uses a bounded independent context even if login itself was canceled.
func (app *application) revokeUnstored(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = app.request(ctx, httpRequest{
		method: http.MethodPost, path: "/api/auth/sign-out", credential: token, body: struct{}{},
	})
}

// openBrowser invokes the OS URL handler directly, never a shell. A missing or
// slow browser handler leaves the printed manual approval URL usable.
func openBrowser(address string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		// #nosec G204 -- address was validated as the selected installation's consent URL
		return exec.CommandContext(ctx, "open", address).Run()
	case "windows":
		// #nosec G204 -- address was validated as the selected installation's consent URL
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", address).Run()
	default:
		// #nosec G204 -- address was validated as the selected installation's consent URL
		return exec.CommandContext(ctx, "xdg-open", address).Run()
	}
}

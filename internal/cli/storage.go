package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

const (
	storageFile    = "file"
	storageKeyring = "keyring"
)

// ErrNoCredential indicates that an installation has no saved CLI session.
var ErrNoCredential = errors.New("no saved CLI session; run " + cliName + " auth login")

var errCredentialStoreUnavailable = errors.New(
	"OS credential store unavailable; configure your keyring or explicitly use auth login --insecure-storage",
)

// CredentialStore owns session secrets keyed by canonical installation origin.
// Implementations must not expose secrets in errors. Get returns ErrNoCredential
// for an absent entry, and Delete must tolerate an already absent entry.
type CredentialStore interface {
	Get(server string) (string, error)
	Set(server, credential string) error
	Delete(server string) error
}

type configuration struct {
	Server            string            `json:"server"`
	CredentialStorage map[string]string `json:"credential_storage"`
}

type systemCredentials struct{}

// Get reads one installation's session from the OS credential store.
func (systemCredentials) Get(server string) (string, error) {
	value, err := keyring.Get(cliName, server)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNoCredential
	}
	if err != nil {
		return "", errCredentialStoreUnavailable
	}
	return value, nil
}

// Set fails closed rather than silently falling back to plaintext storage.
func (systemCredentials) Set(server, credential string) error {
	if err := keyring.Set(cliName, server, credential); err != nil {
		return errors.New("cannot save to OS credential store; configure your keyring or explicitly use auth login --insecure-storage")
	}
	return nil
}

// Delete removes only this installation's CLI credential.
func (systemCredentials) Delete(server string) error {
	err := keyring.Delete(cliName, server)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if err != nil {
		return errors.New("cannot remove the session from the OS credential store")
	}
	return nil
}

type fileCredentials struct{ directory string }

// path uses a digest to scope a credential to an origin without interpreting
// any server input as a filesystem path.
func (s fileCredentials) path(server string) string {
	digest := sha256.Sum256([]byte(server))
	return filepath.Join(s.directory, "credentials", hex.EncodeToString(digest[:])+".json")
}

// Get reads explicitly opted-in plaintext credentials, rejecting symlinks,
// non-regular files, and group/world-readable permissions.
func (s fileCredentials) Get(server string) (string, error) {
	path := s.path(server)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNoCredential
	}
	if err != nil {
		return "", errors.New("cannot read the saved credential file")
	}
	// Windows ACLs are not represented by POSIX mode bits; the file inherits
	// the user profile directory's protection. Other platforms must prove 0600.
	unsafePermissions := runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0
	if !info.Mode().IsRegular() || unsafePermissions {
		return "", errors.New("credential file must be a regular file accessible only to its owner (0600)")
	}
	// #nosec G304 -- path is fixed under the config directory and keyed by a SHA-256 digest
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("cannot read the saved credential file")
	}
	var token string
	if err := json.Unmarshal(data, &token); err != nil || token == "" {
		return "", errors.New("saved credential file is invalid; log in again")
	}
	return token, nil
}

// Set atomically stores an explicitly opted-in plaintext session as owner-only
// JSON. No fallback from the OS keyring invokes this method automatically.
func (s fileCredentials) Set(server, credential string) error {
	return writePrivateJSON(s.path(server), credential)
}

// Delete removes this installation's plaintext credential, if present.
func (s fileCredentials) Delete(server string) error {
	err := os.Remove(s.path(server))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// writePrivateJSON replaces a complete owner-only file atomically, so interrupted
// writes cannot leave a partial session or configuration behind.
func writePrivateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".melti-*")
	if err != nil {
		return fmt.Errorf("create private configuration file: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write configuration: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// configure resolves flags/environment before the saved default installation.
// It copies the HTTP client to forbid credential-bearing redirects without
// mutating a caller-owned client. Commands such as version and completion do
// not require an installation and skip this work.
func (app *application) configure(cmd *cobra.Command) error {
	if !requiresInstallation(cmd) {
		return nil
	}
	if app.directory == "" {
		return errors.New("cannot resolve the configuration directory; set --config-dir")
	}
	data, err := os.ReadFile(filepath.Join(app.directory, "config.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read configuration: %w", err)
	}
	if err == nil {
		if err := json.Unmarshal(data, &app.config); err != nil {
			return errors.New("invalid configuration JSON")
		}
	}
	if app.server == "" {
		app.server = app.config.Server
	}
	if app.server == "" {
		return &Error{Code: "configuration_required", Message: "set --server to your Meltiply installation origin"}
	}
	app.server, err = installationOrigin(app.server)
	if err != nil {
		return &Error{Code: codeInvalidArguments, Message: err.Error()}
	}
	app.client = newHTTPClient(app.options.Client)
	app.store = app.options.Credentials
	if app.store == nil {
		app.store = systemCredentials{}
		if app.config.CredentialStorage[app.server] == storageFile {
			app.store = fileCredentials{directory: app.directory}
		}
	}
	return nil
}

// requiresInstallation reports whether the leaf command acts on Meltiply data.
// The auth and posts subcommands do; their parent groups, version, help, and
// shell completion do not.
func requiresInstallation(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current == cmd {
			continue
		}
		switch current.Name() {
		case "auth", "posts":
			return true
		}
	}
	return false
}

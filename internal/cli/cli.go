// Package cli implements the melti-cli command interface for a remote Meltiply installation.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	// cliName is both the executable name and the device-flow client identifier.
	cliName = "melti-cli"
	// codeInvalidArguments is the stable machine code for local input failures.
	codeInvalidArguments = "invalid_arguments"
)

// Version is build metadata injected by the release build; command output uses
// the same shape so agents can identify the running binary.
type Version struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// Options supplies process I/O and the external HTTP, credential-store, and
// browser seams. Zero values select the production OS implementations.
type Options struct {
	Out         io.Writer
	Err         io.Writer
	ConfigDir   string
	Client      *http.Client
	Credentials CredentialStore
	OpenBrowser func(string) error
	Version     Version
}

type application struct {
	options   Options
	directory string
	server    string
	config    configuration
	store     CredentialStore
	client    *http.Client
}

// NewCommand creates a fresh, independently executable command tree. HTTP work
// uses ExecuteContext's context; credentials never enter command output.
func NewCommand(options Options) *cobra.Command {
	if options.Out == nil {
		options.Out = os.Stdout
	}
	if options.Err == nil {
		options.Err = os.Stderr
	}
	directory := options.ConfigDir
	if directory == "" {
		directory = os.Getenv("MELTI_CONFIG_DIR")
	}
	if directory == "" {
		if base, err := os.UserConfigDir(); err == nil {
			directory = filepath.Join(base, cliName)
		}
	}
	app := &application{options: options, directory: directory}
	root := &cobra.Command{
		Use:           cliName,
		Short:         "Use a Meltiply installation from your terminal",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return app.configure(cmd)
		},
	}
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &Error{Code: codeInvalidArguments, Message: err.Error()}
	})
	root.PersistentFlags().StringVar(&app.server, "server", os.Getenv("MELTI_SERVER"), "Meltiply installation origin")
	root.PersistentFlags().StringVar(&app.directory, "config-dir", directory, "Configuration directory")
	auth := &cobra.Command{Use: "auth", Short: "Authorize and manage this CLI's session"}
	auth.AddCommand(app.loginCommand(), app.statusCommand(), app.logoutCommand())
	root.AddCommand(auth, app.postsCommand(), app.versionCommand())
	return root
}

// Run executes one invocation, returning a process exit code and writing one
// JSON error to stderr on failure. Successful output remains on stdout.
func Run(ctx context.Context, args []string, options Options) int {
	command := NewCommand(options)
	command.SetArgs(args)
	if err := command.ExecuteContext(ctx); err != nil {
		var detail *Error
		if !errors.As(err, &detail) {
			detail = &Error{Code: "command_failed", Message: err.Error()}
		}
		_ = json.NewEncoder(command.ErrOrStderr()).Encode(struct {
			Error *Error `json:"error"`
		}{Error: detail})
		if errors.Is(err, context.Canceled) {
			return 130
		}
		if detail.Code == codeInvalidArguments {
			return 2
		}
		return 1
	}
	return 0
}

// noArguments rejects accidental positional arguments without displaying usage
// on runtime failures.
func noArguments(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return &Error{Code: codeInvalidArguments, Message: err.Error()}
	}
	return nil
}

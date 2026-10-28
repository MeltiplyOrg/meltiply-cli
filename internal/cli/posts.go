package cli

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"
)

// postsCommand owns terminal input and output only; composition, permissions,
// and idempotency remain the existing backend Post service's responsibility.
func (app *application) postsCommand() *cobra.Command {
	command := &cobra.Command{Use: "posts", Short: "Read Posts and create draft Posts"}
	command.AddCommand(app.listPostsCommand(), app.createPostCommand())
	return command
}

// createPostCommand creates a text-only draft with one action identity. It does
// not publish, schedule, retry, or require a connected Platform account.
func (app *application) createPostCommand() *cobra.Command {
	var brandProfile, text, key string
	command := &cobra.Command{
		Use: "create", Short: "Create a text-only draft Post", Args: noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !validUUID(brandProfile) || text == "" {
				return &Error{Code: codeInvalidArguments, Message: "provide --brand-profile <UUID> and nonempty --text"}
			}
			if key == "" {
				var err error
				key, err = actionKey()
				if err != nil {
					return err
				}
			}
			if !validActionKey(key) {
				return &Error{Code: codeInvalidArguments, Message: "idempotency key must contain 8–128 printable ASCII characters"}
			}
			jwt, err := app.authenticatedJWT(cmd.Context())
			if err != nil {
				return err
			}
			result, err := app.request(cmd.Context(), httpRequest{
				method: http.MethodPost, path: "/api/v1/posts", credential: jwt, key: key,
				body: struct {
					BrandProfileID string `json:"brand_profile_id"`
					DefaultText    string `json:"default_text"`
				}{BrandProfileID: brandProfile, DefaultText: text},
			})
			if err != nil {
				var detail *Error
				if !errors.As(err, &detail) {
					detail = &Error{Code: "request_failed", Message: err.Error(), cause: err}
				}
				detail.IdempotencyKey = key
				return detail
			}
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result.body); err != nil {
				return &Error{
					Code: "output_failed", Message: err.Error(), IdempotencyKey: key, cause: err,
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&brandProfile, "brand-profile", "", "Owning Brand Profile UUID (required)")
	command.Flags().StringVar(&text, "text", "", "Default Post text (required)")
	command.Flags().StringVar(&key, "idempotency-key", "", "Reuse this key to retry the same creation safely; otherwise a UUID is generated")
	return command
}

// validUUID validates the identifier's wire shape without reimplementing the
// server's existence, ownership, or active-Brand-Profile checks.
func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i := range len(value) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if value[i] != '-' {
				return false
			}
			continue
		}
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// validActionKey mirrors the core HTTP contract's bounded printable header shape.
func validActionKey(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}

// actionKey generates a cryptographically random UUIDv4 for exactly one command
// invocation; failed mutations include it in their machine-readable error.
func actionKey() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate action identity: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:]), nil
}

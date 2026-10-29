package cli

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/MeltiplyOrg/meltiply-cli/internal/api"
)

// listPostsCommand fetches exactly one bounded page. Explicit pagination avoids
// silently loading an installation's complete publishing history into an agent.
func (app *application) listPostsCommand() *cobra.Command {
	var brandProfile, status string
	var limit, offset int
	command := &cobra.Command{
		Use: "list", Short: "List one page of Posts as JSON", Args: noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
				return &Error{Code: codeInvalidArguments, Message: "limit must be 1–100 and offset must be 0–1000000"}
			}

			query := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
			if brandProfile != "" {
				identifier, err := uuid.Parse(brandProfile)
				if err != nil {
					return &Error{Code: codeInvalidArguments, Message: "brand-profile must be a UUID"}
				}
				query.Set("brand_profile_id", identifier.String())
			}
			if status != "" {
				lifecycle := api.PostStatus(status)
				if !lifecycle.Valid() {
					return &Error{Code: codeInvalidArguments, Message: "status must be draft or active"}
				}
				query.Set("status", string(lifecycle))
			}
			jwt, err := app.authenticatedJWT(cmd.Context())
			if err != nil {
				return err
			}
			result, err := app.request(cmd.Context(), httpRequest{
				method: http.MethodGet, path: "/api/v1/posts?" + query.Encode(), credential: jwt,
			})
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(result.body)
		},
	}
	command.Flags().IntVar(&limit, "limit", 20, "Page size (1–100)")
	command.Flags().IntVar(&offset, "offset", 0, "Page offset (0–1000000)")
	command.Flags().StringVar(&brandProfile, "brand-profile", "", "Filter by owning Brand Profile UUID")
	command.Flags().StringVar(&status, "status", "", "Filter by lifecycle state: draft or active")
	return command
}

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/GF6599/lazylab/internal/cliout"
	"github.com/GF6599/lazylab/internal/gitlab"
)

// newWhoamiCmd implements `lazylab whoami` — the canonical "is my token
// working and who does it belong to?" check. It also doubles as the proof
// of plumbing for the Cobra restructure: if whoami works, the persistent
// flag chain, context wiring, and exit-code mapping are all wired correctly.
func newWhoamiCmd() *cobra.Command {
	var formatFlag string
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the GitLab user the configured token belongs to",
		Long: `Calls GET /user against the configured GitLab host and prints
the resulting user record. Useful for verifying a token, distinguishing
which account is active when switching between gitlab.com and a company
instance, or scripting around the authenticated identity.

Exits non-zero with a distinct code on auth failure (3), rate limit (2),
and network failure (4) so wrapping scripts can branch on the result.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := cliout.ParseFormat(formatFlag)
			if err != nil {
				return err
			}
			client := clientFromCtx(cmd.Context())
			user, err := client.CurrentUser(cmd.Context())
			if err != nil {
				return err
			}
			return writeUser(os.Stdout, user, format)
		},
	}
	cmd.Flags().StringVarP(&formatFlag, "format", "f", "table", "Output format: table, json")
	return cmd
}

// writeUser renders a UserInfo in the requested format. The table form
// uses a key:value block — single records don't benefit from column
// alignment, and the key:value shape stays readable when piped into less
// or grep.
//
// The writer is io.Writer (not *os.File) so unit tests can capture
// output into a bytes.Buffer without spawning a real file. The
// production caller passes os.Stdout.
func writeUser(w io.Writer, u gitlab.UserInfo, format cliout.Format) error {
	switch format {
	case cliout.FormatJSON:
		return cliout.PrintJSON(w, u)
	default:
		return cliout.PrintKV(w, []cliout.KV{
			{Key: "ID", Value: strconv.Itoa(u.ID)},
			{Key: "Username", Value: u.Username},
			{Key: "Name", Value: u.Name},
			{Key: "Email", Value: u.Email},
			{Key: "State", Value: u.State},
			{Key: "Web URL", Value: u.WebURL},
			{Key: "Admin", Value: fmt.Sprintf("%t", u.IsAdmin)},
		})
	}
}

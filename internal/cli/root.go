package cli

import (
	"context"
)

// Root builds the command tree.
func Root() *Command {
	return &Command{
		Name:  "tgl",
		Short: "A command-line interface for Tangled issues, pull requests and repositories",
		Long: `Repository context comes from the git remote in the working directory,
or from --repo owner/name.

Every command accepts --json and works non-interactively. Exit codes: 0 success,
1 error, 4 auth failure, 8 not found.

Environment:
  TANGLED_HANDLE, TANGLED_APP_PASSWORD   credentials for CI and agents
  TANGLED_REPO                           default --repo value
  TANGLED_KNOT                           default --knot for repo create
  TANGLED_BOBBIN_HOST                    alternate appview base URL`,
		Sub: []*Command{authCmd(), browseCmd(), issueCmd(), prCmd(), repoCmd(), versionCmd()},
	}
}

func versionCmd() *Command {
	return &Command{
		Name:  "version",
		Short: "Print the tgl version",
		Run: func(ctx context.Context, app *App, args []string) error {
			if app.JSON {
				return app.EmitJSON(map[string]string{"version": Version})
			}
			app.Print("tgl %s", Version)
			return nil
		},
	}
}

package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/1broseidon/cymbal/internal/index"
	"github.com/spf13/cobra"
)

var importersCmd = &cobra.Command{
	Use:   "importers <file|package>",
	Short: "Find files that import a given file or package",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]
		dbPath := getDBPath(cmd)
		jsonOut := getJSONFlag(cmd)
		depth, _ := cmd.Flags().GetInt("depth")
		limit, _ := cmd.Flags().GetInt("limit")

		results, err := index.FindImportersByPath(dbPath, target, depth, limit)
		if err != nil {
			return err
		}

		if len(results) == 0 {
			fmt.Fprintf(os.Stderr, "No importers found for '%s'.\n", target)
			os.Exit(1)
		}

		if jsonOut {
			return writeJSON(results)
		}

		var content strings.Builder
		for _, r := range results {
			fmt.Fprintf(&content, "%s:%s\n", r.RelPath, r.Import)
		}

		frontmatter([]kv{
			{"target", target},
			{"importer_count", fmt.Sprintf("%d", len(results))},
		}, content.String())
		return nil
	},
}

func init() {
	importersCmd.Flags().IntP("depth", "D", 1, "import chain depth (max 3)")
	importersCmd.Flags().IntP("limit", "n", 50, "max results")
	rootCmd.AddCommand(importersCmd)
}

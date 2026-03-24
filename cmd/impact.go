package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/1broseidon/cymbal/internal/index"
	"github.com/spf13/cobra"
)

var impactCmd = &cobra.Command{
	Use:   "impact <symbol>",
	Short: "Transitive caller analysis — what is impacted if this symbol changes",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		dbPath := getDBPath(cmd)
		jsonOut := getJSONFlag(cmd)
		depth, _ := cmd.Flags().GetInt("depth")
		limit, _ := cmd.Flags().GetInt("limit")

		results, err := index.FindImpact(dbPath, name, depth, limit)
		if err != nil {
			return err
		}

		if len(results) == 0 {
			fmt.Fprintf(os.Stderr, "No callers found for '%s'.\n", name)
			os.Exit(1)
		}

		if jsonOut {
			return writeJSON(results)
		}

		// Group results by depth.
		maxDepth := 0
		for _, r := range results {
			if r.Depth > maxDepth {
				maxDepth = r.Depth
			}
		}

		var content strings.Builder
		for d := 1; d <= maxDepth; d++ {
			fmt.Fprintf(&content, "# depth %d\n", d)
			for _, r := range results {
				if r.Depth != d {
					continue
				}
				line := readSourceLine(r.File, r.Line)
				fmt.Fprintf(&content, "%s:%d: %s\n", r.RelPath, r.Line, strings.TrimSpace(line))
			}
		}

		frontmatter([]kv{
			{"symbol", name},
			{"depth", fmt.Sprintf("%d", depth)},
			{"total_callers", fmt.Sprintf("%d", len(results))},
		}, content.String())
		return nil
	},
}

func init() {
	impactCmd.Flags().IntP("depth", "D", 2, "max call-chain depth (max 5)")
	impactCmd.Flags().IntP("limit", "n", 100, "max results")
	rootCmd.AddCommand(impactCmd)
}

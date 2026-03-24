package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/1broseidon/cymbal/internal/index"
	"github.com/spf13/cobra"
)

var refsCmd = &cobra.Command{
	Use:   "refs <symbol>",
	Short: "Find references to a symbol (best-effort)",
	Long: `Find files and lines that reference a symbol name.

Default: shows call-expression references across indexed files.
--importers: shows files that import the file defining this symbol.
--impact: shorthand for --importers --depth 2 (transitive impact).

Note: references are best-effort based on AST name matching, not semantic analysis.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		dbPath := getDBPath(cmd)
		jsonOut := getJSONFlag(cmd)
		importers, _ := cmd.Flags().GetBool("importers")
		impact, _ := cmd.Flags().GetBool("impact")
		depth, _ := cmd.Flags().GetInt("depth")
		limit, _ := cmd.Flags().GetInt("limit")

		if impact {
			importers = true
			if depth < 2 {
				depth = 2
			}
		}

		if importers {
			return refsImporters(dbPath, name, depth, limit, jsonOut)
		}
		return refsSymbol(dbPath, name, limit, jsonOut)
	},
}

func init() {
	refsCmd.Flags().Bool("importers", false, "find files that import the defining file")
	refsCmd.Flags().Bool("impact", false, "transitive impact analysis (--importers --depth 2)")
	refsCmd.Flags().IntP("depth", "D", 1, "import chain depth for --importers (max 3)")
	refsCmd.Flags().IntP("limit", "n", 50, "max results")
	rootCmd.AddCommand(refsCmd)
}

func refsSymbol(dbPath, name string, limit int, jsonOut bool) error {
	results, err := index.FindReferences(dbPath, name, limit)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "No references found for '%s'.\n", name)
		os.Exit(1)
	}

	if jsonOut {
		return writeJSON(results)
	}

	var content strings.Builder
	for _, r := range results {
		line := readSourceLine(r.File, r.Line)
		fmt.Fprintf(&content, "%s:%d: %s\n", r.RelPath, r.Line, strings.TrimSpace(line))
	}

	frontmatter([]kv{
		{"symbol", name},
		{"ref_count", fmt.Sprintf("%d", len(results))},
	}, content.String())
	return nil
}

func refsImporters(dbPath, name string, depth, limit int, jsonOut bool) error {
	results, err := index.FindImporters(dbPath, name, depth, limit)
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "No importers found for '%s'.\n", name)
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
		{"symbol", name},
		{"importer_count", fmt.Sprintf("%d", len(results))},
	}, content.String())
	return nil
}

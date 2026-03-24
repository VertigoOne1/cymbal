package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// writeJSON writes a versioned JSON envelope to stdout.
func writeJSON(data any) error {
	envelope := map[string]any{
		"version": "0.1",
		"results": data,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(envelope)
}

// frontmatter writes YAML-style frontmatter followed by content.
// Keys are printed in the order provided.
func frontmatter(meta []kv, content string) {
	fmt.Println("---")
	for _, m := range meta {
		fmt.Printf("%s: %s\n", m.k, m.v)
	}
	fmt.Println("---")
	if content != "" {
		fmt.Print(content)
		// Ensure trailing newline.
		if !strings.HasSuffix(content, "\n") {
			fmt.Println()
		}
	}
}

// kv is an ordered key-value pair for frontmatter output.
type kv struct {
	k, v string
}

// readSourceLine reads a single line from a file on disk.
// Returns the trimmed-right content or "" on error.
func readSourceLine(path string, lineNum int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	cur := 0
	for scanner.Scan() {
		cur++
		if cur == lineNum {
			return scanner.Text()
		}
	}
	return ""
}

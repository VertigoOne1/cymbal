// bench/main.go — Phase 1 speed benchmark harness for cymbal.
//
// Usage:
//
//	go run ./bench setup   — clone corpus repos into bench/.corpus/
//	go run ./bench run     — execute benchmarks, write bench/RESULTS.md
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ── Corpus config ──────────────────────────────────────────────────

type Corpus struct {
	Repos []Repo `yaml:"repos"`
}

type Repo struct {
	Name     string   `yaml:"name"`
	URL      string   `yaml:"url"`
	Ref      string   `yaml:"ref"`
	Language string   `yaml:"language"`
	Symbols  []string `yaml:"symbols"`
}

// ── Tool abstraction ───────────────────────────────────────────────

// Op is a benchmark operation.
type Op string

const (
	OpIndex   Op = "index"
	OpReindex Op = "reindex"
	OpSearch  Op = "search"
	OpRefs    Op = "refs"
	OpShow    Op = "show"
)

// Tool defines how to invoke a particular tool for each operation.
type Tool struct {
	Name    string
	Binary  string // binary name checked via exec.LookPath
	Ops     map[Op]CmdFunc
	Cleanup func(repoDir string) // optional: called before cold index
}

// CmdFunc builds an exec.Cmd for an operation. symbol is empty for index ops.
type CmdFunc func(repoDir, symbol string) *exec.Cmd

// ── Results ────────────────────────────────────────────────────────

type Result struct {
	Tool    string
	Repo    string
	Op      Op
	Symbol  string
	Timings []time.Duration
	Output  int // bytes of output
}

func (r Result) Median() time.Duration {
	s := make([]time.Duration, len(r.Timings))
	copy(s, r.Timings)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

func (r Result) Min() time.Duration {
	m := r.Timings[0]
	for _, t := range r.Timings[1:] {
		if t < m {
			m = t
		}
	}
	return m
}

// ── Tool definitions ───────────────────────────────────────────────

func cymbalDBPath(repoDir string) string {
	abs, _ := filepath.Abs(repoDir)
	h := sha256.Sum256([]byte(abs))
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cymbal", "repos", hex.EncodeToString(h[:8]), "index.db")
}

func defineTools(cymbalBin string) []Tool {
	return []Tool{
		{
			Name:   "cymbal",
			Binary: cymbalBin,
			Ops: map[Op]CmdFunc{
				OpIndex: func(dir, _ string) *exec.Cmd {
					return exec.Command(cymbalBin, "index", ".")
				},
				OpReindex: func(dir, _ string) *exec.Cmd {
					return exec.Command(cymbalBin, "index", ".")
				},
				OpSearch: func(dir, sym string) *exec.Cmd {
					return exec.Command(cymbalBin, "search", sym)
				},
				OpRefs: func(dir, sym string) *exec.Cmd {
					return exec.Command(cymbalBin, "refs", sym)
				},
				OpShow: func(dir, sym string) *exec.Cmd {
					return exec.Command(cymbalBin, "show", sym)
				},
			},
			Cleanup: func(dir string) {
				os.Remove(cymbalDBPath(dir))
			},
		},
		{
			Name:   "ripgrep",
			Binary: "rg",
			Ops: map[Op]CmdFunc{
				OpSearch: func(dir, sym string) *exec.Cmd {
					return exec.Command("rg", "--no-heading", "-c", sym)
				},
				OpRefs: func(dir, sym string) *exec.Cmd {
					// rg has no semantic refs — just grep for the symbol name.
					return exec.Command("rg", "--no-heading", "-n", sym)
				},
				OpShow: func(dir, sym string) *exec.Cmd {
					// Approximate "show": find definition-like pattern, show context.
					// This is what an agent would actually do without cymbal.
					pattern := "(?:def |func |class |type |interface |struct )" + sym
					return exec.Command("rg", "--no-heading", "-n", "-A", "30", pattern)
				},
			},
		},
		{
			Name:   "ctags",
			Binary: "ctags",
			Ops: map[Op]CmdFunc{
				OpIndex: func(dir, _ string) *exec.Cmd {
					return exec.Command("ctags", "-R", "--fields=+n", ".")
				},
				OpReindex: func(dir, _ string) *exec.Cmd {
					return exec.Command("ctags", "-R", "--fields=+n", ".")
				},
				OpSearch: func(dir, sym string) *exec.Cmd {
					// readtags is the companion query tool for ctags
					return exec.Command("readtags", "-e", "-", sym)
				},
			},
			Cleanup: func(dir string) {
				os.Remove(filepath.Join(dir, "tags"))
			},
		},
	}
}

// ── Core benchmark logic ───────────────────────────────────────────

const (
	indexIters = 3
	queryIters = 10
	warmup     = 1
)

func timeCmd(cmd *exec.Cmd) (time.Duration, int, error) {
	start := time.Now()
	out, err := cmd.CombinedOutput()
	return time.Since(start), len(out), err
}

// preRun is an optional function called before each iteration (e.g., to reset state for cold benchmarks).
type preRun func()

func runBench(tool Tool, op Op, repoDir, symbol string, iters int, before ...preRun) Result {
	r := Result{
		Tool:   tool.Name,
		Repo:   filepath.Base(repoDir),
		Op:     op,
		Symbol: symbol,
	}

	// Warmup runs (discarded).
	for i := 0; i < warmup; i++ {
		for _, fn := range before {
			fn()
		}
		cmd := tool.Ops[op](repoDir, symbol)
		cmd.Dir = repoDir
		cmd.Run()
	}

	for i := 0; i < iters; i++ {
		for _, fn := range before {
			fn()
		}
		cmd := tool.Ops[op](repoDir, symbol)
		cmd.Dir = repoDir
		d, n, err := timeCmd(cmd)
		if err != nil && op != OpSearch {
			// Search may legitimately return no results for some tools.
			fmt.Fprintf(os.Stderr, "  WARN: %s %s %s %s: %v\n", tool.Name, op, r.Repo, symbol, err)
		}
		r.Timings = append(r.Timings, d)
		r.Output = n
	}
	return r
}

// ── Setup command ──────────────────────────────────────────────────

func cmdSetup(corpus Corpus, corpusDir string) error {
	if err := os.MkdirAll(corpusDir, 0o755); err != nil {
		return err
	}

	for _, repo := range corpus.Repos {
		dest := filepath.Join(corpusDir, repo.Name)
		if _, err := os.Stat(dest); err == nil {
			fmt.Printf("  %s: already cloned\n", repo.Name)
			continue
		}
		fmt.Printf("  %s: cloning %s @ %s ...\n", repo.Name, repo.URL, repo.Ref)
		cmd := exec.Command("git", "clone", "--depth=1", "--branch", repo.Ref, repo.URL, dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("cloning %s: %w", repo.Name, err)
		}
	}

	fmt.Println("\nCorpus ready.")
	return nil
}

// ── Run command ────────────────────────────────────────────────────

func cmdRun(corpus Corpus, corpusDir, cymbalBin string) error {
	tools := defineTools(cymbalBin)

	// Check tool availability.
	var available []Tool
	for _, t := range tools {
		if _, err := exec.LookPath(t.Binary); err != nil {
			fmt.Fprintf(os.Stderr, "  SKIP: %s not found (%s)\n", t.Name, t.Binary)
			continue
		}
		available = append(available, t)
	}
	if len(available) == 0 {
		return fmt.Errorf("no tools available")
	}

	var results []Result

	for _, repo := range corpus.Repos {
		dir := filepath.Join(corpusDir, repo.Name)
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("corpus repo %s not found — run: go run ./bench setup", repo.Name)
		}

		fmt.Printf("\n== %s (%s) ==\n", repo.Name, repo.Language)

		for _, tool := range available {
			fmt.Printf("  %s:\n", tool.Name)

			// Index (cold) — cleanup before each iteration for true cold measurement.
			if _, ok := tool.Ops[OpIndex]; ok {
				var before []preRun
				if tool.Cleanup != nil {
					before = append(before, func() { tool.Cleanup(dir) })
				}
				fmt.Printf("    index (cold) ...")
				r := runBench(tool, OpIndex, dir, "", indexIters, before...)
				fmt.Printf(" %v\n", r.Median())
				results = append(results, r)
			}

			// Re-index (warm).
			if _, ok := tool.Ops[OpReindex]; ok {
				fmt.Printf("    reindex ...")
				r := runBench(tool, OpReindex, dir, "", indexIters)
				fmt.Printf(" %v\n", r.Median())
				results = append(results, r)
			}

			// Queries.
			for _, sym := range repo.Symbols {
				for _, op := range []Op{OpSearch, OpRefs, OpShow} {
					if _, ok := tool.Ops[op]; !ok {
						continue
					}
					fmt.Printf("    %s(%s) ...", op, sym)
					r := runBench(tool, op, dir, sym, queryIters)
					fmt.Printf(" %v\n", r.Median())
					results = append(results, r)
				}
			}
		}
	}

	// Generate report.
	report := generateReport(results, available)
	outPath := filepath.Join("bench", "RESULTS.md")
	if err := os.WriteFile(outPath, []byte(report), 0o644); err != nil {
		return err
	}
	fmt.Printf("\nResults written to %s\n", outPath)
	return nil
}

// ── Report generation ──────────────────────────────────────────────

func generateReport(results []Result, tools []Tool) string {
	var b strings.Builder

	b.WriteString("# Cymbal Benchmark Results\n\n")
	b.WriteString(fmt.Sprintf("**Date:** %s  \n", time.Now().Format("2006-01-02 15:04 MST")))
	b.WriteString(fmt.Sprintf("**Platform:** %s/%s  \n", runtime.GOOS, runtime.GOARCH))
	b.WriteString(fmt.Sprintf("**CPU:** %d cores  \n\n", runtime.NumCPU()))

	// Group by repo.
	byRepo := map[string][]Result{}
	for _, r := range results {
		byRepo[r.Repo] = append(byRepo[r.Repo], r)
	}

	// Sorted repo names.
	repos := make([]string, 0, len(byRepo))
	for k := range byRepo {
		repos = append(repos, k)
	}
	sort.Strings(repos)

	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name
	}

	for _, repo := range repos {
		b.WriteString(fmt.Sprintf("## %s\n\n", repo))

		// Index/reindex table.
		b.WriteString("### Indexing\n\n")
		b.WriteString("| Operation |")
		for _, tn := range toolNames {
			b.WriteString(fmt.Sprintf(" %s |", tn))
		}
		b.WriteString("\n|---|")
		for range toolNames {
			b.WriteString("---|")
		}
		b.WriteString("\n")

		for _, op := range []Op{OpIndex, OpReindex} {
			b.WriteString(fmt.Sprintf("| %s |", op))
			for _, tn := range toolNames {
				r := findResult(byRepo[repo], tn, op, "")
				if r == nil {
					b.WriteString(" — |")
				} else {
					b.WriteString(fmt.Sprintf(" %s |", fmtDuration(r.Median())))
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")

		// Query tables — speed.
		b.WriteString("### Query Speed\n\n")
		b.WriteString("| Symbol | Op |")
		for _, tn := range toolNames {
			b.WriteString(fmt.Sprintf(" %s |", tn))
		}
		b.WriteString("\n|---|---|")
		for range toolNames {
			b.WriteString("---|")
		}
		b.WriteString("\n")

		// Collect unique (symbol, op) pairs.
		type symOp struct{ sym, op string }
		seen := map[symOp]bool{}
		var pairs []symOp
		for _, r := range byRepo[repo] {
			if r.Op == OpIndex || r.Op == OpReindex {
				continue
			}
			so := symOp{r.Symbol, string(r.Op)}
			if !seen[so] {
				seen[so] = true
				pairs = append(pairs, so)
			}
		}

		for _, p := range pairs {
			b.WriteString(fmt.Sprintf("| `%s` | %s |", p.sym, p.op))
			for _, tn := range toolNames {
				r := findResult(byRepo[repo], tn, Op(p.op), p.sym)
				if r == nil {
					b.WriteString(" — |")
				} else {
					b.WriteString(fmt.Sprintf(" %s |", fmtDuration(r.Median())))
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")

		// Query tables — output size (token efficiency).
		b.WriteString("### Output Size (Token Efficiency)\n\n")
		b.WriteString("| Symbol | Op |")
		for _, tn := range toolNames {
			b.WriteString(fmt.Sprintf(" %s |", tn))
		}
		b.WriteString("\n|---|---|")
		for range toolNames {
			b.WriteString("---|")
		}
		b.WriteString("\n")

		for _, p := range pairs {
			b.WriteString(fmt.Sprintf("| `%s` | %s |", p.sym, p.op))
			for _, tn := range toolNames {
				r := findResult(byRepo[repo], tn, Op(p.op), p.sym)
				if r == nil {
					b.WriteString(" — |")
				} else {
					tokens := r.Output / 4 // rough cl100k_base approximation
					b.WriteString(fmt.Sprintf(" %s (~%d tok) |", fmtBytes(r.Output), tokens))
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

func findResult(results []Result, tool string, op Op, symbol string) *Result {
	for i := range results {
		r := &results[i]
		if r.Tool == tool && r.Op == op && r.Symbol == symbol {
			return r
		}
	}
	return nil
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.1fµs", float64(d.Microseconds()))
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func fmtBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	return fmt.Sprintf("%.1fKB", float64(n)/1024)
}

// ── Entrypoint ─────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: go run ./bench [setup|run]\n")
		os.Exit(1)
	}

	corpusFile := filepath.Join("bench", "corpus.yaml")
	data, err := os.ReadFile(corpusFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading %s: %v\n", corpusFile, err)
		os.Exit(1)
	}

	var corpus Corpus
	if err := yaml.Unmarshal(data, &corpus); err != nil {
		fmt.Fprintf(os.Stderr, "parsing %s: %v\n", corpusFile, err)
		os.Exit(1)
	}

	corpusDir := filepath.Join("bench", ".corpus")

	// Resolve cymbal binary: prefer freshly built one.
	cymbalBin := "cymbal"
	if bin, err := exec.LookPath("./cymbal"); err == nil {
		cymbalBin, _ = filepath.Abs(bin)
	} else if bin, err := exec.LookPath("cymbal"); err == nil {
		cymbalBin = bin
	}

	switch os.Args[1] {
	case "setup":
		fmt.Println("Setting up benchmark corpus...")
		if err := cmdSetup(corpus, corpusDir); err != nil {
			fmt.Fprintf(os.Stderr, "setup: %v\n", err)
			os.Exit(1)
		}
	case "run":
		fmt.Println("Running benchmarks...")
		fmt.Printf("Using cymbal: %s\n", cymbalBin)
		if err := cmdRun(corpus, corpusDir, cymbalBin); err != nil {
			fmt.Fprintf(os.Stderr, "run: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nUsage: go run ./bench [setup|run]\n", os.Args[1])
		os.Exit(1)
	}
}

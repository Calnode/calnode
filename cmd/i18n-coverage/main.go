// Command i18n-coverage prints how much of each catalogue every shipped language
// carries.
//
// It is a progress report, not a gate: the tests in internal/i18n are what fail a
// build. This exists because a translator needs to see 512/1324 rather than guess, and
// a reviewer needs to see whether a language is close enough to offer before reading it.
//
// Run it from the repository root:
//
//	go run ./cmd/i18n-coverage
//	make i18n-coverage
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/calnode/calnode/internal/i18n"
)

type catalogue struct {
	name string
	dir  string
	// floor is the coverage below which the language is not offered to visitors.
	// Zero means "no floor for this catalogue yet".
	floor float64
}

const sourceCode = "en"

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	catalogues := []catalogue{
		{name: "booker (public surfaces)", dir: "internal/i18n/locales", floor: i18n.MinCoverage},
		{name: "admin (SPA)", dir: "frontend/src/lib/i18n", floor: 0},
	}

	for _, c := range catalogues {
		if err := report(*root, c); err != nil {
			fmt.Fprintf(os.Stderr, "i18n-coverage: %s: %v\n", c.dir, err)
			os.Exit(1)
		}
	}
}

func report(root string, c catalogue) error {
	dir := filepath.Join(root, c.dir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		fmt.Printf("%s\n  %s does not exist yet, skipped\n\n", c.name, c.dir)
		return nil
	}
	if err != nil {
		return err
	}

	source, err := load(filepath.Join(dir, sourceCode+".json"))
	if err != nil {
		return err
	}
	if len(source) == 0 {
		return fmt.Errorf("%s has no strings", filepath.Join(dir, sourceCode+".json"))
	}

	type row struct {
		code    string
		keys    int
		cover   float64
		unknown int
	}
	var rows []row
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		code := strings.TrimSuffix(e.Name(), ".json")
		strings_, err := load(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		r := row{code: code, keys: len(strings_)}
		for k := range strings_ {
			if _, ok := source[k]; ok {
				r.cover++
			} else {
				r.unknown++
			}
		}
		r.cover = float64(r.cover) / float64(len(source))
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].code < rows[j].code })

	fmt.Printf("%s\n  %s (source %s.json, %d keys)\n\n", c.name, c.dir, sourceCode, len(source))
	fmt.Printf("  %-7s %6s %8s %10s %8s\n", "code", "keys", "coverage", "status", "unknown")
	for _, r := range rows {
		status := "source"
		switch {
		case r.code == sourceCode:
		case r.unknown > 0:
			status = "has unknown keys"
		case c.floor > 0 && r.cover < c.floor:
			status = "withheld"
		default:
			status = "offered"
		}
		fmt.Printf("  %-7s %6d %7.0f%% %10s %8d\n", r.code, r.keys, r.cover*100, status, r.unknown)
	}
	if c.floor > 0 {
		fmt.Printf("\n  floor: %.0f%% (below it a language is not offered; ?lang=<code> still works)\n", c.floor*100)
	}
	fmt.Println()
	return nil
}

// load reads one catalogue file and returns its flattened key set. Nested objects are
// joined with dots, so the admin catalogue's plural members count individually
// ("common.save", "teams.memberCount.one", ...). Arrays are not used in either
// catalogue and are skipped rather than guessed at.
func load(path string) (map[string]struct{}, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]struct{}{}
	flatten("", raw, out)
	return out, nil
}

func flatten(prefix string, v any, out map[string]struct{}) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, child, out)
		}
	case string:
		out[prefix] = struct{}{}
	}
}

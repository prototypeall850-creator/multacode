package tools

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Output caps keep transcripts and sessions bounded (plan.md context rules).
const (
	maxOutputBytes = 32 * 1024
	maxListEntries = 200
	maxSearchHits  = 50
)

// ignoreDirs skips VCS, deps, and build output by default.
var ignoreDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true,
	"target": true, "__pycache__": true, ".cache": true,
	"dist": true, "build": true,
}

// --- list_files ---

type ListFiles struct{ Root string }

func (t *ListFiles) Name() string        { return "list_files" }
func (t *ListFiles) Description() string { return "List files under project root" }
func (t *ListFiles) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"dir": map[string]any{"type": "string", "description": "subdirectory relative to project root"},
		},
	}
}

func (t *ListFiles) Run(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Dir string `json:"dir"`
	}
	_ = json.Unmarshal(input, &in)
	base := t.Root
	if in.Dir != "" {
		base = filepath.Join(t.Root, filepath.Clean(in.Dir))
	}
	var out []string
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(t.Root, p)
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			if p != base && (ignoreDirs[info.Name()] || strings.HasPrefix(info.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		out = append(out, rel)
		if len(out) >= maxListEntries {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	sort.Strings(out)
	return Result{Output: strings.Join(out, "\n"), Truncated: len(out) >= maxListEntries}, nil
}

// --- search_files ---

type SearchFiles struct{ Root string }

func (t *SearchFiles) Name() string { return "search_files" }
func (t *SearchFiles) Description() string {
	return "Search file contents (rg if available, else Go walk)"
}
func (t *SearchFiles) Schema() any {
	return map[string]any{
		"type":     "object",
		"required": []string{"query"},
		"properties": map[string]any{
			"query":       map[string]any{"type": "string"},
			"include":     map[string]any{"type": "string", "description": "glob like *.go"},
			"max_results": map[string]any{"type": "integer"},
		},
	}
}

func (t *SearchFiles) Run(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Query      string `json:"query"`
		Include    string `json:"include"`
		MaxResults int    `json:"max_results"`
	}
	_ = json.Unmarshal(input, &in)
	if strings.TrimSpace(in.Query) == "" {
		return Result{}, errEmptyQuery
	}
	max := in.MaxResults
	if max <= 0 || max > maxSearchHits {
		max = maxSearchHits
	}
	if _, err := exec.LookPath("rg"); err == nil {
		return t.runRG(ctx, in.Query, in.Include, max)
	}
	return t.runWalk(ctx, in.Query, in.Include, max)
}

func (t *SearchFiles) runRG(ctx context.Context, query, include string, max int) (Result, error) {
	args := []string{"--line-number", "--no-heading", "--max-count", "5",
		"--max-count-matches", "5", "-m", strconv.Itoa(max),
		"--glob", "!{.git,node_modules,vendor,target}/**"}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, "--", query, t.Root)
	cmd := exec.CommandContext(ctx, "rg", args...)
	out, err := cmd.Output()
	lines := trimLines(string(out), max)
	// rg exits 1 when no match; that is not a failure.
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			return Result{}, err
		}
	}
	if len(lines) == 0 {
		return Result{Output: "(no matches)"}, nil
	}
	return Result{Output: RedactSecrets(strings.Join(lines, "\n")), Truncated: len(lines) >= max}, nil
}

func (t *SearchFiles) runWalk(ctx context.Context, query, include string, max int) (Result, error) {
	var hits []string
	_ = filepath.Walk(t.Root, func(p string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return filepath.SkipAll
		default:
		}
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && ignoreDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Size() > 512*1024 {
			return nil
		}
		if include != "" {
			ok, _ := filepath.Match(include, info.Name())
			if !ok {
				return nil
			}
		}
		if !isTextName(info.Name()) {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(t.Root, p)
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, query) {
				hits = append(hits, rel+":"+strconv.Itoa(i+1)+":"+strings.TrimSpace(line))
				if len(hits) >= max {
					return filepath.SkipAll
				}
				if countFileHits(hits, rel) >= 5 {
					break
				}
			}
		}
		return nil
	})
	if len(hits) == 0 {
		return Result{Output: "(no matches)"}, nil
	}
	return Result{Output: RedactSecrets(strings.Join(hits, "\n")), Truncated: len(hits) >= max}, nil
}

func countFileHits(hits []string, file string) int {
	n := 0
	for _, h := range hits {
		if strings.HasPrefix(h, file+":") {
			n++
		}
	}
	return n
}

func isTextName(name string) bool {
	// Skip common binary formats; everything else gets a bounded read.
	l := strings.ToLower(name)
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf",
		".zip", ".tar", ".gz", ".bin", ".exe", ".so", ".o", ".a", ".woff", ".woff2", ".ttf"} {
		if strings.HasSuffix(l, ext) {
			return false
		}
	}
	return true
}

func trimLines(s string, max int) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
		if len(out) >= max {
			break
		}
	}
	return out
}

// --- read_file ---

type ReadFile struct{ Root string }

func (t *ReadFile) Name() string        { return "read_file" }
func (t *ReadFile) Description() string { return "Read bounded file content" }
func (t *ReadFile) Schema() any {
	return map[string]any{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]any{
			"path":   map[string]any{"type": "string"},
			"offset": map[string]any{"type": "integer", "description": "1-based first line"},
			"limit":  map[string]any{"type": "integer", "description": "max lines"},
		},
	}
}

func (t *ReadFile) Run(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	_ = json.Unmarshal(input, &in)
	if strings.TrimSpace(in.Path) == "" {
		return Result{}, errEmptyPath
	}
	if isSecretPath(in.Path) {
		return Result{}, errSecretBlocked
	}
	abs, err := resolveInRoot(t.Root, in.Path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, err
	}
	if len(data) > maxOutputBytes && in.Limit <= 0 {
		return Result{}, errRangeRequired
	}
	lines := strings.Split(string(data), "\n")
	start := in.Offset - 1
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return Result{Output: "(offset beyond EOF)"}, nil
	}
	end := len(lines)
	if in.Limit > 0 && start+in.Limit < end {
		end = start + in.Limit
	}
	out := strings.Join(lines[start:end], "\n")
	truncated := end < len(lines)
	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes]
		truncated = true
	}
	return Result{Output: RedactSecrets(out), Truncated: truncated}, nil
}

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EditFile applies exact string replacement or whole-file creation.
// Every Run goes through PreviewEdit first so the TUI approval modal
// and the transcript show the same diff that gets applied.
type EditFile struct{ Root string }

func (t *EditFile) Name() string { return "edit_file" }
func (t *EditFile) Description() string {
	return "Replace one exact string in a file, or create a new file. Shows a diff before approval."
}
func (t *EditFile) Schema() any {
	return map[string]any{
		"type":     "object",
		"required": []string{"path"},
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "relative to project root"},
			"old":    map[string]any{"type": "string", "description": "exact string to replace (exactly one match required)"},
			"new":    map[string]any{"type": "string", "description": "replacement text"},
			"create": map[string]any{"type": "boolean", "description": "true to create a new file with content from new"},
		},
	}
}

// EditInput is the parsed tool payload.
type EditInput struct {
	Path   string `json:"path"`
	Old    string `json:"old"`
	New    string `json:"new"`
	Create bool   `json:"create"`
}

func parseEditInput(raw json.RawMessage) (EditInput, error) {
	var in EditInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, fmt.Errorf("bad edit input: %v", err)
	}
	if strings.TrimSpace(in.Path) == "" {
		return in, errEmptyPath
	}
	return in, nil
}

// resolveInRoot confines a relative path to the project root.
func resolveInRoot(root, p string) (string, error) {
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("absolute paths not allowed: %s", p)
	}
	abs := filepath.Join(root, filepath.Clean(p))
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path outside project root: %s", p)
	}
	return abs, nil
}

// PreviewEdit computes the diff without writing. Shared by Run and the TUI modal.
func PreviewEdit(root string, raw json.RawMessage) (rel, diff string, err error) {
	in, err := parseEditInput(raw)
	if err != nil {
		return "", "", err
	}
	if isSecretPath(in.Path) {
		return "", "", errSecretBlocked
	}
	abs, err := resolveInRoot(root, in.Path)
	if err != nil {
		return "", "", err
	}
	rel = filepath.ToSlash(in.Path)
	if in.Create {
		if _, statErr := os.Stat(abs); statErr == nil {
			return "", "", fmt.Errorf("%s already exists (omit create to edit it)", rel)
		}
		return rel, UnifiedDiff(rel, nil, splitEditLines(in.New)), nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", "", err
	}
	if in.Old == "" {
		return "", "", errors.New("old string is empty (use create:true for new files)")
	}
	switch n := strings.Count(string(data), in.Old); {
	case n == 0:
		return "", "", fmt.Errorf("old string not found in %s", rel)
	case n > 1:
		return "", "", fmt.Errorf("old string matches %d times in %s; narrow it to exactly one", n, rel)
	}
	after := strings.Replace(string(data), in.Old, in.New, 1)
	return rel, UnifiedDiff(rel, splitEditLines(string(data)), splitEditLines(after)), nil
}

func (t *EditFile) Run(ctx context.Context, input json.RawMessage) (Result, error) {
	in, err := parseEditInput(input)
	if err != nil {
		return Result{}, err
	}
	rel, diff, err := PreviewEdit(t.Root, input)
	if err != nil {
		return Result{}, err
	}
	abs, err := resolveInRoot(t.Root, in.Path)
	if err != nil {
		return Result{}, err
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if in.Create {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Result{}, err
		}
		if err := os.WriteFile(abs, []byte(in.New), 0o644); err != nil {
			return Result{}, err
		}
	} else {
		data, err := os.ReadFile(abs)
		if err != nil {
			return Result{}, err
		}
		after := strings.Replace(string(data), in.Old, in.New, 1)
		if err := os.WriteFile(abs, []byte(after), 0o644); err != nil {
			return Result{}, err
		}
	}
	out := fmt.Sprintf("edited %s\n%s", rel, diff)
	if len(out) > maxOutputBytes {
		out = out[:maxOutputBytes] + "\n(truncated)"
	}
	return Result{Output: out}, nil
}

func splitEditLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// UnifiedDiff renders one hunk with 3 lines of context.
func UnifiedDiff(path string, before, after []string) string {
	start := 0
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	endB, endA := len(before), len(after)
	for endB > start && endA > start && before[endB-1] == after[endA-1] {
		endB--
		endA--
	}
	cs := max(start-3, 0)
	ceB := min(endB+3, len(before))
	ceA := min(endA+3, len(after))
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", path, path)
	fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", cs+1, ceB-cs, cs+1, ceA-cs)
	for i := cs; i < start; i++ {
		sb.WriteString(" " + before[i] + "\n")
	}
	for i := start; i < endB; i++ {
		sb.WriteString("-" + before[i] + "\n")
	}
	for i := start; i < endA; i++ {
		sb.WriteString("+" + after[i] + "\n")
	}
	for i := endB; i < ceB; i++ {
		sb.WriteString(" " + before[i] + "\n")
	}
	out := sb.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > 200 {
		lines = append(lines[:200], "…(diff truncated)")
	}
	return strings.Join(lines, "\n")
}

package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"multacode/internal/tools"
)

// stubWebTool returns canned output for slash-command tests.
type stubWebTool struct {
	name string
	out  string
	err  error
}

func (s stubWebTool) Name() string        { return s.name }
func (s stubWebTool) Description() string { return "stub" }
func (s stubWebTool) Schema() any         { return map[string]any{"type": "object"} }
func (s stubWebTool) Run(ctx context.Context, input json.RawMessage) (tools.Result, error) {
	if s.err != nil {
		return tools.Result{}, s.err
	}
	return tools.Result{Output: s.out}, nil
}

func runSlashCmd(t *testing.T, m Model, line string) Model {
	t.Helper()
	m.ta.SetValue(line)
	updated, cmd := m.Update(keyMsg("enter"))
	m = updated.(Model)
	if cmd == nil {
		return m
	}
	updated, _ = m.Update(cmd())
	return updated.(Model)
}

func TestFetchSlashTracksSources(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.registry["web_fetch"] = stubWebTool{name: "web_fetch",
		out: "URL: https://go.dev/\nTitle: Go Home\n\nhello"}
	m = runSlashCmd(t, m, "/fetch https://go.dev/")
	found := false
	for _, e := range m.entries {
		if e.role == "tool" && strings.Contains(e.content, "Go Home") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fetch output missing: %+v", m.entries)
	}
	m = sendLine(t, m, "/sources")
	listed := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "https://go.dev/") && strings.Contains(e.content, "Go Home") {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("source not tracked: %+v", m.entries)
	}
}

func TestSearchSlashUnconfigured(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	// Default registry has no search provider: honest error, no crash.
	m = runSlashCmd(t, m, "/search golang generics")
	found := false
	for _, e := range m.entries {
		if e.role == "error" && strings.Contains(e.content, "no provider") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected no-provider error: %+v", m.entries)
	}
}

func TestSearchSlashWithBackendTracksSources(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.registry["web_search"] = stubWebTool{name: "web_search",
		out: "web_search \"go\": 1 result(s)\n\n1. Go (go.dev)\n   URL: https://go.dev/\n   lang"}
	m = runSlashCmd(t, m, "/search go")
	m = sendLine(t, m, "/sources")
	listed := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "https://go.dev/") {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("search source not tracked: %+v", m.entries)
	}
}

func TestSourcesEmpty(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "/sources")
	found := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "none yet") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected empty hint: %+v", m.entries)
	}
}

func TestFetchUsage(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m = sendLine(t, m, "/fetch")
	found := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "Usage: /fetch") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected usage: %+v", m.entries)
	}
}

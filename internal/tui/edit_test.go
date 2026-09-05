package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"multacode/internal/provider"
)

func editProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func pumpToApproval(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 10 && m.approval == nil; i++ {
		if m.evCh == nil {
			break
		}
		updated, _ := m.Update(waitProviderEvent(m.evCh)())
		m = updated.(Model)
	}
	return m
}

func TestEditApprovalShowsDiff(t *testing.T) {
	root := editProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.prov = &scriptProvider{scripts: [][]provider.Event{
		{{Type: "tool_call", ToolCall: provider.ToolCall{ID: "1", Name: "edit_file",
			Input: []byte(`{"path":"note.txt","old":"world","new":"mars"}`)}}},
		{{Type: "text_delta", TextDelta: "done"}},
	}}
	m = sendLine(t, m, "change it")
	m = pumpToApproval(t, m)
	if m.approval == nil || m.approval.tool != "edit_file" {
		t.Fatalf("expected edit approval: %+v", m.approval)
	}
	view := m.View()
	for _, want := range []string{"note.txt", "-hello world", "+hello mars", "@@"} {
		if !strings.Contains(view, want) {
			t.Fatalf("modal missing %q:\n%s", want, view)
		}
	}
	m = drainLoop(t, m) // approve
	data, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(data) != "hello mars\n" {
		t.Fatalf("content = %q", data)
	}
	applied := false
	for _, e := range m.entries {
		if e.role == "tool" && strings.Contains(e.content, "edited note.txt") {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("apply not recorded: %+v", m.entries)
	}
}

func TestEditDenyKeepsFile(t *testing.T) {
	root := editProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.prov = &scriptProvider{scripts: [][]provider.Event{
		{{Type: "tool_call", ToolCall: provider.ToolCall{ID: "1", Name: "edit_file",
			Input: []byte(`{"path":"note.txt","old":"world","new":"mars"}`)}}},
		{{Type: "text_delta", TextDelta: "ok"}},
	}}
	m = sendLine(t, m, "change it")
	m = pumpToApproval(t, m)
	if m.approval == nil {
		t.Fatal("expected approval")
	}
	updated, _ := m.Update(keyMsg("n"))
	m = updated.(Model)
	m = drainLoop(t, m)
	data, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(data) != "hello world\n" {
		t.Fatalf("denied edit was applied: %q", data)
	}
	denied := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "declined") {
			denied = true
		}
	}
	if !denied {
		t.Fatalf("denial not recorded: %+v", m.entries)
	}
}

func TestPlanAgentCannotEdit(t *testing.T) {
	root := editProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.agent = "plan"
	m.agentCtx.Agent = "plan"
	m.prov = &scriptProvider{scripts: [][]provider.Event{
		{{Type: "tool_call", ToolCall: provider.ToolCall{ID: "1", Name: "edit_file",
			Input: []byte(`{"path":"note.txt","old":"world","new":"mars"}`)}}},
		{{Type: "text_delta", TextDelta: "cannot"}},
	}}
	m = sendLine(t, m, "change it")
	m = drainLoop(t, m)
	if m.approval != nil {
		t.Fatal("plan agent must not reach approval")
	}
	data, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(data) != "hello world\n" {
		t.Fatalf("plan edit was applied: %q", data)
	}
	blocked := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "read-only") {
			blocked = true
		}
	}
	if !blocked {
		t.Fatalf("block not recorded clearly: %+v", m.entries)
	}
}

func TestCreateFileSkipsApproval(t *testing.T) {
	root := editProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.prov = &scriptProvider{scripts: [][]provider.Event{
		{{Type: "tool_call", ToolCall: provider.ToolCall{ID: "1", Name: "edit_file",
			Input: []byte(`{"path":"kalkulator.html","create":true,"new":"<h1>hi</h1>"}`)}}},
		{{Type: "text_delta", TextDelta: "done"}},
	}}
	m = sendLine(t, m, "buatkan kalkulator")
	m = drainLoop(t, m)
	if m.approval != nil {
		t.Fatal("new file must not pop approval")
	}
	data, err := os.ReadFile(filepath.Join(root, "kalkulator.html"))
	if err != nil || !strings.Contains(string(data), "<h1>hi</h1>") {
		t.Fatalf("file not written: %v %q", err, data)
	}
}

func TestCreateExistingStillAsks(t *testing.T) {
	root := editProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.prov = &scriptProvider{scripts: [][]provider.Event{
		{{Type: "tool_call", ToolCall: provider.ToolCall{ID: "1", Name: "edit_file",
			Input: []byte(`{"path":"note.txt","create":true,"new":"x"}`)}}},
		{{Type: "text_delta", TextDelta: "done"}},
	}}
	m = sendLine(t, m, "overwrite it")
	m = pumpToApproval(t, m)
	if m.approval == nil {
		t.Fatal("clobbering an existing file must still ask")
	}
}

func TestAutoCreatableGuards(t *testing.T) {
	root := t.TempDir()
	if isAutoCreatable(root, `{"path":"a/b.py","create":true}`) != true {
		t.Fatal("nested new file should be creatable")
	}
	for _, in := range []string{
		`{"path":"a.py"}`,                       // no create flag
		`{"path":"../escape.py","create":true}`, // root escape
		`{"path":"/abs.py","create":true}`,      // absolute
		`not json`,                              // garbage
	} {
		if isAutoCreatable(root, in) {
			t.Fatalf("must not autocreate: %s", in)
		}
	}
	os.WriteFile(filepath.Join(root, "e.py"), []byte("x"), 0o644)
	if isAutoCreatable(root, `{"path":"e.py","create":true}`) {
		t.Fatal("existing file must not autocreate")
	}
}

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"multacode/internal/provider"
)

// scriptProvider replays queued batches, one per Stream call.
type scriptProvider struct {
	scripts [][]provider.Event
}

func (s *scriptProvider) Name() string { return "script" }
func (s *scriptProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "s"}}, nil
}
func (s *scriptProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Event, error) {
	var batch []provider.Event
	if len(s.scripts) > 0 {
		batch = s.scripts[0]
		s.scripts = s.scripts[1:]
	}
	ch := make(chan provider.Event, len(batch)+1)
	go func() {
		defer close(ch)
		for _, ev := range batch {
			ch <- ev
		}
		ch <- provider.Event{Type: "done"}
	}()
	return ch, nil
}

// drainLoop mimics the runtime: pull provider events until the turn ends,
// approving any ask-gated tool call.
func drainLoop(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 200 && (m.generating || m.approval != nil); i++ {
		if m.approval != nil {
			updated, _ := m.Update(keyMsg("y"))
			m = updated.(Model)
			continue
		}
		if m.evCh == nil {
			break
		}
		next := waitProviderEvent(m.evCh)()
		updated, _ := m.Update(next)
		m = updated.(Model)
	}
	if m.generating || m.approval != nil {
		t.Fatal("loop did not settle")
	}
	return m
}

func testProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.go"), []byte("package main\n// hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SOUL.md"), []byte("You are MultaCode test soul."), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAtFileAttachesContent(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "explain @hello.go")
	attached := false
	for _, e := range m.entries {
		if e.role == "system" && strings.Contains(e.content, "@hello.go") && strings.Contains(e.content, "lines") {
			attached = true
		}
	}
	if !attached {
		t.Fatalf("missing attach note: %+v", m.entries)
	}
	// Fake stream has no tools; drain to finish.
	for i := 0; i < 50 && m.generating; i++ {
		updated, _ := m.Update(waitProviderEvent(m.evCh)())
		m = updated.(Model)
	}
}

func TestBangCommandFlow(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.ta.SetValue("!ls")
	updated, cmd := m.Update(keyMsg("enter"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected bang command")
	}
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(Model)
	found := false
	for _, e := range m.entries {
		if e.role == "tool" && strings.Contains(e.content, "hello.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("bang output missing: %+v", m.entries)
	}
}

func TestBangDestructiveBlocked(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "!rm -rf /")
	for _, e := range m.entries {
		if e.role == "error" && strings.Contains(e.content, "blocked") {
			return
		}
	}
	t.Fatalf("expected policy block: %+v", m.entries)
}

func TestSoulMemoryPermissionsViews(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "/soul")
	m = sendLine(t, m, "/memory")
	m = sendLine(t, m, "/permissions")
	var soul, mem, perm bool
	for _, e := range m.entries {
		switch {
		case strings.Contains(e.content, "SOUL layers"):
			soul = true
			if !strings.Contains(e.content, "test soul") && !strings.Contains(e.content, "active") {
				t.Fatalf("soul view: %q", e.content)
			}
		case strings.Contains(e.content, "memory (read-only"):
			mem = true
		case strings.Contains(e.content, "permissions — agent:"):
			perm = true
		}
	}
	if !soul || !mem || !perm {
		t.Fatalf("views missing soul=%v mem=%v perm=%v", soul, mem, perm)
	}
}

func TestApprovalModalApproveRunsTool(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.prov = &scriptProvider{scripts: [][]provider.Event{
		{{Type: "tool_call", ToolCall: provider.ToolCall{ID: "1", Name: "run_shell", Input: []byte(`{"command":"echo approved-hi"}`)}}},
		{{Type: "text_delta", TextDelta: "done-summary"}},
	}}
	m = sendLine(t, m, "run it")
	// tool_call, done, then channel close -> approval modal.
	updated, _ := m.Update(waitProviderEvent(m.evCh)())
	m = updated.(Model)
	updated, _ = m.Update(waitProviderEvent(m.evCh)()) // done
	m = updated.(Model)
	updated, _ = m.Update(waitProviderEvent(m.evCh)()) // stream end
	m = updated.(Model)
	if m.approval == nil || m.approval.tool != "run_shell" {
		t.Fatalf("expected approval modal: %+v", m.approval)
	}
	if !strings.Contains(m.View(), "approval") {
		t.Fatal("modal not rendered")
	}
	m = drainLoop(t, m) // approves, runs echo, continues, finishes
	foundOut, foundFinal := false, false
	for _, e := range m.entries {
		if e.role == "tool" && strings.Contains(e.content, "approved-hi") {
			foundOut = true
		}
		if e.role == "assistant" && strings.Contains(e.content, "done-summary") {
			foundFinal = true
		}
	}
	if !foundOut || !foundFinal {
		t.Fatalf("out=%v final=%v entries=%+v", foundOut, foundFinal, m.entries)
	}
}

func TestApprovalDenyRecordsAndContinues(t *testing.T) {
	root := testProject(t)
	m := NewModelWithOptions(Options{ProjectDir: root})
	m.width, m.height = 80, 30
	m.resize()
	m.prov = &scriptProvider{scripts: [][]provider.Event{
		{{Type: "tool_call", ToolCall: provider.ToolCall{ID: "1", Name: "run_shell", Input: []byte(`{"command":"echo no"}`)}}},
		{{Type: "text_delta", TextDelta: "after-deny"}},
	}}
	m = sendLine(t, m, "run it")
	updated, _ := m.Update(waitProviderEvent(m.evCh)())
	m = updated.(Model)
	updated, _ = m.Update(waitProviderEvent(m.evCh)())
	m = updated.(Model)
	updated, _ = m.Update(waitProviderEvent(m.evCh)())
	m = updated.(Model)
	if m.approval == nil {
		t.Fatal("expected approval modal")
	}
	updated, _ = m.Update(keyMsg("n"))
	m = updated.(Model)
	if m.approval != nil {
		t.Fatal("modal should close on deny")
	}
	m = drainLoop(t, m)
	denied, final := false, false
	for _, e := range m.entries {
		if (e.role == "tool") && strings.Contains(e.content, "declined") {
			denied = true
		}
		if e.role == "assistant" && strings.Contains(e.content, "after-deny") {
			final = true
		}
	}
	if !denied || !final {
		t.Fatalf("denied=%v final=%v entries=%+v", denied, final, m.entries)
	}
}

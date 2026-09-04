package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"multacode/internal/config"
	"multacode/internal/provider"
	"multacode/internal/session"
)

// errProvider fails Stream immediately (error-state tests).
type errProvider struct{ msg string }

func (e errProvider) Name() string { return "err" }
func (e errProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return nil, context.Canceled
}
func (e errProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Event, error) {
	return nil, errTestOf(e.msg)
}

type errTestString string

func (s errTestString) Error() string { return string(s) }

func errTestOf(s string) error { return errTestString(s) }

func sessionOpts(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	sessDir := filepath.Join(t.TempDir(), "sessions")
	return Options{
		ProjectDir: root,
		Paths:      config.Paths{SessionDir: sessDir},
	}
}

func drainFake(t *testing.T, m Model) Model {
	t.Helper()
	for i := 0; i < 50 && m.generating; i++ {
		updated, _ := m.Update(waitProviderEvent(m.evCh)())
		m = updated.(Model)
	}
	return m
}

func TestPersistAndResumeRoundtrip(t *testing.T) {
	opts := sessionOpts(t)
	m := NewModelWithOptions(opts)
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "hello-direktori")
	m = drainFake(t, m)

	ids, err := session.List(opts.Paths.SessionDir)
	if err != nil || len(ids) != 1 {
		t.Fatalf("ids=%v err=%v", ids, err)
	}
	// New model instance resumes the saved transcript.
	m2 := NewModelWithOptions(opts)
	m2.width, m2.height = 80, 30
	m2.resize()
	m2 = sendLine(t, m2, "/sessions "+ids[0])
	foundUser, foundAsst := false, false
	for _, e := range m2.entries {
		if e.role == "user" && strings.Contains(e.content, "hello-direktori") {
			foundUser = true
		}
		if e.role == "assistant" {
			foundAsst = true
		}
	}
	if !foundUser || !foundAsst {
		t.Fatalf("resume missing user=%v asst=%v: %+v", foundUser, foundAsst, m2.entries)
	}
	if m2.sessionID != ids[0] {
		t.Fatalf("session id = %q want %q", m2.sessionID, ids[0])
	}
}

func TestSessionPickerResumeAndDelete(t *testing.T) {
	opts := sessionOpts(t)
	m := NewModelWithOptions(opts)
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "topik-xyz")
	m = drainFake(t, m)

	m = sendLine(t, m, "/sessions")
	if m.sessPicker == nil || len(m.sessPicker.rows) != 1 {
		t.Fatalf("picker = %+v", m.sessPicker)
	}
	if !strings.Contains(m.View(), "topik-xyz") {
		t.Fatal("picker should preview first message")
	}
	// Delete via d.
	updated, _ := m.Update(keyMsg("d"))
	m = updated.(Model)
	if m.sessPicker != nil {
		t.Fatal("picker should close when empty")
	}
	if ids, _ := session.List(opts.Paths.SessionDir); len(ids) != 0 {
		t.Fatalf("ids = %v", ids)
	}
}

func TestSessionPickerEnterResumes(t *testing.T) {
	opts := sessionOpts(t)
	m := NewModelWithOptions(opts)
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "ingat-ini")
	m = drainFake(t, m)
	oldID := m.sessionID

	m = sendLine(t, m, "/new")
	if m.sessionID == oldID {
		t.Fatal("new session should rotate id")
	}
	if len(m.entries) != 1 {
		t.Fatalf("entries after new = %+v", m.entries)
	}
	m = sendLine(t, m, "/sessions")
	if m.sessPicker == nil || len(m.sessPicker.rows) != 2 {
		t.Fatalf("picker = %+v", m.sessPicker)
	}
	// Newest first: cursor 0 is the fresh /new session; move down to "ingat-ini".
	updated, _ := m.Update(keyMsg("down"))
	m = updated.(Model)
	updated, _ = m.Update(keyMsg("enter"))
	m = updated.(Model)
	if m.sessPicker != nil {
		t.Fatal("picker should close after resume")
	}
	found := false
	for _, e := range m.entries {
		if e.role == "user" && strings.Contains(e.content, "ingat-ini") {
			found = true
		}
	}
	if !found {
		t.Fatalf("resumed transcript missing: %+v", m.entries)
	}
}

func TestCompactPrunes(t *testing.T) {
	opts := sessionOpts(t)
	m := NewModelWithOptions(opts)
	m.width, m.height = 80, 30
	m.resize()
	// Build a long transcript deterministically.
	for i := 0; i < 12; i++ {
		m.entries = append(m.entries, entry{role: "user", content: "q"})
		m.entries = append(m.entries, entry{role: "assistant", content: "a"})
		m.entries = append(m.entries, entry{role: "tool", content: "edited f.go\n-hi\n+ho"})
	}
	before := len(m.entries)
	m = sendLine(t, m, "/compact")
	if len(m.entries) >= before {
		t.Fatalf("compact did not prune: %d -> %d", before, len(m.entries))
	}
	summary := false
	for _, e := range m.entries {
		if e.role == "system" && strings.Contains(e.content, "compacted") && strings.Contains(e.content, "f.go") {
			summary = true
		}
	}
	if !summary {
		t.Fatalf("summary missing: %+v", m.entries)
	}
}

func TestDoctorReport(t *testing.T) {
	opts := sessionOpts(t)
	m := NewModelWithOptions(opts)
	m.width, m.height = 80, 30
	m.resize()
	m.ta.SetValue("/doctor")
	updated, cmd := m.Update(keyMsg("enter"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("expected doctor command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	found := false
	for _, e := range m.entries {
		if strings.HasPrefix(e.content, "doctor:") &&
			strings.Contains(e.content, "tool go") &&
			strings.Contains(e.content, "providers: none") {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor report missing: %+v", m.entries)
	}
	// Env profile refreshed into prompt context.
	if m.agentCtx.Env.OS == "" {
		t.Fatal("env profile not refreshed")
	}
}

func TestProviderErrorHint(t *testing.T) {
	opts := sessionOpts(t)
	m := NewModelWithOptions(opts)
	m.width, m.height = 80, 30
	m.resize()
	m.prov = errProvider{msg: "401 Unauthorized: bad key"}
	m.ta.SetValue("hi")
	updated, _ := m.Update(keyMsg("enter"))
	m = updated.(Model)
	found := false
	for _, e := range m.entries {
		if e.role == "error" && strings.Contains(e.content, "401") && strings.Contains(e.content, "/connect") {
			found = true
		}
	}
	if !found {
		t.Fatalf("hint missing: %+v", m.entries)
	}
	// Unknown errors still get a generic hint, not silence.
	if h := hintForProviderError("weird frobnicate"); !strings.Contains(h, "/doctor") {
		t.Fatalf("generic hint = %q", h)
	}
}

func TestPersistNoDirIsNoop(t *testing.T) {
	m := NewModel("/tmp") // no paths: must not crash
	m.persist()
	m = sendLine(t, m, "/sessions")
	found := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "no session dir") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected no-dir notice: %+v", m.entries)
	}
}

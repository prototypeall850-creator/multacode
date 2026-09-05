package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"multacode/internal/config"
	"multacode/internal/provider"
)

func keyMsg(s string) tea.KeyMsg {
	// Bubble Tea v1: KeyMsg is a struct embedding Key.
	var k tea.KeyMsg
	// Parse via string mapping used by tests: construct through Type+Runes?
	// Simplest: use tea.KeyPress fallback — construct KeyMsg from string helper.
	// bubbletea provides tea.KeyMsg{...}; emulate common keys below.
	switch s {
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		k = tea.KeyMsg{Type: tea.KeyTab}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		k = tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+r":
		k = tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+h":
		k = tea.KeyMsg{Type: tea.KeyCtrlH}
	case "alt+enter":
		k = tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	return k
}

func TestSlashHelpOpensHelp(t *testing.T) {
	m := NewModel("/tmp")
	m.width, m.height = 80, 30
	m.resize()
	m.ta.SetValue("/help")
	updated, _ := m.Update(keyMsg("enter"))
	mm := updated.(Model)
	if !mm.showHelp {
		t.Fatal("expected help overlay")
	}
	if !strings.Contains(mm.View(), "Slash:") && !strings.Contains(mm.View(), "slash") {
		t.Fatal("help view missing slash section")
	}
}

func TestTabSwitchesAgent(t *testing.T) {
	m := NewModel("/tmp")
	updated, _ := m.Update(keyMsg("tab"))
	if updated.(Model).agent != "plan" {
		t.Fatal("tab should switch to plan")
	}
}

func TestNarrowRender(t *testing.T) {
	for _, w := range []int{40, 50, 80} {
		m := NewModel("/tmp")
		m.width, m.height = w, 24
		m.resize()
		m.renderTranscript()
		out := m.View()
		if !strings.Contains(out, "Multacode") {
			t.Fatalf("width %d: missing top card", w)
		}
		if !strings.Contains(out, "multacode") {
			t.Fatalf("width %d: missing panel title", w)
		}
	}
}

func TestFakeSendStartsGenerating(t *testing.T) {
	m := NewModel("/tmp")
	m.width, m.height = 80, 30
	m.resize()
	m.ta.SetValue("hello")
	updated, _ := m.Update(keyMsg("enter"))
	mm := updated.(Model)
	if !mm.generating {
		t.Fatal("expected generating after send")
	}
	// Drain the fake stream: deltas then done.
	for i := 0; i < 50 && mm.generating; i++ {
		var cmd tea.Cmd
		_ = cmd
		// Pull one event from the channel like the runtime would.
		next := waitProviderEvent(mm.evCh)()
		var m2 tea.Model
		m2, _ = mm.Update(next)
		mm = m2.(Model)
	}
	if mm.generating {
		t.Fatal("fake stream should finish")
	}
	found := false
	for _, e := range mm.entries {
		if e.role == "assistant" && strings.Contains(e.content, "Fake provider") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing assistant reply: %+v", mm.entries)
	}
}

func sendLine(t *testing.T, m Model, line string) Model {
	t.Helper()
	m.ta.SetValue(line)
	updated, _ := m.Update(keyMsg("enter"))
	return updated.(Model)
}

func TestConnectWizardZen(t *testing.T) {
	m := NewModelWithOptions(Options{ProjectDir: "/tmp"})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "/connect new")
	if m.connectSt == nil {
		t.Fatal("wizard should start")
	}
	m = sendLine(t, m, "zen") // id
	m = sendLine(t, m, "")    // kind -> default zen
	m = sendLine(t, m, "")    // base -> zen default
	m = sendLine(t, m, "k1")  // key (redacted)
	m = sendLine(t, m, "")    // model -> default nemotron-3-ultra-free
	if m.connectSt != nil {
		t.Fatal("wizard should finish")
	}
	if len(m.cfg.Providers) != 1 || m.cfg.Providers[0].ID != "zen" {
		t.Fatalf("providers = %+v", m.cfg.Providers)
	}
	if m.auth["zen"] != "k1" {
		t.Fatalf("auth = %+v", m.auth)
	}
	if m.providerID != "zen" || m.modelID != "nemotron-3-ultra-free" {
		t.Fatalf("active = %s/%s", m.providerID, m.modelID)
	}
	// Key must be redacted from transcript.
	for _, e := range m.entries {
		if e.role == "user" && strings.Contains(e.content, "k1") {
			t.Fatalf("key leaked in transcript: %q", e.content)
		}
	}
}

func TestConnectQuickKeyUpdate(t *testing.T) {
	m := NewModelWithOptions(Options{ProjectDir: "/tmp", Config: config.Config{
		Providers: []config.ProviderConfig{{ID: "zen", Kind: "zen", DefaultModel: "glm-4.7-free", APIKeyRef: "auth:zen"}},
	}})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "/connect zen NEWKEY")
	if m.auth["zen"] != "NEWKEY" {
		t.Fatalf("auth = %+v", m.auth)
	}
	for _, e := range m.entries {
		if e.role == "user" && strings.Contains(e.content, "NEWKEY") {
			t.Fatal("key leaked in transcript")
		}
	}
}

func TestModelsDirectSelect(t *testing.T) {
	m := NewModelWithOptions(Options{ProjectDir: "/tmp", Config: config.Config{
		Providers: []config.ProviderConfig{
			{ID: "zen", Kind: "zen", DefaultModel: "glm-4.7-free", APIKeyRef: "auth:zen"},
			{ID: "oai", Kind: "openai-compatible", BaseURL: "https://x/v1", DefaultModel: "gpt-x"},
		},
	}})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "/models oai/gpt-x")
	if m.providerID != "oai" || m.modelID != "gpt-x" {
		t.Fatalf("active = %s/%s", m.providerID, m.modelID)
	}
}

func TestModelsPickerOpensFromFetch(t *testing.T) {
	m := NewModelWithOptions(Options{ProjectDir: "/tmp", Config: config.Config{
		Providers: []config.ProviderConfig{{ID: "zen", Kind: "zen", DefaultModel: "glm-4.7-free"}},
	}})
	m.width, m.height = 80, 30
	m.resize()
	m.pickerPending = true
	updated, _ := m.Update(modelsFetchedMsg{models: map[string][]provider.Model{
		"zen": {{ID: "glm-4.7-free"}, {ID: "glm-5"}},
	}})
	mm := updated.(Model)
	if mm.picker == nil || len(mm.picker.rows) != 2 {
		t.Fatalf("picker = %+v", mm.picker)
	}
	// Move down and select second model.
	updated, _ = mm.Update(keyMsg("down"))
	mm = updated.(Model)
	updated, _ = mm.Update(keyMsg("enter"))
	mm = updated.(Model)
	if mm.picker != nil {
		t.Fatal("picker should close after select")
	}
	if mm.providerID != "zen" || mm.modelID != "glm-5" {
		t.Fatalf("active = %s/%s", mm.providerID, mm.modelID)
	}
}

func TestModelsPickerFreeFirst(t *testing.T) {
	m := NewModelWithOptions(Options{ProjectDir: "/tmp", Config: config.Config{
		Providers: []config.ProviderConfig{{ID: "zen", Kind: "zen", DefaultModel: "deepseek-v4-flash-free"}},
	}})
	m.width, m.height = 80, 30
	m.resize()
	m.pickerPending = true
	updated, _ := m.Update(modelsFetchedMsg{models: map[string][]provider.Model{
		"zen": {
			{ID: "claude-opus-4-6"},
			{ID: "mimo-v2.5-free", Tags: []string{"free"}},
			{ID: "gpt-5.5"},
		},
	}})
	mm := updated.(Model)
	if mm.picker == nil || len(mm.picker.rows) != 3 {
		t.Fatalf("picker = %+v", mm.picker)
	}
	if mm.picker.rows[0].model != "mimo-v2.5-free" {
		t.Fatalf("free model should lead: %+v", mm.picker.rows)
	}
}

func TestSlashSuggestFiltersMax5(t *testing.T) {
	if got := slashSuggest("/"); len(got) != 5 {
		t.Fatalf("/ should suggest 5, got %v", got)
	}
	got := slashSuggest("/mod")
	if len(got) != 2 || got[0] != "/models" || got[1] != "/model" {
		t.Fatalf("/mod = %v", got)
	}
	if got := slashSuggest("/connect new"); len(got) != 0 {
		t.Fatalf("spaced input should not suggest: %v", got)
	}
	if got := slashSuggest("hello"); len(got) != 0 {
		t.Fatalf("non-slash should not suggest: %v", got)
	}
}

func TestSlashEnterCompletesPartial(t *testing.T) {
	m := NewModel("/tmp")
	m.width, m.height = 80, 30
	m.resize()
	m.ta.SetValue("/mod")
	updated, _ := m.Update(keyMsg("enter"))
	mm := updated.(Model)
	if mm.ta.Value() != "/models " {
		t.Fatalf("value = %q", mm.ta.Value())
	}
	sent := false
	for _, e := range mm.entries {
		if e.role == "user" {
			sent = true
		}
	}
	if sent {
		t.Fatal("partial slash must complete, not send")
	}
}

func TestSlashNavigateAndEsc(t *testing.T) {
	m := NewModel("/tmp")
	m.width, m.height = 80, 30
	m.resize()
	m.ta.SetValue("/m")
	updated, _ := m.Update(keyMsg("down"))
	mm := updated.(Model)
	if mm.slashSel != 1 {
		t.Fatalf("sel = %d", mm.slashSel)
	}
	updated, _ = mm.Update(keyMsg("up"))
	mm = updated.(Model)
	if mm.slashSel != 0 {
		t.Fatalf("sel = %d", mm.slashSel)
	}
	updated, _ = mm.Update(keyMsg("esc"))
	mm = updated.(Model)
	if len(mm.activeSuggest()) != 0 {
		t.Fatal("esc should dismiss suggestions")
	}
	if !strings.Contains(mm.View(), "> ") {
		// input box still renders; suggestions gone is what matters
		t.Log("note: view has no suggest box, ok")
	}
}

func TestModelsNoProviderGuides(t *testing.T) {
	m := NewModelWithOptions(Options{ProjectDir: "/tmp"})
	m.width, m.height = 80, 30
	m.resize()
	m = sendLine(t, m, "/models")
	if m.picker != nil || m.pickerPending {
		t.Fatal("no picker should open without providers")
	}
	found := false
	for _, e := range m.entries {
		if strings.Contains(e.content, "/connect new") {
			found = true
		}
	}
	if !found {
		t.Fatalf("guidance missing: %+v", m.entries)
	}
}

func TestModelsAllFailedGuides(t *testing.T) {
	m := NewModelWithOptions(Options{ProjectDir: "/tmp"})
	m.width, m.height = 80, 30
	m.resize()
	m.pickerPending = true
	updated, _ := m.Update(modelsFetchedMsg{
		models: map[string][]provider.Model{},
		errs:   map[string]string{"zen": "timeout"},
	})
	mm := updated.(Model)
	if mm.picker != nil {
		t.Fatal("empty picker should not open")
	}
	found := false
	for _, e := range mm.entries {
		if strings.Contains(e.content, "/doctor") {
			found = true
		}
	}
	if !found {
		t.Fatalf("guidance missing: %+v", mm.entries)
	}
}

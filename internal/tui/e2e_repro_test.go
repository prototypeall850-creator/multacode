package tui

// E2E reproduction: full user flow against a MOCK Zen-like server.
// wizard (/connect new) -> /models fetch -> select -> chat streams text.
import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"multacode/internal/config"
)

func mockZenServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/models", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"nemotron-3-ultra-free"},{"id":"mimo-v2.5-free"},{"id":"gpt-paid"}]}`)
	})
	mux.HandleFunc("/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"halo\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	return httptest.NewServer(mux)
}

func TestE2EWizardModelsChat(t *testing.T) {
	srv := mockZenServer()
	defer srv.Close()

	m := NewModelWithOptions(Options{ProjectDir: "/tmp"})
	m.width, m.height = 80, 30
	m.resize()

	// 1. Wizard: /connect new, pick zen, base=MOCK, key=none, model=enter.
	m = sendLine(t, m, "/connect new")
	for _, ans := range []string{"zen", srv.URL + "/chat/completions", "", ""} {
		m.ta.SetValue(ans)
		updated, _ := m.Update(keyMsg("enter"))
		m = updated.(Model)
	}
	if len(m.cfg.Providers) != 1 {
		t.Fatalf("providers = %+v", m.cfg.Providers)
	}
	pc := m.cfg.Providers[0]
	t.Logf("saved provider: %+v", pc)
	if m.providerID != "zen" {
		t.Fatalf("active provider = %q (want zen)", m.providerID)
	}
	t.Logf("active model = %q", m.modelID)

	// 2. /models fetch against the mock.
	m = sendLine(t, m, "/models")
	if !m.pickerPending {
		t.Fatalf("pickerPending=false; entries=%+v", m.entries)
	}
	// Run the fetch cmd synchronously.
	msg := m.fetchModelsCmd()()
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if m.picker == nil {
		t.Fatalf("no picker opened; entries=%+v", m.entries)
	}
	if len(m.picker.rows) != 3 {
		t.Fatalf("rows = %+v", m.picker.rows)
	}
	if !strings.Contains(m.picker.rows[0].model, "free") {
		t.Fatalf("free-first sort broken: %+v", m.picker.rows)
	}

	// 3. Select second row, check active switches.
	m.selectPickerRow(m.picker.rows[1])
	if m.providerID != "zen" || m.modelID != "mimo-v2.5-free" {
		t.Fatalf("active = %s/%s", m.providerID, m.modelID)
	}

	// 4. Chat: pump waitProviderEvent cmds like the runtime does.
	if cmd := m.startStream("halo"); cmd == nil {
		t.Fatalf("startStream nil; entries=%+v", m.entries)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for m.generating {
			msg := waitProviderEvent(m.evCh)()
			var cmd tea.Cmd
			var updated tea.Model
			updated, cmd = m.Update(msg)
			m = updated.(Model)
			_ = cmd
		}
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("generation hung; entries=%+v", m.entries)
	}
	found := false
	for _, e := range m.entries {
		if e.role == "assistant" && strings.Contains(e.content, "halo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no assistant answer; entries=%+v", m.entries)
	}
	_ = config.DefaultConfig
}

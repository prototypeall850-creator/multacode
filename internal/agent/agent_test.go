package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"multacode/internal/config"
	"multacode/internal/permission"
	"multacode/internal/provider"
	"multacode/internal/tools"
)

// scriptProvider replays queued event batches, one batch per Stream call.
type scriptProvider struct {
	scripts [][]provider.Event
	reqs    []provider.ChatRequest
}

func (s *scriptProvider) Name() string { return "script" }
func (s *scriptProvider) ListModels(ctx context.Context) ([]provider.Model, error) {
	return []provider.Model{{ID: "s"}}, nil
}
func (s *scriptProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.Event, error) {
	s.reqs = append(s.reqs, req)
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

func toolCall(id, name, input string) provider.Event {
	return provider.Event{Type: "tool_call", ToolCall: provider.ToolCall{
		ID: id, Name: name, Input: json.RawMessage(input),
	}}
}
func textEv(s string) provider.Event { return provider.Event{Type: "text_delta", TextDelta: s} }

func testProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SOUL.md"), []byte("Speak Indonesian casually."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "multa.md"), []byte("test command: go test ./..."), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRunInspectsProjectThenAnswers(t *testing.T) {
	root := testProject(t)
	sp := &scriptProvider{scripts: [][]provider.Event{
		{textEv("looking"), toolCall("1", "list_files", `{}`)},
		{textEv("project has main.go")},
	}}
	a := &Agent{Provider: sp, Tools: tools.DefaultRegistry(root)}
	req := Request{
		Mode: ModeBuild, Model: "s",
		Messages:   []Message{{Role: "user", Content: "summarize this project"}},
		ProjectDir: root,
		Ctx:        LoadContext(root, config.Paths{}, "build"),
		Policy:     permission.DefaultPolicy(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var events []TurnEvent
	allowAll := func(string, json.RawMessage) bool { return true }
	if err := a.Run(ctx, req, allowAll, func(e TurnEvent) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}
	var sawStart, sawResult, sawDone, sawFinal bool
	for _, e := range events {
		switch e.Type {
		case "tool_start":
			if e.Tool == "list_files" {
				sawStart = true
			}
		case "tool_result":
			if strings.Contains(e.Result, "main.go") {
				sawResult = true
			}
		case "text":
			if strings.Contains(e.Text, "main.go") {
				sawFinal = true
			}
		case "done":
			sawDone = true
		}
	}
	if !sawStart || !sawResult || !sawDone || !sawFinal {
		t.Fatalf("events = %+v", events)
	}
	if len(sp.reqs) != 2 {
		t.Fatalf("expected 2 provider turns, got %d", len(sp.reqs))
	}
	// Tool defs offered and observation folded back.
	if len(sp.reqs[0].Tools) == 0 {
		t.Fatal("first turn must include tool defs")
	}
	if !strings.Contains(sp.reqs[1].Messages[len(sp.reqs[1].Messages)-1].Content, "main.go") {
		t.Fatalf("observation not folded: %+v", sp.reqs[1].Messages)
	}
}

func TestPlanModeDeniesEdit(t *testing.T) {
	sp := &scriptProvider{scripts: [][]provider.Event{
		{toolCall("1", "edit_file", `{"path":"a"}`)},
		{textEv("cannot edit in plan mode")},
	}}
	a := &Agent{Provider: sp, Tools: tools.DefaultRegistry(t.TempDir())}
	req := Request{
		Mode: ModePlan, Model: "s",
		Messages: []Message{{Role: "user", Content: "change it"}},
		Policy:   permission.PolicyForAgent("plan"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	decideCalled := false
	decide := func(string, json.RawMessage) bool { decideCalled = true; return true }
	var events []TurnEvent
	if err := a.Run(ctx, req, decide, func(e TurnEvent) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}
	if decideCalled {
		t.Fatal("plan+deny must not reach the decider")
	}
	found := false
	for _, e := range events {
		if e.Type == "tool_result" && strings.Contains(e.Result, "blocked") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected blocked observation: %+v", events)
	}
}

func TestSystemPromptStack(t *testing.T) {
	root := testProject(t)
	c := LoadContext(root, config.Paths{}, "build")
	if len(c.Soul) != 1 || !strings.Contains(c.Soul[0].Content, "Indonesian") {
		t.Fatalf("soul = %+v", c.Soul)
	}
	if !strings.Contains(c.ProjectMemory, "go test") {
		t.Fatalf("memory = %q", c.ProjectMemory)
	}
	p := SystemPrompt(ModeBuild, c)
	for _, want := range []string{"MultaCode", "SOUL.md", "multa.md", "environment", "Termux", "pkg install"} {
		// Termux hints only when running under Termux; require the rest.
		if want == "Termux" || want == "pkg install" {
			continue
		}
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
	plan := SystemPrompt(ModePlan, c)
	if !strings.Contains(plan, "PLAN MODE") || strings.Contains(plan, "BUILD MODE") {
		t.Fatalf("plan prompt wrong:\n%s", plan)
	}
}

func TestWebToolsExposedAndCited(t *testing.T) {
	a := &Agent{Tools: tools.DefaultRegistry(t.TempDir())}
	var names []string
	for _, d := range a.ToolDefs() {
		names = append(names, d.Name)
	}
	for _, want := range []string{"web_search", "web_fetch"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("tool defs missing %s: %v", want, names)
		}
	}
	p := SystemPrompt(ModeBuild, PromptContext{ProjectDir: "/tmp"})
	if !strings.Contains(p, "cite") {
		t.Fatalf("prompt must require citations:\n%s", p)
	}
	if got := DecideCall(permission.DefaultPolicy(), "web_fetch", `{}`); got != permission.Allow {
		t.Fatalf("web_fetch = %v", got)
	}
}

func TestBuildAgentApprovedEditApplies(t *testing.T) {
	root := testProject(t)
	target := filepath.Join(root, "note.txt")
	if err := os.WriteFile(target, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp := &scriptProvider{scripts: [][]provider.Event{
		{toolCall("1", "edit_file", `{"path":"note.txt","old":"world","new":"mars"}`)},
		{textEv("edited")},
	}}
	a := &Agent{Provider: sp, Tools: tools.DefaultRegistry(root)}
	req := Request{
		Mode: ModeBuild, Model: "s",
		Messages:   []Message{{Role: "user", Content: "change world to mars"}},
		ProjectDir: root,
		Policy:     permission.PolicyForAgent("build"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	approve := func(string, json.RawMessage) bool { return true }
	var events []TurnEvent
	if err := a.Run(ctx, req, approve, func(e TurnEvent) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "hello mars\n" {
		t.Fatalf("content = %q", data)
	}
	found := false
	for _, e := range events {
		if e.Type == "tool_result" && strings.Contains(e.Result, "+hello mars") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diff not recorded: %+v", events)
	}
}
func TestDecideCallShellRisk(t *testing.T) {
	pol := permission.DefaultPolicy()
	if got := DecideCall(pol, "run_shell", `{"command":"ls -la"}`); got != permission.Ask {
		t.Fatalf("ls = %v", got)
	}
	if got := DecideCall(pol, "run_shell", `{"command":"rm -rf /"}`); got != permission.Deny {
		t.Fatalf("rm-rf = %v", got)
	}
	if got := DecideCall(pol, "search_files", `{}`); got != permission.Allow {
		t.Fatalf("search = %v", got)
	}
	if got := DecideCall(pol, "nope", `{}`); got != permission.Deny {
		t.Fatalf("unknown = %v", got)
	}
}

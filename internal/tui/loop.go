// Tool loop glue for the TUI: @file expansion, !commands,
// permission-gated tool execution, approval modal, and
// read-only /soul /memory /permissions views (Milestone 3).
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"multacode/internal/agent"
	"multacode/internal/permission"
	"multacode/internal/provider"
	"multacode/internal/tools"
)

// pendingApproval pauses the ReAct loop until the user decides.
type pendingApproval struct {
	tool   string
	input  string
	queued []providerToolCallMsg // remaining calls for this turn
}

type bangResultMsg struct {
	command  string
	output   string
	exitCode int
	trunc    bool
	errStr   string
}

// webResultMsg carries a finished /search or /fetch tool run.
type webResultMsg struct {
	tool   string
	input  string
	output string
	trunc  bool
	errStr string
}

// SourceRef tracks one web source used for the current session.
type SourceRef struct {
	Title string
	URL   string
}

var atFileRe = regexp.MustCompile(`@([^\s` + "`" + `]+)`)

// effectivePolicy merges agent defaults with user config overrides.
func (m *Model) effectivePolicy() permission.Policy {
	p := permission.PolicyForAgent(m.agent)
	ov := func(cur permission.Decision, raw string) permission.Decision {
		switch permission.Decision(strings.ToLower(strings.TrimSpace(raw))) {
		case permission.Allow, permission.Ask, permission.Deny:
			return permission.Decision(strings.ToLower(strings.TrimSpace(raw)))
		default:
			return cur
		}
	}
	return permission.Policy{
		Read:   ov(p.Read, m.cfg.Permission.Read),
		Search: ov(p.Search, m.cfg.Permission.Search),
		Edit:   ov(p.Edit, m.cfg.Permission.Edit),
		Shell:  ov(p.Shell, m.cfg.Permission.Shell),
		Delete: ov(p.Delete, m.cfg.Permission.Delete),
	}
}

func (m *Model) toolDefs() []provider.ToolDef {
	return (&agent.Agent{Tools: m.registry}).ToolDefs()
}

// expandAtFiles resolves @path tokens against the project dir.
// Returns the provider-ready text plus transcript notes.
func expandAtFiles(text, projectDir string) (string, []string) {
	var notes []string
	var ctxBlocks []string
	seen := map[string]bool{}
	expanded := atFileRe.ReplaceAllStringFunc(text, func(tok string) string {
		raw := strings.Trim(tok[1:], ".,:;!?")
		if raw == "" || seen[raw] {
			return tok
		}
		seen[raw] = true
		abs := filepath.Join(projectDir, filepath.Clean(raw))
		info, err := os.Stat(abs)
		if err != nil {
			notes = append(notes, "📎 @"+raw+" (missing)")
			ctxBlocks = append(ctxBlocks, "<context src=\"@"+raw+"\">(file not found)</context>")
			return tok
		}
		if info.IsDir() {
			ents, _ := os.ReadDir(abs)
			var names []string
			for _, e := range ents {
				names = append(names, e.Name())
				if len(names) >= 30 {
					break
				}
			}
			sort.Strings(names)
			notes = append(notes, fmt.Sprintf("📎 @%s (dir, %d entries)", raw, len(names)))
			ctxBlocks = append(ctxBlocks, "<context src=\"@"+raw+"\">\n"+strings.Join(names, "\n")+"\n</context>")
			return tok
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			notes = append(notes, "📎 @"+raw+" (unreadable)")
			ctxBlocks = append(ctxBlocks, "<context src=\"@"+raw+"\">(unreadable: "+err.Error()+")</context>")
			return tok
		}
		if len(data) > 16*1024 {
			data = data[:16*1024]
		}
		body := tools.RedactSecrets(string(data))
		lines := strings.Count(body, "\n") + 1
		notes = append(notes, fmt.Sprintf("📎 @%s (%d lines)", raw, lines))
		ctxBlocks = append(ctxBlocks, "<context src=\"@"+raw+"\">\n"+body+"\n</context>")
		return tok
	})
	if len(ctxBlocks) > 0 {
		expanded += "\n\n" + strings.Join(ctxBlocks, "\n")
	}
	return expanded, notes
}

// runBangCmd executes a user-typed !command (explicit user intent = one-shot approval,
// but policy-deny patterns are still blocked).
func (m *Model) runBangCmd(cmd string) tea.Cmd {
	policy := m.effectivePolicy()
	if permission.ClassifyShell(cmd, policy.Shell) == permission.Deny {
		m.entries = append(m.entries, entry{role: "error", content: "blocked by policy: " + cmd})
		return nil
	}
	root := m.projectDir
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := (&tools.RunShell{Root: root}).Run(ctx, json.RawMessage(`{"command":`+quoteJSON(cmd)+`}`))
		msg := bangResultMsg{command: cmd, output: res.Output, exitCode: res.ExitCode, trunc: res.Truncated}
		if err != nil {
			msg.errStr = err.Error()
		}
		return msg
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// processToolCalls drains one turn's tool calls through the permission
// policy. Returns a command that continues the loop, or nil when the
// loop pauses on an approval modal.
func (m *Model) processToolCalls() tea.Cmd {
	policy := m.effectivePolicy()
	for len(m.toolQueue) > 0 {
		tc := m.toolQueue[0]
		m.toolQueue = m.toolQueue[1:]
		verdict := agent.DecideCall(policy, tc.Name, tc.Input)
		switch verdict {
		case permission.Deny:
			o := fmt.Sprintf("[tool:%s blocked] denied by %s-agent policy", tc.Name, m.agent)
			if tc.Name == "edit_file" && m.agent == "plan" {
				o = "[tool:edit_file blocked] plan agent is read-only and cannot edit; press tab to switch to build"
			}
			m.entries = append(m.entries, entry{role: "tool", content: o})
			m.loopMsgs = append(m.loopMsgs, provider.Message{Role: "user", Content: o})
		case permission.Ask:
			m.approval = &pendingApproval{tool: tc.Name, input: tc.Input, queued: append([]providerToolCallMsg(nil), m.toolQueue...)}
			m.toolQueue = nil
			return nil
		default: // allow
			m.execToolCall(tc.Name, tc.Input)
		}
	}
	return m.continueLoop()
}

func (m *Model) execToolCall(name, input string) {
	tool, ok := m.registry[name]
	if !ok {
		o := fmt.Sprintf("[tool:%s unknown] no such tool", name)
		m.entries = append(m.entries, entry{role: "tool", content: o})
		m.loopMsgs = append(m.loopMsgs, provider.Message{Role: "user", Content: o})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var raw json.RawMessage = json.RawMessage(input)
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	res, err := tool.Run(ctx, raw)
	o := agent.ToolObservation(name, res, err)
	if err == nil && (name == "web_search" || name == "web_fetch") {
		m.trackSources(res.Output)
	}
	m.entries = append(m.entries, entry{role: "tool", content: previewTool(o)})
	m.loopMsgs = append(m.loopMsgs, provider.Message{Role: "user", Content: o})
}

// trackSources extracts "URL: ..." lines from web tool output for /sources.
func (m *Model) trackSources(output string) {
	title := ""
	for _, l := range strings.Split(output, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "Title: ") && title == "" {
			title = strings.TrimPrefix(t, "Title: ")
		}
		if strings.HasPrefix(t, "URL: ") {
			u := strings.TrimSpace(strings.TrimPrefix(t, "URL: "))
			if u != "" {
				m.addSource(title, u)
			}
		}
	}
}

func (m *Model) addSource(title, rawURL string) {
	for _, s := range m.sources {
		if s.URL == rawURL {
			return
		}
	}
	if title == "" {
		title = hostOfToolURL(rawURL)
	}
	m.sources = append(m.sources, SourceRef{Title: title, URL: rawURL})
	if len(m.sources) > 30 {
		m.sources = m.sources[len(m.sources)-30:]
	}
}

func hostOfToolURL(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
	return raw
}

func (m *Model) sourcesReport() string {
	if len(m.sources) == 0 {
		return "sources: none yet — use /search or /fetch first."
	}
	var sb strings.Builder
	sb.WriteString("sources:\n")
	for i, s := range m.sources {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n", i+1, s.Title, s.URL)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// runWebToolCmd runs web_search/web_fetch for /search and /fetch.
func (m *Model) runWebToolCmd(toolName, input string) tea.Cmd {
	tool, ok := m.registry[toolName]
	if !ok {
		m.entries = append(m.entries, entry{role: "error", content: toolName + ": no such tool"})
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := tool.Run(ctx, json.RawMessage(input))
		msg := webResultMsg{tool: toolName, input: input, output: res.Output, trunc: res.Truncated}
		if err != nil {
			msg.errStr = err.Error()
		}
		return msg
	}
}

func previewTool(s string) string {
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}

// continueLoop re-streams with folded tool observations, bounded by MaxSteps.
func (m *Model) continueLoop() tea.Cmd {
	m.loopSteps++
	if m.loopSteps >= agent.DefaultMaxStepsFor(0) {
		m.entries = append(m.entries, entry{role: "error", content: "step limit reached; rephrase or /compact and retry"})
		m.finishGeneration()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelGen = cancel
	ch, err := m.prov.Stream(ctx, provider.ChatRequest{
		Model:    m.modelID,
		Messages: m.loopMsgs,
		Tools:    m.toolDefs(),
	})
	if err != nil {
		m.entries = append(m.entries, entry{role: "error",
			content: "provider: " + err.Error() + " (" + hintForProviderError(err.Error()) + ")"})
		m.finishGeneration()
		return nil
	}
	m.evCh = ch
	m.assistantBuf = ""
	m.generating = true
	return tea.Batch(m.sp.Tick, waitProviderEvent(ch))
}

func (m *Model) resolveApproval(approve bool) tea.Cmd {
	ap := m.approval
	m.approval = nil
	if ap == nil {
		return nil
	}
	if approve {
		m.entries = append(m.entries, entry{role: "system", content: "approved: " + ap.tool})
		m.execToolCall(ap.tool, ap.input)
	} else {
		o := fmt.Sprintf("[tool:%s denied] user declined approval", ap.tool)
		m.entries = append(m.entries, entry{role: "tool", content: o})
		m.loopMsgs = append(m.loopMsgs, provider.Message{Role: "user", Content: o})
	}
	m.toolQueue = append(ap.queued, m.toolQueue...)
	return m.processToolCalls()
}

func (m *Model) renderApproval(width int) string {
	if m.approval == nil {
		return ""
	}
	body := fmt.Sprintf("approval — %s agent\n\ntool:  %s\n", m.agent, m.approval.tool)
	if m.approval.tool == "edit_file" {
		rel, diff, err := tools.PreviewEdit(m.projectDir, json.RawMessage(m.approval.input))
		if err != nil {
			body += fmt.Sprintf("file:  (cannot preview: %s)\n", err.Error())
		} else {
			if len(diff) > 1500 {
				diff = diff[:1500] + "\n…(diff truncated)"
			}
			body += fmt.Sprintf("file:  %s\n\n%s\n", rel, diff)
		}
	} else {
		preview := m.approval.input
		if len(preview) > 500 {
			preview = preview[:500] + "…"
		}
		body += fmt.Sprintf("input: %s\n", preview)
	}
	body += "\n[y]/enter approve once   [n]/esc deny"
	return panelStyle.Width(width - 4).Render(body)
}

// --- read-only views ---

func (m *Model) soulReport() string {
	var sb strings.Builder
	sb.WriteString("SOUL layers:\n")
	active := map[string]bool{}
	for _, s := range m.agentCtx.Soul {
		active[s.Path] = true
		first := strings.SplitN(s.Content, "\n", 2)[0]
		fmt.Fprintf(&sb, "- active [%s] %s (%d bytes): %s\n", s.Source, s.Path, len(s.Content), first)
	}
	for _, want := range []struct{ src, path string }{
		{"project", filepath.Join(m.projectDir, "SOUL.md")},
		{"global", filepath.Join(m.paths.ConfigDir, "SOUL.md")},
	} {
		if want.path == "" || want.path == "SOUL.md" || active[want.path] {
			continue
		}
		fmt.Fprintf(&sb, "- missing [%s] %s\n", want.src, want.path)
	}
	sb.WriteString("SOUL shapes tone and defaults; it never overrides safety or approvals.")
	return strings.TrimRight(sb.String(), "\n")
}

func (m *Model) memoryReport() string {
	var sb strings.Builder
	sb.WriteString("memory (read-only; updates always ask first, never store secrets):\n")
	if m.agentCtx.ProjectMemory != "" {
		fmt.Fprintf(&sb, "- project multa.md (%d bytes):\n  %s\n",
			len(m.agentCtx.ProjectMemory), firstLines(m.agentCtx.ProjectMemory, 6))
	} else {
		fmt.Fprintf(&sb, "- project %s (missing)\n", filepath.Join(m.projectDir, "multa.md"))
	}
	if m.paths.ConfigDir != "" {
		if m.agentCtx.UserMemory != "" {
			fmt.Fprintf(&sb, "- user %s (%d bytes):\n  %s",
				filepath.Join(m.paths.ConfigDir, "multa.md"),
				len(m.agentCtx.UserMemory), firstLines(m.agentCtx.UserMemory, 6))
		} else {
			fmt.Fprintf(&sb, "- user %s (missing)", filepath.Join(m.paths.ConfigDir, "multa.md"))
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "…")
	}
	return strings.Join(lines, "\n  ")
}

func (m *Model) permissionsReport() string {
	p := m.effectivePolicy()
	var sb strings.Builder
	fmt.Fprintf(&sb, "permissions — agent:%s (config overrides agent defaults):\n", m.agent)
	fmt.Fprintf(&sb, "read=%s search=%s edit=%s shell=%s delete=%s\n",
		p.Read, p.Search, p.Edit, p.Shell, p.Delete)
	if m.agent == "plan" {
		sb.WriteString("plan agent: read/search/shell-reads allowed; edits denied.")
	} else {
		sb.WriteString("build agent: edits ask first; destructive shell denied outright.")
	}
	return strings.TrimRight(sb.String(), "\n")
}

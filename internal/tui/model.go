// Package tui is the Bubble Tea interface per plan.md.
// Milestone 2: real provider streaming, /connect provider setup,
// /models picker. Layout mirrors the counter-app prototype.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"multacode/internal/agent"
	"multacode/internal/config"
	"multacode/internal/provider"
	"multacode/internal/session"
	"multacode/internal/tools"
)

type entry struct {
	role    string // user | assistant | tool | system | error
	content string
}

// Streaming messages.
type streamDelta string
type streamDone struct{}
type providerDone struct{}
type streamErrorMsg string

type providerToolCallMsg struct {
	ID    string
	Name  string
	Input string
}

// Options wires config/auth into the TUI (Milestone 2).
type Options struct {
	ProjectDir string
	Paths      config.Paths
	Config     config.Config
	Auth       config.Auth
	Notice     string
}

type Model struct {
	projectDir string
	paths      config.Paths
	cfg        config.Config
	auth       config.Auth

	prov       provider.Provider
	providerID string
	modelID    string
	agent      string

	ta       textarea.Model
	vp       viewport.Model
	sp       spinner.Model
	width    int
	height   int
	entries  []entry
	showHelp bool
	showTool bool

	generating   bool
	assistantBuf string
	pendingOut   bool
	cancelGen    context.CancelFunc
	evCh         <-chan provider.Event
	lastCtrlC    time.Time

	// Milestone 3: ReAct loop state.
	registry  tools.Registry
	agentCtx  agent.PromptContext
	loopMsgs  []provider.Message
	loopSteps int
	toolQueue []providerToolCallMsg
	approval  *pendingApproval
	sources   []SourceRef

	modelsCache   map[string][]provider.Model
	picker        *modelsPicker
	pickerPending bool
	connectSt     *connectState

	// Milestone 6: session persistence.
	sessionID  string
	sessPicker *sessionPicker

	// Slash autocomplete state.
	slashSel int
	slashOff bool // esc-dismissed until input changes
}

func NewModel(projectDir string) Model {
	return NewModelWithOptions(Options{ProjectDir: projectDir})
}

func NewModelWithOptions(opts Options) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message, @file, !cmd, or /help"
	ta.Prompt = "> "
	ta.CharLimit = 0
	ta.SetHeight(1)
	ta.ShowLineNumbers = false
	ta.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(cyan)

	vp := viewport.New(80, 12)

	m := Model{
		projectDir:  opts.ProjectDir,
		paths:       opts.Paths,
		cfg:         opts.Config,
		auth:        opts.Auth,
		agent:       "build",
		ta:          ta,
		vp:          vp,
		sp:          sp,
		showTool:    true,
		sessionID:   session.NewID(),
		modelsCache: map[string][]provider.Model{},
		entries: []entry{
			{role: "system", content: "Welcome to multacode. Type /help. Tab switches build/plan."},
		},
	}
	if opts.Notice != "" {
		m.entries = append(m.entries, entry{role: "system", content: opts.Notice})
	}
	m.registry = tools.DefaultRegistry(m.projectDir)
	m.registry["web_search"] = tools.NewWebSearch(
		m.cfg.Search.Provider,
		provider.ResolveAPIKey(m.cfg.Search.APIKeyRef, map[string]string(m.auth)),
		m.cfg.Search.BaseURL,
	)
	m.agentCtx = agent.LoadContext(m.projectDir, m.paths, m.agent)
	m.rebuildProvider()
	return m
}

func Run(projectDir string) error {
	return RunWithOptions(Options{ProjectDir: projectDir})
}

func RunWithOptions(opts Options) error {
	p := tea.NewProgram(NewModelWithOptions(opts), tea.WithAltScreen())
	_, err := p.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return textarea.Blink
}

// --- provider wiring ---

func (m *Model) rebuildProvider() {
	id := m.providerID
	if id == "" {
		id = m.cfg.DefaultProvider
	}
	if id == "" && len(m.cfg.Providers) > 0 {
		id = m.cfg.Providers[0].ID
	}
	if id == "" {
		m.prov = provider.Fake{}
		m.providerID = ""
		m.modelID = "fake-coding"
		return
	}
	pc := findProvider(m.cfg.Providers, id)
	if pc == nil {
		m.prov = provider.Fake{}
		m.providerID = ""
		m.modelID = "fake-coding"
		return
	}
	authMap := map[string]string(m.auth)
	p, err := provider.BuildProvider(*pc, authMap)
	if err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "provider " + id + ": " + err.Error()})
		m.prov = provider.Fake{}
		m.providerID = ""
		m.modelID = "fake-coding"
		return
	}
	m.prov = p
	m.providerID = id
	m.modelID = m.cfg.DefaultModel
	if m.modelID == "" {
		m.modelID = pc.DefaultModel
	}
	if m.modelID == "" {
		m.modelID = "default"
	}
}

func (m *Model) saveAll() error {
	if m.paths.ConfigFile != "" {
		if err := config.Save(m.paths.ConfigFile, m.cfg); err != nil {
			return err
		}
	}
	if m.paths.AuthFile != "" {
		if err := config.SaveAuth(m.paths.AuthFile, m.auth); err != nil {
			return err
		}
	}
	return nil
}

func (m *Model) displayModel() string {
	if m.providerID != "" {
		return m.providerID + "/" + m.modelID
	}
	return m.modelID
}

// history returns the last n user/assistant turns as provider messages.
// Tool observations ride along as user-role context so every provider
// shape (chat, responses, Anthropic) accepts them.
func (m *Model) history(n int) []provider.Message {
	var out []provider.Message
	for i := len(m.entries) - 1; i >= 0 && len(out) < n; i-- {
		e := m.entries[i]
		switch e.role {
		case "user":
			out = append([]provider.Message{{Role: "user", Content: e.content}}, out...)
		case "assistant":
			out = append([]provider.Message{{Role: "assistant", Content: e.content}}, out...)
		case "tool":
			out = append([]provider.Message{{Role: "user", Content: e.content}}, out...)
		}
	}
	return out
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.renderTranscript()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(msg)
		if m.generating {
			return m, cmd
		}
		return m, nil

	case streamDelta:
		m.assistantBuf += string(msg)
		m.pendingOut = true
		m.renderTranscript()
		return m, waitProviderEvent(m.evCh)

	case providerToolCallMsg:
		m.flushAssistantBuf()
		preview := msg.Input
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		m.entries = append(m.entries, entry{role: "tool", content: "◈ " + msg.Name + " " + preview})
		m.toolQueue = append(m.toolQueue, msg)
		m.pendingOut = true
		m.renderTranscript()
		return m, waitProviderEvent(m.evCh)

	case providerDone:
		return m, waitProviderEvent(m.evCh)

	case streamErrorMsg:
		m.flushAssistantBuf()
		m.entries = append(m.entries, entry{role: "error", content: string(msg)})
		m.finishGeneration()
		m.persist()
		m.renderTranscript()
		return m, nil

	case streamDone:
		m.flushAssistantBuf()
		if len(m.toolQueue) > 0 || m.approval != nil {
			cmd := m.processToolCalls()
			m.renderTranscript()
			return m, cmd
		}
		if !m.pendingOut {
			m.entries = append(m.entries, entry{role: "system", content: "(empty response — provider sent no text or tool call; retry, or /models to switch model)"})
		}
		m.finishGeneration()
		m.persist()
		m.renderTranscript()
		return m, nil

	case modelsFetchedMsg:
		// Store fetched models in cache.
		for id, models := range msg.models {
			m.modelsCache[id] = models
		}
		// Report any fetch errors to user.
		for id, errStr := range msg.errs {
			if _, ok := msg.models[id]; !ok {
				m.entries = append(m.entries, entry{role: "error", content: "models " + id + ": " + errStr})
			}
		}
		// If /models was called without args, build and open the picker.
		if m.pickerPending {
			m.pickerPending = false
			rows := m.buildPickerRows()
			if len(rows) == 0 {
				m.entries = append(m.entries, entry{role: "system", content: "No models listed. Check errors above, run `/doctor` for connectivity, or `/connect` to re-add the provider."})
			} else {
				// Initialize picker with proper state: cursor at 0, offset at 0.
				m.picker = &modelsPicker{rows: rows, cursor: 0, offset: 0}
				m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("Models picker opened. Use ↑↓/jk to navigate, enter to select, esc to close. (%d models)", len(rows))})
			}
		}
		m.renderTranscript()
		return m, nil

	case bangResultMsg:
		if msg.errStr != "" {
			m.entries = append(m.entries, entry{role: "error", content: "!" + msg.command + ": " + msg.errStr})
		} else {
			body := msg.output
			if msg.trunc {
				body += "\n(truncated)"
			}
			m.entries = append(m.entries, entry{role: "tool", content: fmt.Sprintf("!%s (exit=%d)\n%s", msg.command, msg.exitCode, body)})
		}
		m.persist()
		m.renderTranscript()
		return m, nil

	case webResultMsg:
		if msg.errStr != "" {
			m.entries = append(m.entries, entry{role: "error", content: msg.tool + ": " + msg.errStr})
		} else {
			body := msg.output
			if msg.trunc {
				body += "\n(truncated)"
			}
			m.entries = append(m.entries, entry{role: "tool", content: body})
			m.trackSources(msg.output)
		}
		m.persist()
		m.renderTranscript()
		return m, nil

	case doctorMsg:
		m.agentCtx.Env = msg.profile
		m.entries = append(m.entries, entry{role: "system", content: msg.report})
		m.persist()
		m.renderTranscript()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m *Model) flushAssistantBuf() {
	if text := strings.TrimSpace(m.assistantBuf); text != "" {
		m.entries = append(m.entries, entry{role: "assistant", content: text})
		m.assistantBuf = ""
	}
}

func (m *Model) finishGeneration() {
	m.generating = false
	m.assistantBuf = ""
	m.pendingOut = false
	m.evCh = nil
	m.toolQueue = nil
	m.loopMsgs = nil
	m.loopSteps = 0
	if m.cancelGen != nil {
		m.cancelGen()
		m.cancelGen = nil
	}
}

func (m *Model) cancelGeneration() {
	m.finishGeneration()
}

func waitProviderEvent(ch <-chan provider.Event) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return streamDone{}
		}
		ev, ok := <-ch
		if !ok {
			return streamDone{}
		}
		switch ev.Type {
		case "text_delta":
			return streamDelta(ev.TextDelta)
		case "tool_call":
			return providerToolCallMsg{ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Input: string(ev.ToolCall.Input)}
		case "error":
			if ev.Err != nil {
				return streamErrorMsg("stream error: " + ev.Err.Error() + " (" + hintForProviderError(ev.Err.Error()) + ")")
			}
			return streamErrorMsg("stream error (" + hintForProviderError("unknown") + ")")
		default: // done, usage-only, unknown
			return providerDone{}
		}
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showHelp {
		if key == "esc" || key == "ctrl+h" || key == "q" {
			m.showHelp = false
			m.renderTranscript()
			return m, nil
		}
		if key == "ctrl+c" {
			m.persist()
			return m, tea.Quit
		}
		return m, nil
	}

	// Approval modal open: y/enter approves once, n/esc denies.
	if m.approval != nil {
		switch key {
		case "ctrl+c":
			m.approval = nil
			m.finishGeneration()
			m.entries = append(m.entries, entry{role: "system", content: "Turn cancelled."})
			m.renderTranscript()
			return m, nil
		case "enter", "y", "Y":
			cmd := m.resolveApproval(true)
			m.renderTranscript()
			return m, cmd
		case "esc", "n", "N":
			cmd := m.resolveApproval(false)
			m.renderTranscript()
			return m, cmd
		}
		return m, nil
	}

	// Sessions picker open: navigate/resume/delete/close.
	if m.sessPicker != nil {
		switch key {
		case "esc":
			m.sessPicker = nil
			return m, nil
		case "up", "ctrl+p":
			if m.sessPicker.cursor > 0 {
				m.sessPicker.cursor--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.sessPicker.cursor < len(m.sessPicker.rows)-1 {
				m.sessPicker.cursor++
			}
			return m, nil
		case "enter":
			if len(m.sessPicker.rows) == 0 {
				m.sessPicker = nil
				return m, nil
			}
			m.resumeSession(m.sessPicker.rows[m.sessPicker.cursor].id)
			m.renderTranscript()
			return m, nil
		case "ctrl+c":
			m.persist()
			return m, tea.Quit
		}
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'j':
				if m.sessPicker.cursor < len(m.sessPicker.rows)-1 {
					m.sessPicker.cursor++
				}
				return m, nil
			case 'k':
				if m.sessPicker.cursor > 0 {
					m.sessPicker.cursor--
				}
				return m, nil
			case 'd':
				m.deletePickedSession()
				m.renderTranscript()
				return m, nil
			}
		}
		return m, nil
	}

	// Models picker open: navigate/select/close.
	if m.picker != nil {
		switch key {
		case "esc":
			m.picker = nil
			return m, nil
		case "up", "ctrl+p":
			if m.picker.cursor > 0 {
				m.picker.cursor--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.picker.cursor < len(m.picker.rows)-1 {
				m.picker.cursor++
			}
			return m, nil
		case "enter":
			if len(m.picker.rows) == 0 {
				m.picker = nil
				return m, nil
			}
			m.selectPickerRow(m.picker.rows[m.picker.cursor])
			m.renderTranscript()
			return m, nil
		case "ctrl+c":
			m.persist()
			return m, tea.Quit
		}
		// j/k rune navigation.
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'j':
				if m.picker.cursor < len(m.picker.rows)-1 {
					m.picker.cursor++
				}
				return m, nil
			case 'k':
				if m.picker.cursor > 0 {
					m.picker.cursor--
				}
				return m, nil
			}
		}
		return m, nil
	}

	// Slash autocomplete: navigate/complete while typing "/...".
	if sug := m.activeSuggest(); len(sug) > 0 {
		if m.slashSel < 0 || m.slashSel >= len(sug) {
			m.slashSel = 0
		}
		switch key {
		case "up", "ctrl+p":
			m.slashSel = (m.slashSel - 1 + len(sug)) % len(sug)
			return m, nil
		case "down", "ctrl+n":
			m.slashSel = (m.slashSel + 1) % len(sug)
			return m, nil
		case "tab":
			m.completeSuggest(sug)
			return m, nil
		case "enter":
			// Exact command runs; partial input completes the selection.
			if isExactSlashCommand(strings.TrimSpace(m.ta.Value())) {
				break
			}
			m.completeSuggest(sug)
			return m, nil
		case "esc":
			m.slashOff = true
			return m, nil
		}
	}

	switch key {
	case "ctrl+c":
		if m.generating {
			m.cancelGeneration()
			m.entries = append(m.entries, entry{role: "system", content: "Generation cancelled. (ctrl+c again to quit)"})
			m.lastCtrlC = time.Now()
			m.renderTranscript()
			return m, nil
		}
		if time.Since(m.lastCtrlC) < 2*time.Second {
			m.persist()
			return m, tea.Quit
		}
		m.lastCtrlC = time.Now()
		m.entries = append(m.entries, entry{role: "system", content: "Press ctrl+c again to quit."})
		m.renderTranscript()
		return m, nil

	case "esc":
		if m.connectSt != nil {
			m.connectSt = nil
			m.entries = append(m.entries, entry{role: "system", content: "Connect cancelled."})
			m.renderTranscript()
		}
		return m, nil

	case "tab":
		if m.agent == "build" {
			m.agent = "plan"
		} else {
			m.agent = "build"
		}
		m.agentCtx.Agent = m.agent
		m.entries = append(m.entries, entry{role: "system", content: "Agent: " + m.agent})
		m.renderTranscript()
		return m, nil

	case "ctrl+r":
		m.showTool = !m.showTool
		m.renderTranscript()
		return m, nil

	case "ctrl+h":
		m.showHelp = true
		return m, nil

	case "alt+enter", "ctrl+j":
		m.ta.InsertString("\n")
		return m, nil

	case "enter":
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			// Empty line still answers the wizard (accept default).
			if m.connectSt != nil {
				m.ta.Reset()
				m.ta.SetHeight(1)
				m.answerConnect("")
				m.renderTranscript()
			}
			return m, nil
		}
		m.ta.Reset()
		m.ta.SetHeight(1)
		m.entries = append(m.entries, entry{role: "user", content: text})
		// Wizard consumes plain answers.
		if m.connectSt != nil && !strings.HasPrefix(text, "/") {
			m.answerConnect(text)
			m.renderTranscript()
			return m, nil
		}
		if strings.HasPrefix(text, "/") {
			quit, cmd := m.runSlash(text)
			m.renderTranscript()
			if quit {
				return m, tea.Quit
			}
			return m, cmd
		}
		// !command runs shell directly and adds output to context.
		if strings.HasPrefix(text, "!") {
			cmd := strings.TrimSpace(strings.TrimPrefix(text, "!"))
			if cmd == "" {
				m.entries = append(m.entries, entry{role: "system", content: "Usage: !<shell command> (e.g. !ls)"})
				m.renderTranscript()
				return m, nil
			}
			m.renderTranscript()
			return m, m.runBangCmd(cmd)
		}
		m.renderTranscript()
		return m, m.startStream(text)
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	m.slashOff = false // typing re-enables suggestions
	if m.slashSel < 0 {
		m.slashSel = 0
	}
	lines := strings.Count(m.ta.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > 5 {
		lines = 5
	}
	m.ta.SetHeight(lines)
	return m, cmd
}

// --- slash ---

func (m *Model) runSlash(text string) (bool, tea.Cmd) {
	fields := strings.Fields(text)
	name := fields[0]
	args := fields[1:]
	switch name {
	case "/help":
		m.showHelp = true
		return false, nil
	case "/exit", "/quit", "/q":
		m.persist()
		return true, nil
	case "/new", "/clear":
		m.persist()
		m.sessionID = session.NewID()
		m.sources = nil
		m.entries = []entry{{role: "system", content: "New session started."}}
		m.persist()
		return false, nil
	case "/agent":
		if m.agent == "build" {
			m.agent = "plan"
		} else {
			m.agent = "build"
		}
		m.agentCtx.Agent = m.agent
		m.entries = append(m.entries, entry{role: "system", content: "Agent: " + m.agent})
		return false, nil
	case "/soul":
		m.entries = append(m.entries, entry{role: "system", content: m.soulReport()})
		return false, nil
	case "/memory":
		m.entries = append(m.entries, entry{role: "system", content: m.memoryReport()})
		return false, nil
	case "/permissions":
		m.entries = append(m.entries, entry{role: "system", content: m.permissionsReport()})
		return false, nil
	case "/search":
		if len(args) == 0 {
			m.entries = append(m.entries, entry{role: "system", content: "Usage: /search <query>"})
			return false, nil
		}
		q, _ := json.Marshal(strings.Join(args, " "))
		return false, m.runWebToolCmd("web_search", `{"query":`+string(q)+`}`)
	case "/fetch":
		if len(args) == 0 {
			m.entries = append(m.entries, entry{role: "system", content: "Usage: /fetch <url>"})
			return false, nil
		}
		u, _ := json.Marshal(args[0])
		return false, m.runWebToolCmd("web_fetch", `{"url":`+string(u)+`}`)
	case "/sources":
		m.entries = append(m.entries, entry{role: "system", content: m.sourcesReport()})
		return false, nil
	case "/sessions":
		if len(args) == 0 {
			m.openSessionPicker()
			return false, nil
		}
		if args[0] == "delete" && len(args) > 1 {
			if err := session.Delete(m.paths.SessionDir, args[1]); err != nil {
				m.entries = append(m.entries, entry{role: "error", content: "delete failed: " + err.Error()})
			} else {
				m.entries = append(m.entries, entry{role: "system", content: "deleted session " + args[1]})
				if args[1] == m.sessionID {
					m.sessionID = ""
				}
			}
			return false, nil
		}
		m.resumeSession(args[0])
		return false, nil
	case "/compact":
		m.runCompact()
		return false, nil
	case "/doctor":
		m.entries = append(m.entries, entry{role: "system", content: "doctor: checking…"})
		return false, m.doctorCmd(m.width, m.height)
	case "/connect":
		m.runConnectArgs(args)
		return false, nil
	case "/models", "/model":
		return false, m.runModelsArgs(args)
	default:
		m.entries = append(m.entries, entry{role: "system", content: "(slash: Milestone 3+ — " + text + ")"})
		return false, nil
	}
}

// --- streaming send ---

func (m *Model) startStream(userText string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelGen = cancel

	expanded, notes := expandAtFiles(userText, m.projectDir)
	for _, n := range notes {
		m.entries = append(m.entries, entry{role: "system", content: n})
	}
	mode := agent.ModeBuild
	if m.agent == "plan" {
		mode = agent.ModePlan
	}
	m.agentCtx.Agent = m.agent
	sys := agent.SystemPrompt(mode, m.agentCtx)
	m.loopMsgs = append([]provider.Message{{Role: "system", Content: sys}},
		append(m.history(20), provider.Message{Role: "user", Content: expanded})...)
	m.loopSteps = 0
	m.toolQueue = nil

	ch, err := m.prov.Stream(ctx, provider.ChatRequest{
		Model:    m.modelID,
		Messages: m.loopMsgs,
		Tools:    m.toolDefs(),
	})
	if err != nil {
		m.entries = append(m.entries, entry{role: "error",
			content: "provider: " + err.Error() + " (" + hintForProviderError(err.Error()) + ")"})
		m.cancelGen = nil
		return nil
	}
	m.evCh = ch
	m.assistantBuf = ""
	m.pendingOut = false
	m.generating = true
	return tea.Batch(m.sp.Tick, waitProviderEvent(ch))
}

// --- layout ---

func (m *Model) resize() {
	w := m.width
	if w <= 0 {
		w = 80
	}
	inputH := 3 + m.ta.Height()
	statusH := 1
	topH := 5
	spinnerH := 1
	reserved := topH + spinnerH + inputH + statusH + 4
	vpH := m.height - reserved
	if vpH < 5 {
		vpH = 5
	}
	m.vp.Width = w - 4
	m.vp.Height = vpH
	m.ta.SetWidth(w - 8)
	if m.ta.Width() < 10 {
		m.ta.SetWidth(10)
	}
}

func (m *Model) renderTranscript() {
	var sb strings.Builder
	for _, e := range m.entries {
		switch e.role {
		case "user":
			sb.WriteString(userStyle.Render("› you: ") + e.content + "\n\n")
		case "assistant":
			sb.WriteString(assistantStyle.Render("◆ multacode: ") + "\n" + highlightFences(e.content) + "\n\n")
		case "tool":
			if !m.showTool {
				sb.WriteString(toolStyle.Render("▸ tool (hidden, ctrl+r)") + "\n")
				continue
			}
			sb.WriteString(toolStyle.Render("▸ "+e.content) + "\n\n")
		case "error":
			sb.WriteString(errStyle.Render("✖ "+e.content) + "\n\n")
		default:
			sb.WriteString(hintStyle.Render("· "+e.content) + "\n\n")
		}
	}
	if m.generating && strings.TrimSpace(m.assistantBuf) != "" {
		sb.WriteString(assistantStyle.Render("◆ multacode: ") + "\n" + highlightFences(strings.TrimSpace(m.assistantBuf)) + "▌\n\n")
	}
	m.vp.SetContent(strings.TrimRight(sb.String(), "\n"))
	m.vp.GotoBottom()
}

func (m Model) View() string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	if m.showHelp {
		return renderHelp(w)
	}

	cardW := 44
	if w-2 < cardW {
		cardW = w - 2
	}
	if cardW < 20 {
		cardW = 20
	}
	topInner := fmt.Sprintf("%s\n%s\n%s",
		titleStyle.Render(">_ Multacode (v1.0)"),
		dimStyle.Render(fmt.Sprintf("model : %s", m.displayModel())),
		titleStyle.Render("/models to change"),
	)
	top := cardStyle.Width(cardW).Render(topInner)
	top = centerText(top, w)

	var mainBox string
	if m.approval != nil {
		mainBox = m.renderApproval(w)
	} else if m.sessPicker != nil {
		mainBox = m.renderSessionPicker(w)
	} else if m.picker != nil {
		mainBox = m.renderPicker(w)
	} else {
		panelW := w - 2
		if panelW < 20 {
			panelW = 20
		}
		innerH := m.vp.Height
		lines := strings.Split(m.vp.View(), "\n")
		for len(lines) < innerH {
			lines = append(lines, "")
		}
		mainBox = panelStyle.Width(panelW).Render(strings.Join(lines, "\n"))
		mainBox = withTitle(mainBox, "multacode")
	}

	statusRow := ""
	if m.approval != nil {
		statusRow = fmt.Sprintf("waiting approval: %s — y approve • n deny", m.approval.tool)
	} else if m.generating {
		statusRow = fmt.Sprintf("%s thinking… %d chars", m.sp.View(), len(strings.TrimSpace(m.assistantBuf)))
	} else if m.connectSt != nil {
		statusRow = hintStyle.Render("connect wizard • type answer or `cancel` • esc aborts")
	} else if m.picker != nil {
		statusRow = hintStyle.Render("models picker • ↑↓/jk move • enter select • esc close")
	} else {
		statusRow = hintStyle.Render(fmt.Sprintf("agent:%s • %s • tab switch • /help", m.agent, m.projectDir))
	}

	panelW := w - 2
	if panelW < 20 {
		panelW = 20
	}
	suggestBox := ""
	if sug := m.activeSuggest(); len(sug) > 0 {
		sel := m.slashSel
		if sel < 0 || sel >= len(sug) {
			sel = 0
		}
		var sb strings.Builder
		for i, c := range sug {
			if i == sel {
				sb.WriteString(suggestSelStyle.Render("> "+c) + "\n")
			} else {
				sb.WriteString(hintStyle.Render("  "+c) + "\n")
			}
		}
		suggestBox = inputFrameStyle.Width(panelW).Render(strings.TrimRight(sb.String(), "\n")) + "\n"
	}
	inputBox := inputFrameStyle.Width(panelW).Render(m.ta.View())

	help := hintStyle.Render("enter send • / commands ↑↓+tab • tab build/plan • ctrl+r tools • ctrl+h help • ctrl+c quit")

	parts := []string{"", top, mainBox, statusRow, suggestBox + inputBox, help}
	return strings.Join(parts, "\n")
}

func centerText(s string, width int) string {
	lines := strings.Split(s, "\n")
	max := 0
	for _, l := range lines {
		if w := lipgloss.Width(l); w > max {
			max = w
		}
	}
	pad := (width - max) / 2
	if pad < 0 {
		pad = 0
	}
	for i, l := range lines {
		lines[i] = strings.Repeat(" ", pad) + l
	}
	return strings.Join(lines, "\n")
}

// withTitle overlays a title on the top border (prototype parity).
func withTitle(box, title string) string {
	lines := strings.Split(box, "\n")
	if len(lines) == 0 {
		return box
	}
	top := lines[0]
	runes := []rune(top)
	t := " " + title + " "
	for i := 2; i < len(runes) && i-2 < len([]rune(t)); i++ {
		runes[i] = []rune(t)[i-2]
	}
	lines[0] = string(runes)
	return strings.Join(lines, "\n")
}

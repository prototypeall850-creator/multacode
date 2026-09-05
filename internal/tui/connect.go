// Connect wizard (/connect) and model picker (/models) state for the TUI.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"multacode/internal/config"
	"multacode/internal/provider"
)

// --- /connect wizard ---

// connectPreset is one entry of the /connect menu. Base is the default
// BaseURL saved for the provider ("" = preset built-in default).
// KeyHint tells the user where to get the key; empty = no key needed.
type connectPreset struct {
	id      string
	name    string
	popular bool // false = under PROVIDER LAIN
	kind    string
	base    string
	model   string
	keyHint string
	custom  bool // custom base/key/model: ask id + require base
}

var connectPresets = []connectPreset{
	{id: "zen", name: "Opencode Zen (free)", popular: true, kind: "zen", base: "", model: "nemotron-3-ultra-free"},
	{id: "go", name: "Opencode Go", popular: true, kind: "zen", base: "https://opencode.ai/zen/go/v1", model: "claude-sonnet-4-6", keyHint: "API key from opencode.ai (login → API keys, Go subscription)"},
	{id: "openai", name: "OpenAI", popular: true, kind: "openai-compatible", base: "https://api.openai.com/v1", model: "gpt-4o-mini", keyHint: "sk-... from platform.openai.com → API keys"},
	{id: "copilot", name: "Github Copilot", popular: true, kind: "openai-compatible", base: "https://api.githubcopilot.com", model: "gpt-4o", keyHint: "token from `gh auth token` (GitHub CLI, needs Copilot access) — experimental"},
	{id: "anthropic", name: "Anthropic", popular: true, kind: "anthropic", base: "https://api.anthropic.com", model: "claude-sonnet-4-6", keyHint: "sk-ant-... from console.anthropic.com → API keys"},
	{id: "google", name: "Google", popular: true, kind: "openai-compatible", base: "https://generativelanguage.googleapis.com/v1beta/openai", model: "gemini-2.5-flash", keyHint: "key from aistudio.google.com → Get API key"},
	{id: "meta", name: "Meta", popular: true, kind: "openai-compatible", base: "https://api.meta.ai/v1", model: "muse-spark-1.3", keyHint: "MODEL_API_KEY from developer.meta.com"},
	{id: "oai-custom", name: "OpenAI-compatible", popular: false, kind: "openai-compatible", keyHint: "API key for your endpoint (stored in auth.json; enter = none)", custom: true},
	{id: "ant-custom", name: "Anthropic-compatible", popular: false, kind: "anthropic", keyHint: "API key for your endpoint (stored in auth.json; enter = none)", custom: true},
}

func findPreset(s string) *connectPreset {
	s = strings.ToLower(strings.TrimSpace(s))
	for i := range connectPresets {
		if s == connectPresets[i].id || s == fmt.Sprint(presetNum(&connectPresets[i])) {
			return &connectPresets[i]
		}
	}
	return nil
}

func presetNum(p *connectPreset) int {
	for i := range connectPresets {
		if &connectPresets[i] == p {
			return i + 1
		}
	}
	return 0
}

func connectMenu() string {
	var sb strings.Builder
	sb.WriteString("Pick a provider (number or id), or `cancel`.\nPOPULAR\n")
	for i := range connectPresets {
		p := &connectPresets[i]
		if !p.popular {
			continue
		}
		extra := ""
		if p.keyHint == "" {
			extra = " — no key needed"
		}
		fmt.Fprintf(&sb, "  %d) %s  %s%s\n", i+1, p.id, p.name, extra)
	}
	sb.WriteString("PROVIDER LAIN\n")
	for i := range connectPresets {
		p := &connectPresets[i]
		if p.popular {
			continue
		}
		fmt.Fprintf(&sb, "  %d) %s  %s (custom base URL)\n", i+1, p.id, p.name)
	}
	return strings.TrimRight(sb.String(), "\n")
}

type connectState struct {
	step   int // 0:pick 1:id(custom) 2:base 3:key 4:model
	preset *connectPreset
	id     string
	base   string
	key    string
}

func (m *Model) startConnectWizard() {
	m.connectSt = &connectState{step: 0}
	m.entries = append(m.entries, entry{role: "system", content: connectMenu()})
}

// answerConnect consumes a non-slash user message as a wizard answer.
// Returns true when the message was consumed.
func (m *Model) answerConnect(text string) bool {
	st := m.connectSt
	if st == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(text), "cancel") {
		m.connectSt = nil
		m.entries = append(m.entries, entry{role: "system", content: "Connect cancelled."})
		return true
	}
	switch st.step {
	case 0: // pick preset
		p := findPreset(text)
		if p == nil {
			m.entries = append(m.entries, entry{role: "system", content: "Unknown choice. Type a number (1-" + fmt.Sprint(len(connectPresets)) + ") or id, or `cancel`."})
			return true
		}
		if findProvider(m.cfg.Providers, p.id) != nil && !p.custom {
			m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("%q already added. Use `/connect %s <api-key>` to update its key.", p.id, p.id)})
			return true
		}
		st.preset = p
		if p.custom {
			st.step = 1
			m.entries = append(m.entries, entry{role: "system", content: "Provider id? (one word, e.g. myproxy — or `cancel`)"})
			return true
		}
		st.id = p.id
		st.step = 2
		m.entries = append(m.entries, entry{role: "system", content: basePrompt(p, "")})
		return true
	case 1: // custom id
		id := strings.TrimSpace(text)
		if id == "" || strings.ContainsAny(id, " \t/") {
			m.entries = append(m.entries, entry{role: "system", content: "Id must be one word (letters, digits, dash). Try again or `cancel`."})
			return true
		}
		if findProvider(m.cfg.Providers, id) != nil {
			m.entries = append(m.entries, entry{role: "system", content: "Id already exists. Use `/connect " + id + " <api-key>` to update its key, or pick another id."})
			return true
		}
		st.id = id
		st.step = 2
		m.entries = append(m.entries, entry{role: "system", content: basePrompt(st.preset, "")})
		return true
	case 2: // base URL
		base := strings.TrimSpace(text)
		if base == "" {
			base = st.preset.base
		}
		if base == "" && st.preset.kind == "openai-compatible" {
			m.entries = append(m.entries, entry{role: "system", content: "Base URL is required (e.g. https://openrouter.ai/api/v1). Try again or `cancel`."})
			return true
		}
		st.base = base
		st.step = 3
		m.entries = append(m.entries, entry{role: "system", content: keyPrompt(st.preset)})
		return true
	case 3: // api key
		st.key = strings.TrimSpace(text)
		m.redactLastUser()
		st.step = 4
		m.entries = append(m.entries, entry{role: "system", content: modelPrompt(st.preset)})
		return true
	case 4: // default model
		model := strings.TrimSpace(text)
		if model == "" {
			model = st.preset.model
		}
		m.finishConnect(st.id, st.preset, st.base, st.key, model)
		m.connectSt = nil
		m.rebuildProvider()
	}
	return true
}

func basePrompt(p *connectPreset, _ string) string {
	if p.base != "" {
		return fmt.Sprintf("Base URL? (enter = %s)", p.base)
	}
	if p.kind == "zen" && p.id == "zen" {
		return "Base URL? (enter = Zen default, keyless free tier)"
	}
	return "Base URL? (required — e.g. https://openrouter.ai/api/v1)"
}

func keyPrompt(p *connectPreset) string {
	if p.keyHint != "" {
		return fmt.Sprintf("API key? (%s)", p.keyHint)
	}
	return "API key? (free tier works WITHOUT a key — enter = none, stored in auth.json if given)"
}

func modelPrompt(p *connectPreset) string {
	if p.model != "" {
		return fmt.Sprintf("Default model? (enter = %s — `/models` lists the live catalog)", p.model)
	}
	return "Default model? (e.g. gpt-4o-mini — or enter to pick later via `/models`)"
}

func (m *Model) finishConnect(id string, p *connectPreset, base, key, model string) {
	pc := config.ProviderConfig{ID: id, Kind: p.kind, Name: p.name, BaseURL: base, DefaultModel: model, APIKeyRef: "auth:" + id}
	m.cfg.Providers = append(m.cfg.Providers, pc)
	if key != "" {
		if m.auth == nil {
			m.auth = config.Auth{}
		}
		m.auth[id] = key
	}
	if len(m.cfg.Providers) == 1 {
		m.cfg.DefaultProvider = id
		m.cfg.DefaultModel = model
	}
	if err := m.saveAll(); err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "save config: " + err.Error()})
		return
	}
	msg := fmt.Sprintf("✓ Saved provider %q (%s).", id, p.name)
	switch {
	case key != "":
		msg += " Key stored in auth.json."
	case p.keyHint == "":
		msg += " (free tier, no key needed)"
	}
	msg += " Tip: `/models` shows the live catalog."
	m.entries = append(m.entries, entry{role: "system", content: msg})
}

func defaultModelForKind(kind string) string {
	switch kind {
	case "zen":
		return "nemotron-3-ultra-free"
	case "anthropic":
		return "claude-sonnet-4-6"
	default:
		return ""
	}
}

func findProvider(list []config.ProviderConfig, id string) *config.ProviderConfig {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func (m *Model) redactLastUser() {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].role == "user" {
			m.entries[i].content = "•••••• (redacted)"
			return
		}
	}
}

// runConnectArgs handles `/connect ...` with arguments.
func (m *Model) runConnectArgs(args []string) {
	if len(args) == 0 {
		if len(m.cfg.Providers) == 0 {
			m.entries = append(m.entries, entry{role: "system", content: "No providers yet. Type `/connect new` to add one."})
			m.startConnectWizardSilent()
			return
		}
		var sb strings.Builder
		sb.WriteString("Providers:\n")
		for _, p := range m.cfg.Providers {
			mark := " "
			if p.ID == m.providerID {
				mark = "✓"
			}
			sb.WriteString(fmt.Sprintf("%s %s (%s) default=%s\n", mark, p.ID, p.Kind, p.DefaultModel))
		}
		sb.WriteString("\nUsage: `/connect new` | `/connect <id> <api-key>` | `/connect list`")
		m.entries = append(m.entries, entry{role: "system", content: strings.TrimRight(sb.String(), "\n")})
		return
	}
	switch args[0] {
	case "new":
		m.startConnectWizard()
	case "list":
		if len(m.cfg.Providers) == 0 {
			m.entries = append(m.entries, entry{role: "system", content: "No providers yet. Type `/connect new`."})
			return
		}
		var sb strings.Builder
		for _, p := range m.cfg.Providers {
			mark := " "
			if p.ID == m.providerID {
				mark = "✓"
			}
			fmt.Fprintf(&sb, "%s %s (%s) default=%s\n", mark, p.ID, p.Kind, p.DefaultModel)
		}
		m.entries = append(m.entries, entry{role: "system", content: strings.TrimRight(sb.String(), "\n")})
	default:
		// `/connect <id> <api-key>`
		if len(args) < 2 {
			m.entries = append(m.entries, entry{role: "system", content: "Usage: `/connect <id> <api-key>`"})
			return
		}
		id := args[0]
		if findProvider(m.cfg.Providers, id) == nil {
			m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("Unknown provider %q. Type `/connect new` to add it.", id)})
			return
		}
		if m.auth == nil {
			m.auth = config.Auth{}
		}
		m.auth[id] = strings.Join(args[1:], " ")
		m.redactLastUser()
		if err := m.saveAll(); err != nil {
			m.entries = append(m.entries, entry{role: "error", content: "save auth: " + err.Error()})
			return
		}
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("✓ Updated key for %q.", id)})
		m.rebuildProvider()
	}
}

// startConnectWizardSilent begins the wizard without duplicating the prompt.
func (m *Model) startConnectWizardSilent() {
	m.connectSt = &connectState{step: 0}
	m.entries = append(m.entries, entry{role: "system", content: connectMenu()})
}

// --- /models picker ---

type pickerRow struct {
	prov  string
	model string
	tags  string
}

type modelsPicker struct {
	rows   []pickerRow
	cursor int
	offset int
}

type modelsFetchedMsg struct {
	models map[string][]provider.Model
	errs   map[string]string
}

func (m *Model) runModelsArgs(args []string) tea.Cmd {
	if len(args) == 0 {
		if len(m.cfg.Providers) == 0 {
			m.entries = append(m.entries, entry{role: "system", content: "No providers configured. Run `/connect new` first (Zen free tier needs no key), then `/models` again."})
			return nil
		}
		m.entries = append(m.entries, entry{role: "system", content: "Fetching models from " + fmt.Sprintf("%d provider(s)…", len(m.cfg.Providers))})
		m.pickerPending = true
		return m.fetchModelsCmd()
	}
	// `/models <prov>` or `/models <prov> <model>` or `<prov>/<model>`
	var provID, modelID string
	if len(args) == 1 && strings.Contains(args[0], "/") {
		parts := strings.SplitN(args[0], "/", 2)
		provID, modelID = parts[0], parts[1]
	} else {
		provID = args[0]
		if len(args) > 1 {
			modelID = strings.Join(args[1:], " ")
		}
	}
	pc := findProvider(m.cfg.Providers, provID)
	if pc == nil {
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("Unknown provider %q. `/models` to list.", provID)})
		return nil
	}
	if modelID == "" {
		modelID = pc.DefaultModel
	}
	if modelID == "" {
		if cached, ok := m.modelsCache[provID]; ok && len(cached) > 0 {
			modelID = cached[0].ID
		}
	}
	m.cfg.DefaultProvider = provID
	if modelID != "" {
		m.cfg.DefaultModel = modelID
	}
	if err := m.saveAll(); err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "save config: " + err.Error()})
		return nil
	}
	m.providerID = provID
	m.rebuildProvider()
	m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("✓ Active: %s / %s", m.providerID, m.modelID)})
	return nil
}

func (m *Model) fetchModelsCmd() tea.Cmd {
	provs := append([]config.ProviderConfig(nil), m.cfg.Providers...)
	auth := map[string]string{}
	for k, v := range m.auth {
		auth[k] = v
	}
	return func() tea.Msg {
		out := modelsFetchedMsg{models: map[string][]provider.Model{}, errs: map[string]string{}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, pc := range provs {
			p, err := provider.BuildProvider(pc, auth)
			if err != nil {
				out.errs[pc.ID] = "provider init failed: " + err.Error()
				continue
			}
			models, err := p.ListModels(ctx)
			if err != nil {
				out.errs[pc.ID] = err.Error()
				// Fallback to default model if available.
				if pc.DefaultModel != "" {
					out.models[pc.ID] = []provider.Model{{ID: pc.DefaultModel}}
				}
				continue
			}
			out.models[pc.ID] = models
		}
		return out
	}
}

func (m *Model) buildPickerRows() []pickerRow {
	var rows []pickerRow
	for _, pc := range m.cfg.Providers {
		models, ok := m.modelsCache[pc.ID]
		if !ok || len(models) == 0 {
			def := pc.DefaultModel
			if def == "" {
				def = "(no default — type to add)"
			}
			rows = append(rows, pickerRow{prov: pc.ID, model: def})
			continue
		}
		// Free models first: most users on Zen come for the -free list.
		ordered := append([]provider.Model(nil), models...)
		sort.SliceStable(ordered, func(i, j int) bool {
			return hasTag(ordered[i].Tags, "free") && !hasTag(ordered[j].Tags, "free")
		})
		for _, mdl := range ordered {
			rows = append(rows, pickerRow{prov: pc.ID, model: mdl.ID, tags: strings.Join(mdl.Tags, ",")})
		}
	}
	return rows
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// provDisplayName returns the friendly provider name for picker group headers.
func (m *Model) provDisplayName(id string) string {
	if pc := findProvider(m.cfg.Providers, id); pc != nil && pc.Name != "" {
		return pc.Name
	}
	if p := findPreset(id); p != nil {
		return p.name
	}
	return id
}

func (m *Model) selectPickerRow(r pickerRow) {
	m.cfg.DefaultProvider = r.prov
	if !strings.HasPrefix(r.model, "(no default") {
		m.cfg.DefaultModel = r.model
	}
	if err := m.saveAll(); err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "save config: " + err.Error()})
	} else {
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("✓ Selected: %s / %s", m.cfg.DefaultProvider, m.cfg.DefaultModel)})
	}
	m.picker = nil
	m.providerID = r.prov
	m.rebuildProvider()
}

func (m *Model) renderPicker(width int) string {
	title := "models — ↑↓/jk move • enter select • esc close • ✓ = active"
	var sb strings.Builder
	sb.WriteString(titleStyle.Render(title) + "\n")
	rows := m.picker.rows
	if len(rows) == 0 {
		sb.WriteString(hintStyle.Render("No models. Run /connect to add a provider, then /models again.") + "\n")
		return panelStyle.Width(width - 4).Render(sb.String())
	}
	// Scroll window.
	maxRows := 12
	if m.height > 0 && m.height-14 < maxRows {
		maxRows = m.height - 14
	}
	if maxRows < 4 {
		maxRows = 4
	}
	if m.picker.cursor < m.picker.offset {
		m.picker.offset = m.picker.cursor
	}
	if m.picker.cursor >= m.picker.offset+maxRows {
		m.picker.offset = m.picker.cursor - maxRows + 1
	}
	for i := m.picker.offset; i < len(rows) && i < m.picker.offset+maxRows; i++ {
		r := rows[i]
		if i == m.picker.offset || rows[i-1].prov != r.prov {
			sb.WriteString(dimStyle.Render("── "+m.provDisplayName(r.prov)+" ──") + "\n")
		}
		line := fmt.Sprintf("%s / %s", r.prov, r.model)
		if r.tags != "" {
			line += "  [" + r.tags + "]"
		}
		if r.prov == m.providerID && r.model == m.modelID {
			line += "  ✓"
		}
		if i == m.picker.cursor {
			line = "> " + line
			sb.WriteString(userStyle.Render(line) + "\n")
		} else {
			sb.WriteString("  " + line + "\n")
		}
	}
	return panelStyle.Width(width - 4).Render(strings.TrimRight(sb.String(), "\n"))
}

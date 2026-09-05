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

type connectState struct {
	step int // 0:id 1:kind 2:base 3:key 4:model
	id   string
	kind string
	base string
	key  string
}

func (m *Model) startConnectWizard() {
	m.connectSt = &connectState{step: 0}
	m.entries = append(m.entries, entry{role: "system", content: "Connect a provider. Type `cancel` anytime. Provider id? (e.g. zen)"})
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
	case 0:
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
		st.step = 1
		m.entries = append(m.entries, entry{role: "system", content: "Kind? [openai-compatible | anthropic | zen] (default: zen)"})
	case 1:
		kind := strings.ToLower(strings.TrimSpace(text))
		if kind == "" {
			kind = "zen"
		}
		switch kind {
		case "openai", "oai":
			kind = "openai-compatible"
		case "openai-compatible", "anthropic", "zen":
		default:
			m.entries = append(m.entries, entry{role: "system", content: "Unknown kind. Choose openai-compatible, anthropic, or zen."})
			return true
		}
		st.kind = kind
		st.step = 2
		switch kind {
		case "zen":
			m.entries = append(m.entries, entry{role: "system", content: "Base URL? (enter = Zen default https://opencode.ai/zen/v1/responses)"})
		case "anthropic":
			m.entries = append(m.entries, entry{role: "system", content: "Base URL? (enter = https://api.anthropic.com)"})
		default:
			m.entries = append(m.entries, entry{role: "system", content: "Base URL? (e.g. https://openrouter.ai/api/v1 — required for openai-compatible)"})
		}
	case 2:
		base := strings.TrimSpace(text)
		if st.kind == "openai-compatible" && base == "" {
			m.entries = append(m.entries, entry{role: "system", content: "Base URL is required for openai-compatible. Try again or `cancel`."})
			return true
		}
		st.base = base
		st.step = 3
		if st.kind == "zen" {
			m.entries = append(m.entries, entry{role: "system", content: "API key? (free tier works WITHOUT a key — enter = none, stored in auth.json if given)"})
		} else {
			m.entries = append(m.entries, entry{role: "system", content: "API key? (stored in auth.json, never in memory; enter = none)"})
		}
	case 3:
		st.key = strings.TrimSpace(text)
		m.redactLastUser()
		st.step = 4
		def := defaultModelForKind(st.kind)
		if def == "" {
			m.entries = append(m.entries, entry{role: "system", content: "Default model? (e.g. gpt-4o-mini)"})
		} else {
			m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("Default model? (enter = %s)", def)})
		}
	case 4:
		model := strings.TrimSpace(text)
		if model == "" {
			model = defaultModelForKind(st.kind)
		}
		pc := config.ProviderConfig{ID: st.id, Kind: st.kind, BaseURL: st.base, DefaultModel: model, APIKeyRef: "auth:" + st.id}
		if st.kind == "zen" {
			pc.Name = "OpenCode Zen"
		}
		m.cfg.Providers = append(m.cfg.Providers, pc)
		if st.key != "" {
			if m.auth == nil {
				m.auth = config.Auth{}
			}
			m.auth[st.id] = st.key
		}
		if len(m.cfg.Providers) == 1 {
			m.cfg.DefaultProvider = st.id
			m.cfg.DefaultModel = model
		}
		if err := m.saveAll(); err != nil {
			m.entries = append(m.entries, entry{role: "error", content: "save config: " + err.Error()})
		} else {
			msg := fmt.Sprintf("Saved provider %q (%s). Key stored in auth.json.", st.id, st.kind)
			if st.kind == "zen" {
				msg += " Tip: `/models` shows the live list incl. -free models."
			}
			m.entries = append(m.entries, entry{role: "system", content: msg})
		}
		m.connectSt = nil
		m.rebuildProvider()
	}
	return true
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
				mark = "*"
			}
			sb.WriteString(fmt.Sprintf("%s %s (%s) default=%s\n", mark, p.ID, p.Kind, p.DefaultModel))
		}
		sb.WriteString("Usage: `/connect new` | `/connect <id> <api-key>` | `/connect list`")
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
			fmt.Fprintf(&sb, "- %s (%s) default=%s\n", p.ID, p.Kind, p.DefaultModel)
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
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("Updated key for %q.", id)})
		m.rebuildProvider()
	}
}

// startConnectWizardSilent begins the wizard without duplicating the prompt.
func (m *Model) startConnectWizardSilent() {
	m.connectSt = &connectState{step: 0}
	m.entries = append(m.entries, entry{role: "system", content: "Provider id? (e.g. zen, or `cancel`)"})
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
			m.entries = append(m.entries, entry{role: "system", content: "No providers yet. Run `/connect new` first (Zen free tier needs no key), then `/models` again."})
			return nil
		}
		m.entries = append(m.entries, entry{role: "system", content: "Fetching models…"})
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
	m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("Active: %s / %s", m.providerID, m.modelID)})
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
				out.errs[pc.ID] = err.Error()
				continue
			}
			models, err := p.ListModels(ctx)
			if err != nil {
				out.errs[pc.ID] = err.Error()
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

func (m *Model) selectPickerRow(r pickerRow) {
	m.cfg.DefaultProvider = r.prov
	if !strings.HasPrefix(r.model, "(no default") {
		m.cfg.DefaultModel = r.model
	}
	if err := m.saveAll(); err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "save config: " + err.Error()})
	} else {
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("Active: %s / %s", m.cfg.DefaultProvider, m.cfg.DefaultModel)})
	}
	m.picker = nil
	m.providerID = r.prov
	m.rebuildProvider()
}

func (m *Model) renderPicker(width int) string {
	title := "models — ↑↓/jk move • enter select • esc close • * = active"
	var sb strings.Builder
	sb.WriteString(titleStyle.Render(title) + "\n")
	rows := m.picker.rows
	if len(rows) == 0 {
		sb.WriteString(hintStyle.Render("No models. /connect to add a provider.") + "\n")
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
		line := fmt.Sprintf("%s / %s", r.prov, r.model)
		if r.tags != "" {
			line += "  [" + r.tags + "]"
		}
		if r.prov == m.providerID && r.model == m.modelID {
			line += "  *"
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

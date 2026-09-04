// Session persistence, resume picker, /compact, and /doctor (Milestone 6).
package tui

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"multacode/internal/config"
	"multacode/internal/env"
	"multacode/internal/provider"
	"multacode/internal/session"
)

// persist writes the transcript to the session dir. Best-effort:
// failures become a visible error entry instead of silent loss.
func (m *Model) persist() {
	if m.paths.SessionDir == "" {
		return
	}
	if m.sessionID == "" {
		m.sessionID = session.NewID()
	}
	ents := make([]session.Entry, 0, len(m.entries))
	now := time.Now().UTC().Format(time.RFC3339)
	for _, e := range m.entries {
		ents = append(ents, session.Entry{Role: e.role, Content: e.content, Time: now})
	}
	s := session.Session{
		ID: m.sessionID, Project: m.projectDir,
		Model: m.displayModel(), Agent: m.agent, Entries: ents,
	}
	if err := session.Save(m.paths.SessionDir, s); err != nil {
		m.entries = append(m.entries, entry{role: "error",
			content: "session save failed: " + err.Error() + " (check disk space/permissions)"})
	}
}

// --- /sessions picker ---

type sessionRow struct {
	id      string
	project string
	preview string
	count   int
	updated string
}

type sessionPicker struct {
	rows   []sessionRow
	cursor int
	offset int
}

func (m *Model) openSessionPicker() {
	if m.paths.SessionDir == "" {
		m.entries = append(m.entries, entry{role: "system", content: "sessions: no session dir configured"})
		return
	}
	metas, err := session.ListMeta(m.paths.SessionDir)
	if err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "sessions: " + err.Error()})
		return
	}
	var rows []sessionRow
	for _, mt := range metas {
		if mt.Project != "" && mt.Project != m.projectDir {
			continue
		}
		rows = append(rows, sessionRow{
			id: mt.ID, project: mt.Project,
			preview: firstLine(mt.Preview, 48), count: mt.Count, updated: mt.Updated,
		})
	}
	if len(rows) == 0 {
		m.entries = append(m.entries, entry{role: "system", content: "sessions: none saved for this project yet"})
		return
	}
	m.sessPicker = &sessionPicker{rows: rows}
}

func firstLine(s string, n int) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	if s == "" {
		return "(empty)"
	}
	return s
}

func (m *Model) resumeSession(id string) {
	if m.paths.SessionDir == "" {
		return
	}
	s, err := session.Load(m.paths.SessionDir, id)
	if err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "resume failed: " + err.Error()})
		return
	}
	m.persist() // keep the current transcript before switching
	m.sessionID = s.ID
	m.entries = make([]entry, 0, len(s.Entries))
	for _, e := range s.Entries {
		m.entries = append(m.entries, entry{role: e.Role, content: e.Content})
	}
	if s.Agent == "build" || s.Agent == "plan" {
		m.agent = s.Agent
		m.agentCtx.Agent = s.Agent
	}
	m.sources = nil
	m.sessPicker = nil
	m.entries = append(m.entries, entry{role: "system",
		content: fmt.Sprintf("resumed session %s (%d entries)", s.ID, len(s.Entries))})
}

func (m *Model) renderSessionPicker(width int) string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("sessions — enter resume • d delete • esc close") + "\n")
	maxRows := 12
	if m.height > 0 && m.height-14 < maxRows {
		maxRows = m.height - 14
	}
	if maxRows < 4 {
		maxRows = 4
	}
	p := m.sessPicker
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+maxRows {
		p.offset = p.cursor - maxRows + 1
	}
	for i := p.offset; i < len(p.rows) && i < p.offset+maxRows; i++ {
		r := p.rows[i]
		line := fmt.Sprintf("%s  %s  (%d)", r.id, r.preview, r.count)
		if i == p.cursor {
			sb.WriteString(userStyle.Render("> "+line) + "\n")
		} else {
			sb.WriteString("  " + line + "\n")
		}
	}
	return panelStyle.Width(width - 4).Render(strings.TrimRight(sb.String(), "\n"))
}

func (m *Model) deletePickedSession() {
	p := m.sessPicker
	if p == nil || len(p.rows) == 0 {
		return
	}
	id := p.rows[p.cursor].id
	if err := session.Delete(m.paths.SessionDir, id); err != nil {
		m.entries = append(m.entries, entry{role: "error", content: "delete failed: " + err.Error()})
		return
	}
	if id == m.sessionID {
		m.sessionID = ""
	}
	m.entries = append(m.entries, entry{role: "system", content: "deleted session " + id})
	p.rows = append(p.rows[:p.cursor], p.rows[p.cursor+1:]...)
	if p.cursor >= len(p.rows) && p.cursor > 0 {
		p.cursor--
	}
	if len(p.rows) == 0 {
		m.sessPicker = nil
	}
}

// --- /compact ---

var editedPathRe = regexp.MustCompile(`edited (\S+)`)

func (m *Model) runCompact() {
	const window = 8
	if len(m.entries) <= window+2 {
		m.entries = append(m.entries, entry{role: "system", content: "compact: transcript already small, nothing pruned"})
		return
	}
	var nUser, nAsst, nTool int
	files := map[string]bool{}
	for _, e := range m.entries {
		switch e.role {
		case "user":
			nUser++
			for _, mt := range atFileRe.FindAllStringSubmatch(e.content, 10) {
				files[strings.Trim(mt[1], ".,:;!?")] = true
			}
		case "assistant":
			nAsst++
		case "tool":
			nTool++
			for _, mt := range editedPathRe.FindAllStringSubmatch(e.content, 4) {
				files[mt[1]] = true
			}
		}
	}
	var fl []string
	for f := range files {
		fl = append(fl, f)
	}
	sort.Strings(fl)
	if len(fl) > 10 {
		fl = append(fl[:10], "…")
	}
	summary := fmt.Sprintf("compacted %d entries (%d user, %d assistant, %d tool); kept last %d",
		len(m.entries), nUser, nAsst, nTool, window)
	if len(fl) > 0 {
		summary += "; files: " + strings.Join(fl, ", ")
	}
	if len(m.sources) > 0 {
		summary += fmt.Sprintf("; sources: %d", len(m.sources))
	}
	kept := append([]entry(nil), m.entries[len(m.entries)-window:]...)
	head := []entry{}
	if len(m.entries) > 0 && m.entries[0].role == "system" {
		head = append(head, m.entries[0])
	}
	m.entries = append(head, entry{role: "system", content: summary})
	m.entries = append(m.entries, kept...)
	m.persist()
}

// --- /doctor ---

type doctorMsg struct {
	report  string
	profile env.Profile
}

func (m *Model) doctorCmd(width, height int) tea.Cmd {
	provs := append([]config.ProviderConfig(nil), m.cfg.Providers...)
	auth := map[string]string{}
	for k, v := range m.auth {
		auth[k] = v
	}
	return func() tea.Msg {
		p := env.Collect()
		var sb strings.Builder
		sb.WriteString("doctor:\n")
		fmt.Fprintf(&sb, "- env: OS=%s arch=%s termux=%v shell=%s term=%dx%d\n",
			p.OS, p.Arch, p.IsTermux, p.Shell, width, height)
		for _, c := range []string{"go", "git", "rg", "curl", "pkg", "sh"} {
			if _, err := exec.LookPath(c); err == nil {
				fmt.Fprintf(&sb, "- tool %-4s ok\n", c)
			} else {
				fmt.Fprintf(&sb, "- tool %-4s MISSING", c)
				if c == "rg" {
					sb.WriteString(" (fallback Go search active)")
				}
				if c == "pkg" && !p.IsTermux {
					sb.WriteString(" (only needed on Termux)")
				}
				sb.WriteString("\n")
			}
		}
		if len(provs) == 0 {
			sb.WriteString("- providers: none configured (/connect new)\n")
		}
		for _, pc := range provs {
			pr, err := provider.BuildProvider(pc, auth)
			if err != nil {
				fmt.Fprintf(&sb, "- provider %s: config error: %s\n", pc.ID, err.Error())
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			models, err := pr.ListModels(ctx)
			cancel()
			if err != nil {
				fmt.Fprintf(&sb, "- provider %s: FAIL %s (%s)\n", pc.ID, err.Error(), hintForProviderError(err.Error()))
				continue
			}
			fmt.Fprintf(&sb, "- provider %s: ok (%d model(s))\n", pc.ID, len(models))
		}
		if m.paths.SessionDir != "" {
			n := session.CountForProject(m.paths.SessionDir, m.projectDir)
			fmt.Fprintf(&sb, "- sessions: %d saved for this project\n", n)
		}
		return doctorMsg{report: strings.TrimRight(sb.String(), "\n"), profile: p}
	}
}

// hintForProviderError maps common failures to the next action.
func hintForProviderError(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "401") || strings.Contains(l, "unauthorized") ||
		strings.Contains(l, "invalid api key") || strings.Contains(l, "bad key") ||
		strings.Contains(l, "api key"):
		return "hint: key rejected — /connect <id> <new-key>, then /models to retry"
	case strings.Contains(l, "402") || strings.Contains(l, "quota") ||
		strings.Contains(l, "insufficient") || strings.Contains(l, "billing"):
		return "hint: billing/quota — check the provider dashboard or switch via /models"
	case strings.Contains(l, "404") || strings.Contains(l, "not found") ||
		strings.Contains(l, "no such model"):
		return "hint: model not found — /models to pick an available one"
	case strings.Contains(l, "timeout") || strings.Contains(l, "deadline") ||
		strings.Contains(l, "connection") || strings.Contains(l, "resolve") ||
		strings.Contains(l, "network") || strings.Contains(l, "no such host"):
		return "hint: network issue — retry, or /doctor to check connectivity"
	default:
		return "hint: /doctor checks provider connectivity"
	}
}

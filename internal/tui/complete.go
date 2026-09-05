// Slash-command autocomplete: typing "/" shows up to 5 matches,
// navigable with up/down, completed with tab/enter, dismissed with esc.
package tui

import (
	"regexp"
	"strings"
)

var slashCommands = []string{
	"/help", "/connect", "/models", "/model", "/sessions",
	"/new", "/clear", "/agent", "/permissions", "/soul", "/memory",
	"/search", "/fetch", "/sources", "/compact", "/doctor",
	"/exit", "/quit",
}

var slashPrefixRe = regexp.MustCompile(`^/[A-Za-z-]*$`)

// slashSuggest returns up to 5 commands matching a partial "/..." input.
// Only active while the input has no space yet.
func slashSuggest(input string) []string {
	if !slashPrefixRe.MatchString(input) {
		return nil
	}
	var out []string
	for _, c := range slashCommands {
		if strings.HasPrefix(c, input) {
			out = append(out, c)
			if len(out) >= 5 {
				break
			}
		}
	}
	return out
}

func isExactSlashCommand(input string) bool {
	for _, c := range slashCommands {
		if c == input {
			return true
		}
	}
	return false
}

// activeSuggest returns the visible suggestions (nil when dismissed).
func (m *Model) activeSuggest() []string {
	if m.slashOff {
		return nil
	}
	return slashSuggest(m.ta.Value())
}

// completeSuggest replaces the input with the selected suggestion + space.
func (m *Model) completeSuggest(sug []string) {
	if len(sug) == 0 {
		return
	}
	if m.slashSel < 0 || m.slashSel >= len(sug) {
		m.slashSel = 0
	}
	m.ta.SetValue(sug[m.slashSel] + " ")
	m.slashSel = 0
	m.ta.SetHeight(1)
}

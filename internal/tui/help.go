package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const helpText = `multacode — help

Chat:
  enter            send message
  alt+enter        newline (termux: use ctrl+j)
  ctrl+j           newline
  tab              switch build/plan agent
  ctrl+r           toggle tool detail
  ctrl+h           toggle this help
  esc              close help/modal
  ctrl+c           cancel generation, 2x to quit

Slash:
  /help /sessions /new /agent /memory /sources
  /permissions /soul /search /fetch /compact /doctor /exit
  /sessions                  resume picker (enter resume, d delete)
  /compact                   prune old tool output, keep summary
  /connect new                 add provider (wizard)
  /connect <id> <api-key>      update provider key
  /models                      pick provider/model
  /models <prov>[/<model>]     switch directly
  /soul /memory /permissions   context, memory, policy views

Context:
  @file            attach file content to your message
  !command         run shell now, output joins context
  y / n            approve or deny a tool call (modal)
  edit (build)     agent edit shows diff, needs approval

esc closes this screen.`

func renderHelp(width int) string {
	if width <= 0 || width > 100 {
		width = 76
	}
	box := panelStyle.Width(width - 4).Render(helpText)
	// Center-ish: pad left when wide.
	if width >= 60 {
		pad := (width - lipgloss.Width(box)) / 2
		if pad > 0 {
			lines := strings.Split(box, "\n")
			for i, l := range lines {
				lines[i] = strings.Repeat(" ", pad) + l
			}
			return strings.Join(lines, "\n")
		}
	}
	return box
}

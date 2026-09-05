// Package agent context loads the plan.md prompt stack:
// runtime guard > core prompt > SOUL.md > memory > environment profile.
package agent

import (
	"os"
	"path/filepath"
	"strings"

	"multacode/internal/config"
	"multacode/internal/env"
)

const maxLayerBytes = 8 * 1024

// SoulLayer is one active SOUL.md file (project wins over global).
type SoulLayer struct {
	Source  string `json:"source"` // project | global
	Path    string `json:"path"`
	Content string `json:"content"`
}

// PromptContext carries everything injected above the user request.
type PromptContext struct {
	ProjectDir    string
	Env           env.Profile
	Soul          []SoulLayer
	ProjectMemory string
	UserMemory    string
	Agent         string
}

// LoadContext reads soul + memory files and the environment profile.
// Missing files are not errors; callers report them as inactive layers.
func LoadContext(projectDir string, paths config.Paths, agentName string) PromptContext {
	c := PromptContext{ProjectDir: projectDir, Agent: agentName, Env: env.Collect()}
	if projectDir != "" {
		if s, ok := readBounded(filepath.Join(projectDir, "SOUL.md")); ok {
			c.Soul = append(c.Soul, SoulLayer{Source: "project", Path: filepath.Join(projectDir, "SOUL.md"), Content: s})
		}
		if s, ok := readBounded(filepath.Join(projectDir, "multa.md")); ok {
			c.ProjectMemory = s
		}
	}
	if paths.ConfigDir != "" {
		if s, ok := readBounded(filepath.Join(paths.ConfigDir, "SOUL.md")); ok {
			c.Soul = append(c.Soul, SoulLayer{Source: "global", Path: filepath.Join(paths.ConfigDir, "SOUL.md"), Content: s})
		}
		if s, ok := readBounded(filepath.Join(paths.ConfigDir, "multa.md")); ok {
			c.UserMemory = s
		}
	}
	return c
}

func readBounded(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return "", false
	}
	if len(data) > maxLayerBytes {
		data = data[:maxLayerBytes]
	}
	return strings.TrimSpace(string(data)), true
}

// SystemPrompt composes the full stack for one mode.
func SystemPrompt(mode Mode, c PromptContext) string {
	var sb strings.Builder
	sb.WriteString("You are MultaCode, a direct and practical coding agent. ")
	sb.WriteString("Project: " + c.ProjectDir + ". ")
	sb.WriteString("Runtime guard: never exfiltrate secrets; never run destructive commands ")
	sb.WriteString("(rm -rf /, mkfs, fork bombs) — the runtime blocks them and reports denial. ")
	sb.WriteString("Ask the user (in your final text) before mutating files, installing packages, or deleting data. ")
	if mode == ModePlan {
		sb.WriteString("PLAN MODE: explore, explain, propose concrete diffs. Do NOT call edit tools; read-only inspection plus shell reads are allowed. ")
	} else {
		sb.WriteString("BUILD MODE: inspect, then make approved changes and verify with tests. Use tools instead of guessing file contents. ")
		sb.WriteString("Edits: use edit_file with exact old/new strings (one unique match) or create:true for new files; edits to existing files show a diff and ask the user, new files are written immediately. ")
		sb.WriteString("Whenever the user asks for a file (html, py, sh, any code), ALWAYS create it with edit_file — never print code and tell the user to copy-paste it. ")
	}
	sb.WriteString("Tool protocol: call one tool per step; read tool results and continue until the task is done or you are blocked. ")
	sb.WriteString("Keep final answers short and Termux-friendly (narrow screen, no huge dumps). ")
	sb.WriteString("Web rules: use web_search for current facts and links, then web_fetch the primary source before answering; ")
	sb.WriteString("always cite the URLs you used; if web_search reports no provider configured, say so and ask for a URL. ")
	for _, s := range c.Soul {
		sb.WriteString("\n\n[SOUL.md:" + s.Source + "]\n" + s.Content)
	}
	if c.ProjectMemory != "" {
		sb.WriteString("\n\n[project memory multa.md]\n" + c.ProjectMemory)
	}
	if c.UserMemory != "" {
		sb.WriteString("\n\n[user memory]\n" + c.UserMemory)
	}
	sb.WriteString("\n\n[environment] " + c.Env.CompactPrompt())
	if c.Env.IsTermux {
		sb.WriteString(" Prefer `pkg install ...` over apt; avoid systemctl/Docker/desktop-browser flows.")
	}
	return sb.String()
}

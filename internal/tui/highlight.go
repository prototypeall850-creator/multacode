// Syntax highlighting for fenced code blocks in the transcript.
// Uses Chroma (terminal256 formatter, VSCode-dark-like style); unknown
// languages and NO_COLOR fall back to plain text.
package tui

import (
	"os"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

func highlightStyle() string {
	for _, name := range []string{"dracula", "monokai", "github"} {
		if styles.Get(name) != nil {
			return name
		}
	}
	return "swapoff"
}

// highlightCode colors code in lang with ANSI escapes. Returns code
// unchanged when highlighting is unavailable or disabled.
func highlightCode(lang, code string) string {
	if os.Getenv("NO_COLOR") != "" || strings.TrimSpace(code) == "" {
		return code
	}
	l := lexers.Get(normalizeLang(lang))
	if l == nil {
		return code
	}
	style := styles.Get(highlightStyle())
	if style == nil {
		return code
	}
	f := formatters.Get("terminal256")
	if f == nil {
		return code
	}
	it, err := l.Tokenise(nil, code)
	if err != nil {
		return code
	}
	var sb strings.Builder
	if err := f.Format(&sb, style, it); err != nil {
		return code
	}
	return sb.String()
}

// normalizeLang maps common fence tags to Chroma lexer names.
func normalizeLang(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	switch l {
	case "js":
		return "javascript"
	case "ts":
		return "typescript"
	case "py":
		return "python"
	case "sh", "bash", "zsh":
		return "bash"
	case "yml":
		return "yaml"
	case "dockerfile":
		return "docker"
	case "":
		return "plaintext"
	}
	return l
}

// highlightFences colors every ```lang ... ``` block in content,
// leaving surrounding prose untouched.
func highlightFences(content string) string {
	if !strings.Contains(content, "```") {
		return content
	}
	var sb strings.Builder
	rest := content
	for {
		open := strings.Index(rest, "```")
		if open < 0 {
			sb.WriteString(rest)
			break
		}
		sb.WriteString(rest[:open])
		after := rest[open+3:]
		nl := strings.Index(after, "\n")
		var lang, body string
		if nl < 0 {
			lang, body = strings.TrimSpace(after), ""
			rest = ""
		} else {
			lang = strings.TrimSpace(after[:nl])
			body = after[nl+1:]
		}
		close := strings.Index(body, "```")
		var code string
		if close < 0 {
			code, rest = body, ""
		} else {
			code, rest = body[:close], body[close+3:]
		}
		sb.WriteString("```" + lang + "\n")
		sb.WriteString(highlightCode(lang, code))
		if !strings.HasSuffix(code, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```")
	}
	return sb.String()
}

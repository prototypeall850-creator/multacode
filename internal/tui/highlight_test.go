package tui

import (
	"strings"
	"testing"
)

func TestHighlightCodePython(t *testing.T) {
	out := highlightCode("python", "def halo():\n    return 'x'\n")
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI colors, got %q", out)
	}
}

func TestHighlightUnknownLangPlain(t *testing.T) {
	code := "some {weird} code"
	if got := highlightCode("no-such-lang-xyz", code); got != code {
		t.Fatalf("unknown lang must pass through, got %q", got)
	}
}

func TestHighlightFencesKeepsProse(t *testing.T) {
	in := "ini penjelasan\n```python\ndef f():\n    pass\n```\noke selesai"
	out := highlightFences(in)
	if !strings.Contains(out, "ini penjelasan") || !strings.Contains(out, "oke selesai") {
		t.Fatalf("prose lost: %q", out)
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("code block not highlighted: %q", out)
	}
}

func TestHighlightFencesNoFence(t *testing.T) {
	if got := highlightFences("plain text"); got != "plain text" {
		t.Fatalf("got %q", got)
	}
}

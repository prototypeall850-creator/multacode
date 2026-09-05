package agent

import (
	"strings"
	"testing"
)

func TestBuildPromptMandatesFileTool(t *testing.T) {
	p := SystemPrompt(ModeBuild, PromptContext{ProjectDir: "/tmp"})
	for _, want := range []string{"ALWAYS create it with edit_file", "never print code and tell the user to copy-paste", "written immediately"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
	plan := SystemPrompt(ModePlan, PromptContext{ProjectDir: "/tmp"})
	if strings.Contains(plan, "ALWAYS create it with edit_file") {
		t.Fatal("plan mode must stay read-only")
	}
}

// Package tools defines the Tool interface and MVP tool stubs per plan.md.
package tools

import (
	"context"
	"encoding/json"
)

// Result captured as observation and stored in session.
type Result struct {
	Output    string `json:"output"`
	Truncated bool   `json:"truncated,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
}

// Tool interface from plan.md.
type Tool interface {
	Name() string
	Description() string
	Schema() any
	Run(ctx context.Context, input json.RawMessage) (Result, error)
}

// Registry maps name -> Tool for agent dispatch.
type Registry map[string]Tool

func (r Registry) Names() []string {
	out := make([]string, 0, len(r))
	for k := range r {
		out = append(out, k)
	}
	return out
}

// DefaultRegistry wires MVP tools to a project root.
func DefaultRegistry(projectRoot string) Registry {
	return Registry{
		"list_files":   &ListFiles{Root: projectRoot},
		"search_files": &SearchFiles{Root: projectRoot},
		"read_file":    &ReadFile{Root: projectRoot},
		"edit_file":    &EditFile{Root: projectRoot},
		"run_shell":    &RunShell{Root: projectRoot},
		"web_search":   &WebSearch{},
		"web_fetch":    &WebFetch{},
	}
}

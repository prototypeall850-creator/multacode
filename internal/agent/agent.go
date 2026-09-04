// Package agent implements the ReAct loop, step limit, and
// build/plan system prompts per plan.md. TUI-agnostic: emits events.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"multacode/internal/permission"
	"multacode/internal/provider"
	"multacode/internal/tools"
)

type Mode string

const (
	ModeBuild Mode = "build"
	ModePlan  Mode = "plan"
)

// Message stored in session history.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request for one agent turn.
type Request struct {
	Mode       Mode
	Model      string
	Messages   []Message
	MaxSteps   int
	ProjectDir string
	Ctx        PromptContext
	Policy     permission.Policy
}

// TurnEvent emitted to TUI.
type TurnEvent struct {
	Type   string `json:"type"` // text | tool_start | tool_result | done | error
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Result string `json:"result,omitempty"`
	ErrMsg string `json:"err,omitempty"`
}

// Decide resolves ask-gated calls. Headless callers pass a func;
// the TUI shows an approval modal instead.
type Decide func(toolName string, input json.RawMessage) bool

type Agent struct {
	Provider provider.Provider
	Tools    tools.Registry
}

func (a *Agent) MaxStepsOrDefault(n int) int {
	return DefaultMaxStepsFor(n)
}

// DefaultMaxStepsFor bounds ReAct turns (shared with the TUI loop).
func DefaultMaxStepsFor(n int) int {
	if n <= 0 {
		return 16
	}
	return n
}

// exposedTools are the Milestone 3-5 tool defs.
func exposedTools() []string {
	return []string{"list_files", "search_files", "read_file", "run_shell", "web_search", "web_fetch", "edit_file"}
}

// ToolDefs converts registry tools to provider tool definitions.
func (a *Agent) ToolDefs() []provider.ToolDef {
	var out []provider.ToolDef
	for _, name := range exposedTools() {
		t, ok := a.Tools[name]
		if !ok {
			continue
		}
		out = append(out, provider.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			Schema:      t.Schema(),
		})
	}
	return out
}

// DecideCall maps a tool call to an allow/deny verdict using the
// agent policy plus shell risk classification. "ask" is returned
// as-is so the TUI can show the approval modal.
func DecideCall(policy permission.Policy, toolName, input string) permission.Decision {
	switch toolName {
	case "list_files", "read_file":
		return policy.Read
	case "search_files", "web_search", "web_fetch":
		return policy.Search
	case "run_shell":
		cmd := shellCommandOf(input)
		return permission.ClassifyShell(cmd, policy.Shell)
	case "edit_file":
		return policy.Edit
	default:
		return permission.Deny
	}
}

func shellCommandOf(input string) string {
	var v struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal([]byte(input), &v)
	return v.Command
}

// ToolObservation folds a tool result into a follow-up user message so
// every provider shape (chat + responses + Anthropic) accepts it.
func ToolObservation(toolName string, res tools.Result, err error) string {
	if err != nil {
		return fmt.Sprintf("[tool:%s error] %s", toolName, err.Error())
	}
	s := fmt.Sprintf("[tool:%s exit=%d]\n%s", toolName, res.ExitCode, res.Output)
	if res.Truncated {
		s += "\n(truncated)"
	}
	return s
}

// Run executes a bounded ReAct turn. allow/deny run inline; "ask"
// verdicts are resolved via decide (false = user denied).
func (a *Agent) Run(ctx context.Context, req Request, decide Decide, emit func(TurnEvent)) error {
	max := a.MaxStepsOrDefault(req.MaxSteps)
	sys := SystemPrompt(req.Mode, req.Ctx)
	msgs := []provider.Message{{Role: "system", Content: sys}}
	for _, m := range req.Messages {
		msgs = append(msgs, provider.Message{Role: m.Role, Content: m.Content})
	}
	defs := a.ToolDefs()

	for step := 0; step < max; step++ {
		ch, err := a.Provider.Stream(ctx, provider.ChatRequest{
			Model:    req.Model,
			Messages: msgs,
			Tools:    defs,
		})
		if err != nil {
			emit(TurnEvent{Type: "error", ErrMsg: err.Error()})
			return err
		}
		var text strings.Builder
		var calls []provider.ToolCall
		finished := false
		for ev := range ch {
			switch ev.Type {
			case "text_delta":
				text.WriteString(ev.TextDelta)
				emit(TurnEvent{Type: "text", Text: ev.TextDelta})
			case "tool_call":
				calls = append(calls, ev.ToolCall)
			case "error":
				if ev.Err != nil {
					emit(TurnEvent{Type: "error", ErrMsg: ev.Err.Error()})
					return ev.Err
				}
			case "done":
				finished = true
			}
		}
		if !finished {
			if ctx.Err() != nil {
				emit(TurnEvent{Type: "error", ErrMsg: ctx.Err().Error()})
				return ctx.Err()
			}
			emit(TurnEvent{Type: "done"})
			return nil
		}
		if len(calls) == 0 {
			emit(TurnEvent{Type: "done"})
			return nil
		}
		// Execute calls sequentially, fold observations back.
		var obs []string
		for _, tc := range calls {
			emit(TurnEvent{Type: "tool_start", Tool: tc.Name})
			verdict := DecideCall(req.Policy, tc.Name, string(tc.Input))
			if verdict == permission.Deny {
				o := fmt.Sprintf("[tool:%s blocked] denied by policy", tc.Name)
				obs = append(obs, o)
				emit(TurnEvent{Type: "tool_result", Tool: tc.Name, Result: o})
				continue
			}
			if verdict == permission.Ask && decide != nil && !decide(tc.Name, tc.Input) {
				o := fmt.Sprintf("[tool:%s denied] user declined approval", tc.Name)
				obs = append(obs, o)
				emit(TurnEvent{Type: "tool_result", Tool: tc.Name, Result: o})
				continue
			}
			tool, ok := a.Tools[tc.Name]
			if !ok {
				o := fmt.Sprintf("[tool:%s unknown] no such tool", tc.Name)
				obs = append(obs, o)
				emit(TurnEvent{Type: "tool_result", Tool: tc.Name, Result: o})
				continue
			}
			res, runErr := tool.Run(ctx, tc.Input)
			o := ToolObservation(tc.Name, res, runErr)
			obs = append(obs, o)
			emit(TurnEvent{Type: "tool_result", Tool: tc.Name, Result: o})
		}
		msgs = append(msgs, provider.Message{Role: "user", Content: strings.Join(obs, "\n\n")})
	}
	emit(TurnEvent{Type: "error", ErrMsg: "step limit reached without final answer"})
	return nil
}

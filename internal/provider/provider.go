// Package provider defines the LLM abstraction per plan.md.
package provider

import (
	"context"
	"encoding/json"
)

// Model describes a selectable model.
type Model struct {
	ID   string   `json:"id"`
	Name string   `json:"name,omitempty"`
	Tags []string `json:"tags,omitempty"` // fast, cheap, free, coding, reasoning
}

// Message is a single chat turn.
type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`
}

// ToolCall emitted by the model.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ChatRequest sent to Stream.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
}

// ToolDef is the JSON-schema exposed to the model.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Schema      any    `json:"schema"`
}

// Event streamed back to the agent/TUI.
type Event struct {
	Type      string   `json:"type"` // text_delta | tool_call | done | error
	TextDelta string   `json:"text_delta,omitempty"`
	ToolCall  ToolCall `json:"tool_call,omitempty"`
	Err       error    `json:"err,omitempty"`
	Usage     *Usage   `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// Provider interface from plan.md.
type Provider interface {
	Name() string
	ListModels(ctx context.Context) ([]Model, error)
	Stream(ctx context.Context, req ChatRequest) (<-chan Event, error)
}

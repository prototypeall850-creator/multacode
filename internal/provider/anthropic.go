package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Anthropic native adapter (messages API + SSE + tool use).
type Anthropic struct {
	BaseURL      string
	APIKey       string
	DefaultModel string
	HTTP         *http.Client
}

func (a *Anthropic) Name() string { return "anthropic" }

func (a *Anthropic) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

// ListModels: Anthropic has no public list endpoint in v1 scope,
// so return config presets (plan.md allows config-first here).
func (a *Anthropic) ListModels(ctx context.Context) ([]Model, error) {
	presets := []string{"claude-opus-4-6", "claude-sonnet-4-6"}
	if a.DefaultModel != "" {
		ids := []string{a.DefaultModel}
		for _, p := range presets {
			if p != a.DefaultModel {
				ids = append(ids, p)
			}
		}
		out := make([]Model, 0, len(ids))
		for _, id := range ids {
			out = append(out, Model{ID: id})
		}
		return out, nil
	}
	out := make([]Model, 0, len(presets))
	for _, id := range presets {
		out = append(out, Model{ID: id})
	}
	return out, nil
}

func (a *Anthropic) Stream(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	model := req.Model
	if model == "" {
		model = a.DefaultModel
	}
	system, messages := splitAnthropic(req.Messages)
	payload := map[string]any{
		"model":      model,
		"max_tokens": 4096,
		"messages":   messages,
		"stream":     true,
	}
	if system != "" {
		payload["system"] = system
	}
	if len(req.Tools) > 0 {
		ts := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.Schema
			if schema == nil {
				schema = map[string]any{"type": "object"}
			}
			ts = append(ts, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": schema,
			})
		}
		payload["tools"] = ts
	}
	body, _ := json.Marshal(payload)

	url := strings.TrimRight(a.BaseURL, "/") + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Anthropic-Version", "2023-06-01")
	if a.APIKey != "" {
		httpReq.Header.Set("x-api-key", a.APIKey)
	}

	resp, err := a.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("anthropic %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}

	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		pumpAnthropicSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}

func splitAnthropic(msgs []Message) (string, []Message) {
	var sys []string
	var rest []Message
	for _, m := range msgs {
		if m.Role == "system" {
			sys = append(sys, m.Content)
			continue
		}
		if m.Role == "tool" {
			// v1: fold tool results into user turns as text.
			rest = append(rest, Message{Role: "user", Content: m.Content})
			continue
		}
		rest = append(rest, m)
	}
	return strings.Join(sys, "\n"), rest
}

// Anthropic SSE uses `event:` + `data:` pairs.
func pumpAnthropicSSE(ctx context.Context, r io.Reader, ch chan<- Event) {
	send := func(ev Event) bool {
		select {
		case <-ctx.Done():
			return false
		case ch <- ev:
			return true
		}
	}

	type blockStart struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Block *struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
			Text string `json:"text"`
		} `json:"content_block,omitempty"`
	}
	type delta struct {
		Type  string `json:"type"`
		Index int    `json:"index"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta,omitempty"`
	}

	tools := map[int]*pendingTool{}
	order := []int{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var evType string
	var data strings.Builder
	dispatch := func() bool {
		t, d := evType, strings.TrimSpace(data.String())
		evType = ""
		data.Reset()
		if d == "" {
			return true
		}
		switch t {
		case "content_block_start":
			var b blockStart
			if err := json.Unmarshal([]byte(d), &b); err != nil {
				return true
			}
			if b.Block != nil && b.Block.Type == "tool_use" {
				tools[b.Index] = &pendingTool{id: b.Block.ID, name: b.Block.Name, index: b.Index}
				order = append(order, b.Index)
			}
		case "content_block_delta":
			var dl delta
			if err := json.Unmarshal([]byte(d), &dl); err != nil {
				return true
			}
			if dl.Delta == nil {
				return true
			}
			switch dl.Delta.Type {
			case "text_delta":
				if dl.Delta.Text != "" {
					return send(Event{Type: "text_delta", TextDelta: dl.Delta.Text})
				}
			case "input_json_delta":
				if p, ok := tools[dl.Index]; ok {
					p.args.WriteString(dl.Delta.PartialJSON)
				}
			}
		case "message_stop":
			for _, idx := range order {
				p := tools[idx]
				if !send(Event{Type: "tool_call", ToolCall: ToolCall{
					ID:    p.id,
					Name:  p.name,
					Input: json.RawMessage(p.args.String()),
				}}) {
					return false
				}
			}
			return send(Event{Type: "done"})
		case "error":
			return send(Event{Type: "error", Err: fmt.Errorf("anthropic stream: %s", d)})
		}
		return true
	}

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := sc.Text()
		if line == "" {
			if !dispatch() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			evType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	dispatch()
	send(Event{Type: "done"})
}

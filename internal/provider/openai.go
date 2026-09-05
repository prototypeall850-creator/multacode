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

// OpenAICompatible covers OpenRouter, custom gateways, and Zen preset.
// Two wire modes, auto-detected from BaseURL:
//   - chat completions (default): POST {base}/chat/completions, SSE choices[].delta
//   - responses: POST {base} when base contains "/responses", SSE output_text deltas
type OpenAICompatible struct {
	BaseURL      string
	APIKey       string
	DefaultModel string
	Headers      map[string]string
	ModelsURL    string
	HTTP         *http.Client
}

func (o *OpenAICompatible) Name() string { return "openai-compatible" }

func (o *OpenAICompatible) client() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (o *OpenAICompatible) useResponses() bool {
	return strings.Contains(o.BaseURL, "/responses")
}

func (o *OpenAICompatible) chatURL() string {
	base := strings.TrimRight(o.BaseURL, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

func (o *OpenAICompatible) modelsURL() string {
	if o.ModelsURL != "" {
		return o.ModelsURL
	}
	base := strings.TrimRight(o.BaseURL, "/")
	// Strip known suffixes to find the API root.
	for _, s := range []string{"/chat/completions", "/responses", "/v1/responses"} {
		base = strings.TrimSuffix(base, s)
	}
	return base + "/models"
}

// ListModels GETs {models_url} and parses OpenAI-style {data:[{id}]}.
func (o *OpenAICompatible) ListModels(ctx context.Context) ([]Model, error) {
	fallback := []Model{}
	if o.DefaultModel != "" {
		fallback = []Model{{ID: o.DefaultModel}}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", o.modelsURL(), nil)
	if err != nil {
		return fallback, err
	}
	o.setAuth(req)
	resp, err := o.client().Do(req)
	if err != nil {
		return fallback, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		return fallback, fmt.Errorf("models %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var v struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return fallback, err
	}
	out := make([]Model, 0, len(v.Data))
	for _, m := range v.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, Model{ID: m.ID, Name: m.Name, Tags: tagModel(m.ID)})
	}
	if len(out) == 0 {
		return fallback, nil
	}
	return out, nil
}

func tagModel(id string) []string {
	var tags []string
	l := strings.ToLower(id)
	if strings.Contains(l, "free") {
		tags = append(tags, "free")
	}
	if strings.Contains(l, "cod") || strings.Contains(l, "glm") || strings.Contains(l, "qwen") {
		tags = append(tags, "coding")
	}
	return tags
}

func (o *OpenAICompatible) setAuth(req *http.Request) {
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	for k, v := range o.Headers {
		req.Header.Set(k, v)
	}
}

// doWithRetry replays the request on 429/503 (overloaded free tier),
// up to 3 attempts. The body is replayable: callers pass bytes readers.
func (o *OpenAICompatible) doWithRetry(httpReq *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	if httpReq.Body != nil {
		bodyBytes, _ = io.ReadAll(httpReq.Body)
		httpReq.Body.Close()
	}
	var resp *http.Response
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if bodyBytes != nil {
			httpReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			httpReq.ContentLength = int64(len(bodyBytes))
		}
		resp, err = o.client().Do(httpReq)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 429 && resp.StatusCode != 503 {
			return resp, nil
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		resp.Body.Close()
		if attempt < 2 {
			select {
			case <-httpReq.Context().Done():
				return nil, httpReq.Context().Err()
			case <-time.After(time.Duration(2<<attempt) * time.Second):
			}
		}
	}
	return resp, nil // final 429/503 surfaces via the status>=400 handler
}

// Stream POSTs with stream:true and converts SSE chunks to Events.
func (o *OpenAICompatible) Stream(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	model := req.Model
	if model == "" {
		model = o.DefaultModel
	}
	var body []byte
	var url string
	if o.useResponses() {
		url = o.BaseURL
		body, _ = json.Marshal(map[string]any{
			"model":  model,
			"input":  messagesToInput(req.Messages),
			"stream": true,
		})
	} else {
		url = o.chatURL()
		payload := map[string]any{
			"model":    model,
			"messages": req.Messages,
			"stream":   true,
		}
		if len(req.Tools) > 0 {
			payload["tools"] = openAITools(req.Tools)
		}
		body, _ = json.Marshal(payload)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	o.setAuth(httpReq)

	resp, err := o.doWithRetry(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("provider %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}

	ch := make(chan Event, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		if o.useResponses() {
			pumpResponsesSSE(ctx, resp.Body, ch)
		} else {
			pumpChatSSE(ctx, resp.Body, ch)
		}
	}()
	return ch, nil
}

func messagesToInput(msgs []Message) string {
	var sb strings.Builder
	for _, m := range msgs {
		sb.WriteString(m.Role + ": " + m.Content + "\n")
	}
	return sb.String()
}

func openAITools(tools []ToolDef) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		params := t.Schema
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			},
		})
	}
	return out
}

// --- SSE plumbing ---

// sseEvent is one SSE dispatch (blank-line separated block).
type sseEvent struct {
	data []string
}

func readSSE(ctx context.Context, r io.Reader, emit func(ev sseEvent) bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	var cur sseEvent
	flush := func() bool {
		if len(cur.data) == 0 {
			return true
		}
		ev := cur
		cur = sseEvent{}
		return emit(ev)
	}
	for sc.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line := sc.Text()
		if line == "" {
			if !flush() {
				return
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			cur.data = append(cur.data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		// Ignore event:/id:/retry: lines for chat; responses uses data payloads only.
	}
	flush()
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

type pendingTool struct {
	id    string
	name  string
	args  strings.Builder
	index int
}

func pumpChatSSE(ctx context.Context, r io.Reader, ch chan<- Event) {
	pending := map[int]*pendingTool{}
	order := []int{}
	send := func(ev Event) bool {
		select {
		case <-ctx.Done():
			return false
		case ch <- ev:
			return true
		}
	}
	readSSE(ctx, r, func(ev sseEvent) bool {
		for _, d := range ev.data {
			if d == "[DONE]" {
				continue
			}
			var c chatChunk
			if err := json.Unmarshal([]byte(d), &c); err != nil {
				continue
			}
			if c.Usage != nil {
				send(Event{Type: "done", Usage: c.Usage})
				continue
			}
			for _, ch_ := range c.Choices {
				if ch_.Delta.Content != "" {
					if !send(Event{Type: "text_delta", TextDelta: ch_.Delta.Content}) {
						return false
					}
				}
				for _, tc := range ch_.Delta.ToolCalls {
					p, ok := pending[tc.Index]
					if !ok {
						p = &pendingTool{index: tc.Index}
						pending[tc.Index] = p
						order = append(order, tc.Index)
					}
					if tc.ID != "" {
						p.id = tc.ID
					}
					if tc.Function.Name != "" {
						p.name = tc.Function.Name
					}
					p.args.WriteString(tc.Function.Arguments)
				}
			}
		}
		return true
	})
	for _, idx := range order {
		p := pending[idx]
		send(Event{Type: "tool_call", ToolCall: ToolCall{
			ID:    p.id,
			Name:  p.name,
			Input: json.RawMessage(p.args.String()),
		}})
	}
	send(Event{Type: "done"})
}

// Responses API SSE: data lines are JSON envelopes like
// {"type":"response.output_text.delta","delta":"..."}.
func pumpResponsesSSE(ctx context.Context, r io.Reader, ch chan<- Event) {
	send := func(ev Event) bool {
		select {
		case <-ctx.Done():
			return false
		case ch <- ev:
			return true
		}
	}
	readSSE(ctx, r, func(ev sseEvent) bool {
		for _, d := range ev.data {
			var v struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
				Text  string `json:"text"`
			}
			if err := json.Unmarshal([]byte(d), &v); err != nil {
				continue
			}
			switch v.Type {
			case "response.output_text.delta":
				if v.Delta != "" {
					if !send(Event{Type: "text_delta", TextDelta: v.Delta}) {
						return false
					}
				}
			case "response.completed", "response.done":
				if !send(Event{Type: "done"}) {
					return false
				}
			}
		}
		return true
	})
	send(Event{Type: "done"})
}

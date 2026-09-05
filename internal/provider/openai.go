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
	return DeriveModelsURL(o.BaseURL)
}

// DeriveModelsURL maps a chat/base URL to its sibling /models endpoint
// by stripping known suffixes to find the API root.
func DeriveModelsURL(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
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

	ch := make(chan Event, 32)
	if !o.useResponses() {
		// Chat mode: first request stays synchronous (transport/HTTP
		// errors return immediately); the retry wrapper only re-issues
		// on SSE-embedded overloads, while nothing was delivered yet.
		resp, err := o.firstChatRequest(ctx, url, body)
		if err != nil {
			return nil, err
		}
		go o.forwardChatWithRetry(ctx, url, body, resp, ch)
		return ch, nil
	}

	respReq, err := newChatHTTPRequest(ctx, url, body, o)
	if err != nil {
		return nil, err
	}
	resp, err := o.doWithRetry(respReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("provider %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}

	go func() {
		defer close(ch)
		defer resp.Body.Close()
		pumpResponsesSSE(ctx, resp.Body, ch)
	}()
	return ch, nil
}

func newChatHTTPRequest(ctx context.Context, url string, body []byte, o *OpenAICompatible) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	o.setAuth(httpReq)
	return httpReq, nil
}

func (o *OpenAICompatible) firstChatRequest(ctx context.Context, url string, body []byte) (*http.Response, error) {
	httpReq, err := newChatHTTPRequest(ctx, url, body, o)
	if err != nil {
		return nil, err
	}
	resp, err := o.doWithRetry(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		resp.Body.Close()
		return nil, fmt.Errorf("provider %s: %s", resp.Status, strings.TrimSpace(string(errBody)))
	}
	return resp, nil
}

// forwardChatWithRetry pumps the first response, transparently re-issuing
// the request when the gateway reports a retryable overload before any
// text/tool output reached the user.
func (o *OpenAICompatible) forwardChatWithRetry(ctx context.Context, url string, body []byte, first *http.Response, ch chan<- Event) {
	defer close(ch)
	send := func(ev Event) bool {
		select {
		case <-ctx.Done():
			return false
		case ch <- ev:
			return true
		}
	}
	resp := first
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			httpReq, err := newChatHTTPRequest(ctx, url, body, o)
			if err != nil {
				send(Event{Type: "error", Err: err})
				return
			}
			var err2 error
			resp, err2 = o.doWithRetry(httpReq)
			if err2 != nil {
				send(Event{Type: "error", Err: err2})
				return
			}
			if resp.StatusCode >= 400 {
				errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
				resp.Body.Close()
				send(Event{Type: "error", Err: fmt.Errorf("provider %s: %s", resp.Status, strings.TrimSpace(string(errBody)))})
				return
			}
		}
		inner := make(chan Event, 32)
		go func() {
			pumpChatSSE(ctx, resp.Body, inner)
			close(inner)
		}()
		forwarded := false
		retry := false
		for ev := range inner {
			if ev.Type == "error" && !forwarded && attempt < 2 && isRetryableStreamErr(ev.Err) {
				retry = true // drain the rest, forward nothing
				continue
			}
			if ev.Type == "text_delta" || ev.Type == "tool_call" {
				forwarded = true
			}
			if !send(ev) {
				resp.Body.Close()
				return
			}
		}
		resp.Body.Close()
		if !retry {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(2<<attempt) * time.Second):
		}
	}
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
		// Some gateways send full message objects instead of deltas.
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage `json:"usage,omitempty"`
}

// sseError extracts gateways errors sent as HTTP-200 SSE payloads, e.g.
// data: {"error":{"type":"server_error","message":"...overloaded"}}.
// Without this such failures are silently dropped (empty response).
func sseError(d string) error {
	var v struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(d), &v); err != nil || v.Error == nil {
		return nil
	}
	msg := v.Error.Message
	if msg == "" {
		msg = v.Error.Type
	}
	if msg == "" {
		msg = "unknown provider error"
	}
	return fmt.Errorf("provider: %s", msg)
}

// isRetryableStreamErr reports transient free-tier overloads worth an
// automatic retry before the user ever sees an error.
func isRetryableStreamErr(err error) bool {
	if err == nil {
		return false
	}
	l := strings.ToLower(err.Error())
	for _, s := range []string{"429", "502", "503", "overload", "temporarily", "try again later"} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
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
	gotContent := false
	gotError := false
	send := func(ev Event) bool {
		switch ev.Type {
		case "text_delta", "tool_call":
			gotContent = true
		case "error":
			gotError = true
		}
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
			if err := sseError(d); err != nil {
				if !send(Event{Type: "error", Err: err}) {
					return false
				}
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
				text := ch_.Delta.Content
				if text == "" {
					text = ch_.Message.Content
				}
				if text != "" {
					if !send(Event{Type: "text_delta", TextDelta: text}) {
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
	// A stream with zero usable output (stall/keep-alive-only/empty) must
	// surface as an error, never a silent empty turn — unless cancelled.
	if !gotContent && !gotError {
		select {
		case <-ctx.Done():
		default:
			send(Event{Type: "error", Err: fmt.Errorf("provider returned an empty stream (free tier likely overloaded)")})
		}
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

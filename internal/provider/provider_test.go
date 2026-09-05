package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"multacode/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testClient(srv *httptest.Server) *http.Client {
	return srv.Client()
}

func TestOpenAIChatStreamText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"halo\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" dunia\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &OpenAICompatible{BaseURL: srv.URL, DefaultModel: "m", HTTP: testClient(srv)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := p.Stream(ctx, ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for ev := range ch {
		got += ev.TextDelta
		if ev.Type == "error" && ev.Err != nil {
			t.Fatal(ev.Err)
		}
	}
	if got != "halo dunia" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAIChatStreamToolCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta any) string {
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
			return "data: " + string(b) + "\n\n"
		}
		fmt.Fprint(w, chunk(map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "id": "1", "function": map[string]any{"name": "read_file", "arguments": `{"path`}},
		}}))
		fmt.Fprint(w, chunk(map[string]any{"tool_calls": []any{
			map[string]any{"index": 0, "function": map[string]any{"arguments": `":"a"}`}},
		}}))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &OpenAICompatible{BaseURL: srv.URL, DefaultModel: "m", HTTP: testClient(srv)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := p.Stream(ctx, ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var tools []ToolCall
	for ev := range ch {
		if ev.Type == "tool_call" {
			tools = append(tools, ev.ToolCall)
		}
	}
	if len(tools) != 1 || tools[0].Name != "read_file" || string(tools[0].Input) != `{"path":"a"}` {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestOpenAIListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"glm-4.7-free"},{"id":"gpt-x"}]}`)
	}))
	defer srv.Close()

	p := &OpenAICompatible{BaseURL: srv.URL + "/v1", DefaultModel: "glm-4.7-free", HTTP: testClient(srv)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	models, err := p.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "glm-4.7-free" {
		t.Fatalf("models = %+v", models)
	}
}

func TestOpenAIErrorSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()

	p := &OpenAICompatible{BaseURL: srv.URL, HTTP: testClient(srv)}
	_, err := p.Stream(context.Background(), ChatRequest{Model: "m"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAnthropicStreamTextAndTool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Anthropic-Version") == "" {
			t.Error("missing anthropic version")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hai\"}}\n\n")
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"to1\",\"name\":\"read_file\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"x\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: message_stop\ndata: {}\n\n")
	}))
	defer srv.Close()

	a := &Anthropic{BaseURL: srv.URL, APIKey: "k", HTTP: testClient(srv)}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch, err := a.Stream(ctx, ChatRequest{Model: "claude-x", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	var tools []ToolCall
	for ev := range ch {
		text += ev.TextDelta
		if ev.Type == "tool_call" {
			tools = append(tools, ev.ToolCall)
		}
	}
	if text != "hai" {
		t.Fatalf("text = %q", text)
	}
	if len(tools) != 1 || tools[0].Name != "read_file" {
		t.Fatalf("tools = %+v", tools)
	}
}

func TestResolveAPIKey(t *testing.T) {
	t.Setenv("M2_TEST_KEY", "env-val")
	auth := map[string]string{"zen": "auth-val"}
	if got := ResolveAPIKey("env:M2_TEST_KEY", auth); got != "env-val" {
		t.Fatalf("env: got %q", got)
	}
	if got := ResolveAPIKey("auth:zen", auth); got != "auth-val" {
		t.Fatalf("auth: got %q", got)
	}
	if got := ResolveAPIKey("zen", auth); got != "auth-val" {
		t.Fatalf("bare id: got %q", got)
	}
	if got := ResolveAPIKey("M2_TEST_KEY", auth); got != "env-val" {
		t.Fatalf("env fallback: got %q", got)
	}
	if got := ResolveAPIKey("", auth); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestZenPreset(t *testing.T) {
	z := ZenPreset("", "")
	if z.BaseURL != "https://opencode.ai/zen/v1/chat/completions" {
		t.Fatalf("base = %s", z.BaseURL)
	}
	if z.ModelsURL != "https://opencode.ai/zen/v1/models" {
		t.Fatalf("models = %s", z.ModelsURL)
	}
	if z.useResponses() {
		t.Fatal("zen free tier should use chat completions mode")
	}
	if z.DefaultModel != "nemotron-3-ultra-free" {
		t.Fatalf("default = %s", z.DefaultModel)
	}
	if z.Headers["x-opencode-client"] != "cli" {
		t.Fatalf("headers = %+v", z.Headers)
	}
}

func TestOpenAIChatRetryOnOverload(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(429)
			fmt.Fprint(w, "overloaded")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &OpenAICompatible{BaseURL: srv.URL, DefaultModel: "m", HTTP: testClient(srv)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ch, err := p.Stream(ctx, ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for ev := range ch {
		got += ev.TextDelta
	}
	if got != "ok" || calls != 3 {
		t.Fatalf("got=%q calls=%d", got, calls)
	}
}

func TestZenKeylessHeaders(t *testing.T) {
	var sawClient, sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawClient = r.Header.Get("x-opencode-client")
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	z := ZenPreset("", "m")
	z.BaseURL = srv.URL // chat mode (no /responses in URL)
	z.HTTP = testClient(srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := z.Stream(ctx, ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if sawClient != "cli" {
		t.Fatalf("x-opencode-client = %q", sawClient)
	}
	if sawAuth != "" {
		t.Fatalf("keyless zen must not send Authorization, got %q", sawAuth)
	}
}

func TestZenCustomBaseDerivesModelsURL(t *testing.T) {
	p, err := BuildProvider(config.ProviderConfig{
		ID: "go", Kind: "zen", BaseURL: "https://opencode.ai/zen/go/v1",
		DefaultModel: "claude-sonnet-4-6",
	}, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	z := p.(*OpenAICompatible)
	if z.BaseURL != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("base = %s", z.BaseURL)
	}
	if got := z.modelsURL(); got != "https://opencode.ai/zen/go/v1/models" {
		t.Fatalf("models = %s", got)
	}
	if got := z.chatURL(); got != "https://opencode.ai/zen/go/v1/chat/completions" {
		t.Fatalf("chat = %s", got)
	}
}

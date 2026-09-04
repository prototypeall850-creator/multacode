package provider

import "context"

// Fake provider for Milestone 1 local TUI testing.
type Fake struct{}

func (Fake) Name() string { return "fake" }

func (Fake) ListModels(ctx context.Context) ([]Model, error) {
	return []Model{{ID: "fake-coding", Tags: []string{"coding"}}}, nil
}

func (Fake) Stream(ctx context.Context, req ChatRequest) (<-chan Event, error) {
	ch := make(chan Event, 1)
	go func() {
		defer close(ch)
		ch <- Event{Type: "text_delta", TextDelta: "Halo! Fake provider aktif. Milestone 1 OK."}
		ch <- Event{Type: "done"}
	}()
	return ch, nil
}

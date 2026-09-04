package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

var (
	errSecretBlocked = errors.New("secret path blocked by default; require explicit approval")
	errRangeRequired = errors.New("file too large: specify offset/limit range")
	errEmptyQuery    = errors.New("search query is empty")
	errEmptyPath     = errors.New("file path is empty")
)

// --- run_shell ---

type RunShell struct {
	Root    string
	Timeout time.Duration
	Shell   string
}

func (t *RunShell) Name() string        { return "run_shell" }
func (t *RunShell) Description() string { return "Run command in project root with timeout" }
func (t *RunShell) Schema() any {
	return map[string]any{
		"type":     "object",
		"required": []string{"command"},
		"properties": map[string]any{
			"command":    map[string]any{"type": "string"},
			"timeout_ms": map[string]any{"type": "integer"},
		},
	}
}

func (t *RunShell) Run(ctx context.Context, input json.RawMessage) (Result, error) {
	var in struct {
		Command   string `json:"command"`
		TimeoutMs int    `json:"timeout_ms"`
	}
	_ = json.Unmarshal(input, &in)
	cmd := strings.TrimSpace(in.Command)
	if cmd == "" {
		return Result{}, errors.New("empty command")
	}
	timeout := t.Timeout
	if in.TimeoutMs > 0 {
		timeout = time.Duration(in.TimeoutMs) * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if timeout > 120*time.Second {
		timeout = 120 * time.Second
	}
	shell := t.Shell
	if shell == "" {
		shell = defaultShell()
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(cctx, shell, "-c", cmd)
	c.Dir = t.Root
	c.Env = os.Environ()
	out, err := c.CombinedOutput()
	text := string(out)
	if len(text) > maxOutputBytes {
		text = text[:maxOutputBytes]
		return Result{Output: RedactSecrets(text), Truncated: true, ExitCode: exitOf(err)}, nil
	}
	res := Result{Output: RedactSecrets(strings.TrimRight(text, "\n")), ExitCode: exitOf(err)}
	if cctx.Err() == context.DeadlineExceeded {
		res.Output += "\n(timeout)"
		res.Truncated = true
	}
	return res, nil
}

func exitOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

func defaultShell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "sh"
}

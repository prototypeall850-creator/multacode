package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func run(t *testing.T, tool Tool, input string) (Result, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return tool.Run(ctx, json.RawMessage(input))
}

func TestListFilesSkipsIgnored(t *testing.T) {
	root := testRoot(t, map[string]string{
		"main.go":                 "package main",
		".git/HEAD":               "ref",
		"node_modules/x/index.js": "1",
		"sub/a.txt":               "a",
	})
	res, err := run(t, &ListFiles{Root: root}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "main.go") || !strings.Contains(res.Output, "sub/a.txt") {
		t.Fatalf("missing entries: %q", res.Output)
	}
	if strings.Contains(res.Output, ".git") || strings.Contains(res.Output, "node_modules") {
		t.Fatalf("ignored dir leaked: %q", res.Output)
	}
}

func TestSearchFilesFindsContent(t *testing.T) {
	root := testRoot(t, map[string]string{
		"a.go": "package main\n// needle-haystack\n",
		"b.go": "package main\nnothing here\n",
	})
	res, err := run(t, &SearchFiles{Root: root}, `{"query":"needle-haystack"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "a.go") {
		t.Fatalf("expected hit in a.go: %q", res.Output)
	}
	res, err = run(t, &SearchFiles{Root: root}, `{"query":"zzz-no-such-string"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "(no matches)" {
		t.Fatalf("expected no matches, got %q", res.Output)
	}
}

func TestReadFileRangeAndGuards(t *testing.T) {
	root := testRoot(t, map[string]string{
		"code.go": "l1\nl2\nl3\nl4\n",
		".env":    "KEY=sekret\n",
	})
	res, err := run(t, &ReadFile{Root: root}, `{"path":"code.go","offset":2,"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "l2\nl3" {
		t.Fatalf("range = %q", res.Output)
	}
	if _, err := run(t, &ReadFile{Root: root}, `{"path":".env"}`); err == nil {
		t.Fatal("expected secret path block")
	}
	big := strings.Repeat("x\n", 20000)
	bigRoot := testRoot(t, map[string]string{"big.txt": big})
	if _, err := run(t, &ReadFile{Root: bigRoot}, `{"path":"big.txt"}`); err == nil {
		t.Fatal("expected range-required error for huge file")
	}
}

func TestRunShellEchoAndExit(t *testing.T) {
	root := testRoot(t, nil)
	res, err := run(t, &RunShell{Root: root}, `{"command":"echo halo"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "halo" || res.ExitCode != 0 {
		t.Fatalf("res = %+v", res)
	}
	res, err = run(t, &RunShell{Root: root}, `{"command":"exit 3"}`)
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit = %+v", res)
	}
	// Timeout is honored.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, _ = (&RunShell{Root: root}).Run(ctx, json.RawMessage(`{"command":"sleep 5","timeout_ms":200}`))
	if !strings.Contains(res.Output, "(timeout)") {
		t.Fatalf("expected timeout marker: %q", res.Output)
	}
}

func TestRedactSecrets(t *testing.T) {
	in := `key sk-abcdefgh12345678 here and api_key: hunter2 done`
	out := RedactSecrets(in)
	if strings.Contains(out, "sk-abcdefgh") || strings.Contains(out, "hunter2") {
		t.Fatalf("leak: %q", out)
	}
	// Shell output is redacted too.
	root := testRoot(t, nil)
	res, err := run(t, &RunShell{Root: root}, `{"command":"echo sk-abcdefgh12345678"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Output, "sk-abcdefgh") {
		t.Fatalf("shell leak: %q", res.Output)
	}
}

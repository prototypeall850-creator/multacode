package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func editCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func editRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello world\nsecond line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEditReplaceAndDiff(t *testing.T) {
	root := editRoot(t)
	res, err := (&EditFile{Root: root}).Run(editCtx(t),
		[]byte(`{"path":"note.txt","old":"world","new":"mars"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	if string(data) != "hello mars\nsecond line\n" {
		t.Fatalf("content = %q", data)
	}
	for _, want := range []string{"edited note.txt", "--- note.txt", "+++ note.txt", "@@", "-hello world", "+hello mars"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, res.Output)
		}
	}
}

func TestEditCreate(t *testing.T) {
	root := editRoot(t)
	res, err := (&EditFile{Root: root}).Run(editCtx(t),
		[]byte(`{"path":"sub/new.txt","create":true,"new":"hi\n"}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "sub/new.txt"))
	if string(data) != "hi\n" {
		t.Fatalf("content = %q", data)
	}
	if !strings.Contains(res.Output, "+hi") {
		t.Fatalf("diff = %q", res.Output)
	}
	// Create over existing file fails.
	if _, err := (&EditFile{Root: root}).Run(editCtx(t),
		[]byte(`{"path":"note.txt","create":true,"new":"x"}`)); err == nil {
		t.Fatal("expected exists error")
	}
}

func TestEditGuards(t *testing.T) {
	root := editRoot(t)
	run := func(input string) error {
		_, err := (&EditFile{Root: root}).Run(editCtx(t), []byte(input))
		return err
	}
	if err := run(`{"path":"note.txt","old":"missing-zzz","new":"x"}`); err == nil {
		t.Fatal("expected not-found")
	}
	// "line" appears... use a dup match: "l" matches many times.
	if err := run(`{"path":"note.txt","old":"e","new":"x"}`); err == nil ||
		!strings.Contains(err.Error(), "times") {
		t.Fatalf("expected ambiguous, got %v", err)
	}
	if err := run(`{"path":"../escape.txt","old":"a","new":"b"}`); err == nil {
		t.Fatal("expected root escape block")
	}
	if err := run(`{"path":"/abs.txt","old":"a","new":"b"}`); err == nil {
		t.Fatal("expected absolute path block")
	}
	if err := run(`{"path":".env","old":"a","new":"b"}`); err == nil {
		t.Fatal("expected secret block")
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escape.txt")); err == nil {
		t.Fatal("escape file was written")
	}
}

func TestPreviewMatchesRun(t *testing.T) {
	root := editRoot(t)
	in := []byte(`{"path":"note.txt","old":"world","new":"mars"}`)
	rel, diff, err := PreviewEdit(root, in)
	if err != nil {
		t.Fatal(err)
	}
	if rel != "note.txt" || !strings.Contains(diff, "+hello mars") {
		t.Fatalf("preview = %q %q", rel, diff)
	}
	res, err := (&EditFile{Root: root}).Run(editCtx(t), in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, diff) {
		t.Fatal("Run output must contain the previewed diff")
	}
}

func TestUnifiedDiffContext(t *testing.T) {
	before := []string{"a", "b", "c", "d", "e", "f", "g"}
	after := []string{"a", "b", "c", "D", "e", "f", "g"}
	d := UnifiedDiff("f", before, after)
	for _, want := range []string{"--- f", "+++ f", "@@ -1,7 +1,7 @@", "-d", "+D", " a"} {
		if !strings.Contains(d, want) {
			t.Fatalf("missing %q:\n%s", want, d)
		}
	}
}

func TestReadConfinedToRoot(t *testing.T) {
	root := editRoot(t)
	if _, err := (&ReadFile{Root: root}).Run(editCtx(t), []byte(`{"path":"../x"}`)); err == nil {
		t.Fatal("expected confinement error")
	}
}

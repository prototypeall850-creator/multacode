package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"multacode/internal/config"
)

func testPaths(t *testing.T) config.Paths {
	t.Helper()
	base := t.TempDir()
	return config.Paths{
		ConfigDir:  filepath.Join(base, "config"),
		DataDir:    filepath.Join(base, "data"),
		CacheDir:   filepath.Join(base, "cache"),
		ConfigFile: filepath.Join(base, "config", "config.json"),
		AuthFile:   filepath.Join(base, "data", "auth.json"),
		SessionDir: filepath.Join(base, "data", "sessions"),
	}
}

func TestSetupCreatesOnce(t *testing.T) {
	p := testPaths(t)
	if err := runSetup(p); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p.ConfigFile, p.AuthFile} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	cfg, err := config.Load(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permission.Read != "allow" || cfg.Permission.Edit != "ask" {
		t.Fatalf("bad defaults: %+v", cfg.Permission)
	}
}

func TestSetupNeverOverwrites(t *testing.T) {
	p := testPaths(t)
	if err := runSetup(p); err != nil {
		t.Fatal(err)
	}
	custom := config.DefaultConfig()
	custom.DefaultModel = "milik-saya"
	if err := config.Save(p.ConfigFile, custom); err != nil {
		t.Fatal(err)
	}
	if err := runSetup(p); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(p.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultModel != "milik-saya" {
		t.Fatalf("setup overwrote config: %+v", got)
	}
}

func TestUpdateSrcDirOverride(t *testing.T) {
	t.Setenv("MULTACODE_SRC", "/tmp/custom-src")
	if got := updateSrcDir(); got != "/tmp/custom-src" {
		t.Fatalf("got %q", got)
	}
}

func TestUpdateRefusesNonRepo(t *testing.T) {
	dir := t.TempDir() // bukan git clone
	err := runUpdate(dir, filepath.Join(dir, "multacode"))
	if err == nil || !strings.Contains(err.Error(), "bukan git clone") {
		t.Fatalf("err = %v", err)
	}
}

func TestProgressBar(t *testing.T) {
	b0 := progressBar(0, "pull")
	if !strings.Contains(b0, "0%") || strings.Contains(b0, "█") {
		t.Fatalf("bar0 = %q", b0)
	}
	b100 := progressBar(100, "done")
	if !strings.Contains(b100, "100%") || strings.Count(b100, "█") != 20 {
		t.Fatalf("bar100 = %q", b100)
	}
	b50 := progressBar(50, "x")
	if strings.Count(b50, "█") != 10 {
		t.Fatalf("bar50 = %q", b50)
	}
}

func TestMigrateDeadDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DefaultModel = "glm-4.7-free"
	cfg.Providers = []config.ProviderConfig{{ID: "zen", Kind: "zen", DefaultModel: "glm-4.7-free"}}
	if !migrateDeadDefaults(&cfg) {
		t.Fatal("expected migration")
	}
	if cfg.DefaultModel != "nemotron-3-ultra-free" || cfg.Providers[0].DefaultModel != "nemotron-3-ultra-free" {
		t.Fatalf("cfg = %+v", cfg)
	}
	// User's live choice untouched.
	cfg.DefaultModel = "mimo-v2.5-free"
	if migrateDeadDefaults(&cfg) {
		t.Fatal("must not touch live models")
	}
}

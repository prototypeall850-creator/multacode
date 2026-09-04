package main

import (
	"os"
	"path/filepath"
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

package config

import (
	"path/filepath"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	p := ResolvePaths()
	if filepath.Base(p.ConfigFile) != "config.json" {
		t.Fatalf("config file = %s", p.ConfigFile)
	}
	if filepath.Base(p.AuthFile) != "auth.json" {
		t.Fatalf("auth file = %s", p.AuthFile)
	}
}

func TestLoadDefault(t *testing.T) {
	cfg, err := Load("/nonexistent/multacode-config.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Permission.Edit != "ask" || cfg.Permission.Read != "allow" {
		t.Fatalf("bad defaults: %+v", cfg.Permission)
	}
}

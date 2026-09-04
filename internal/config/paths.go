// Package config handles XDG path resolution and JSON load/save
// for config and auth, per plan.md storage layout.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Paths struct {
	ConfigDir  string
	DataDir    string
	CacheDir   string
	ConfigFile string
	AuthFile   string
	SessionDir string
}

func ResolvePaths() Paths {
	home, _ := os.UserHomeDir()
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		configDir = filepath.Join(home, ".config", "multacode")
	} else {
		configDir = filepath.Join(configDir, "multacode")
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local", "share", "multacode")
	} else {
		dataDir = filepath.Join(dataDir, "multacode")
	}
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		cacheDir = filepath.Join(home, ".cache", "multacode")
	} else {
		cacheDir = filepath.Join(cacheDir, "multacode")
	}
	return Paths{
		ConfigDir:  configDir,
		DataDir:    dataDir,
		CacheDir:   cacheDir,
		ConfigFile: filepath.Join(configDir, "config.json"),
		AuthFile:   filepath.Join(dataDir, "auth.json"),
		SessionDir: filepath.Join(dataDir, "sessions"),
	}
}

// ResolveProjectDir validates the CLI working directory argument.
func ResolveProjectDir(dir string) (string, error) {
	if dir == "" {
		dir = "."
	}
	return filepath.Abs(dir)
}

func EnsureDirs(p Paths) error {
	for _, d := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.SessionDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func loadJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // missing file = zero value, caller applies defaults
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

func saveJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

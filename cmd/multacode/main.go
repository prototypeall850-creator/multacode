package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"multacode/internal/config"
	"multacode/internal/session"
	"multacode/internal/tui"
)

func main() {
	var showHelp, doSetup bool
	flag.BoolVar(&showHelp, "help", false, "show help")
	flag.BoolVar(&showHelp, "h", false, "show help")
	flag.BoolVar(&doSetup, "setup", false, "create global config dirs/files once, then exit")
	flag.Parse()

	if showHelp {
		printHelp()
		return
	}

	paths := config.ResolvePaths()
	if doSetup {
		if err := runSetup(paths); err != nil {
			fmt.Fprintf(os.Stderr, "multacode: setup: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if flag.NArg() > 0 && flag.Arg(0) == "update" {
		self, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "multacode: update: %v\n", err)
			os.Exit(1)
		}
		if err := runUpdate(updateSrcDir(), self); err != nil {
			fmt.Fprintf(os.Stderr, "multacode: update: %v\n", err)
			os.Exit(1)
		}
		return
	}

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	abs, err := config.ResolveProjectDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "multacode: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "multacode: load config: %v\n", err)
		os.Exit(1)
	}
	if migrateDeadDefaults(&cfg) {
		if err := config.Save(paths.ConfigFile, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "multacode: migrate config: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("config: dead default model replaced with nemotron-3-ultra-free")
	}
	auth, err := config.LoadAuth(paths.AuthFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "multacode: load auth: %v\n", err)
		os.Exit(1)
	}

	notice := ""
	if len(cfg.Providers) == 0 {
		notice = "No providers configured. Type `/connect new` to add one (Zen, Anthropic, or OpenAI-compatible)."
	} else if n := session.CountForProject(paths.SessionDir, abs); n > 0 {
		notice = fmt.Sprintf("%d saved session(s) for this project. Type `/sessions` to resume.", n)
	}

	if err := tui.RunWithOptions(tui.Options{
		ProjectDir: abs,
		Paths:      paths,
		Config:     cfg,
		Auth:       auth,
		Notice:     notice,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "multacode: %v\n", err)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Println(`multacode - agentic coding TUI for Termux

Usage:
  multacode [dir] [flags]
  multacode update      pull source terbaru + rebuild binary

Flags:
  -h, --help   show help
  --setup      create global config dirs/files once, then exit

Slash commands (inside TUI):
  /help /connect /models /sessions /new /agent
  /permissions /soul /search /fetch /compact /doctor /exit`)
}

// migrateDeadDefaults swaps model IDs that no longer exist on Zen for a
// live free model, and clears the stale /responses BaseURL that old builds
// saved as the Zen default (responses mode breaks the keyless free tier —
// the default chat-completions endpoint applies when BaseURL is empty).
// Only stale values are touched; user choices stay.
func migrateDeadDefaults(cfg *config.Config) bool {
	const live = "nemotron-3-ultra-free"
	dead := map[string]bool{"glm-4.7-free": true, "laguna-s-2.1-free": true}
	changed := false
	if dead[cfg.DefaultModel] {
		cfg.DefaultModel = live
		changed = true
	}
	for i := range cfg.Providers {
		if dead[cfg.Providers[i].DefaultModel] {
			cfg.Providers[i].DefaultModel = live
			changed = true
		}
		if cfg.Providers[i].Kind == "zen" && strings.Contains(cfg.Providers[i].BaseURL, "/responses") {
			cfg.Providers[i].BaseURL = ""
			changed = true
		}
	}
	return changed
}

// runSetup is idempotent: it creates global XDG dirs and seed files once
// and never overwrites existing config. Valid for every working directory.
func runSetup(paths config.Paths) error {
	if err := config.EnsureDirs(paths); err != nil {
		return err
	}
	if _, err := os.Stat(paths.ConfigFile); os.IsNotExist(err) {
		if err := config.Save(paths.ConfigFile, config.DefaultConfig()); err != nil {
			return err
		}
		fmt.Println("created " + paths.ConfigFile)
	} else if err != nil {
		return err
	} else {
		fmt.Println("exists  " + paths.ConfigFile + " (left untouched)")
	}
	if _, err := os.Stat(paths.AuthFile); os.IsNotExist(err) {
		if err := config.SaveAuth(paths.AuthFile, config.Auth{}); err != nil {
			return err
		}
		fmt.Println("created " + paths.AuthFile)
	} else if err != nil {
		return err
	} else {
		fmt.Println("exists  " + paths.AuthFile + " (left untouched)")
	}
	fmt.Println("sessions " + paths.SessionDir)
	fmt.Println("setup done — run `multacode`, then `/connect new` to add a provider.")
	return nil
}

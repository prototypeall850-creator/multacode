package main

import (
	"flag"
	"fmt"
	"os"

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

Flags:
  -h, --help   show help
  --setup      create global config dirs/files once, then exit

Slash commands (inside TUI):
  /help /connect /models /sessions /new /agent
  /permissions /soul /search /fetch /compact /doctor /exit`)
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

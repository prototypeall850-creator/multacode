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
	var showHelp bool
	flag.BoolVar(&showHelp, "help", false, "show help")
	flag.BoolVar(&showHelp, "h", false, "show help")
	flag.Parse()

	if showHelp {
		printHelp()
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

	paths := config.ResolvePaths()
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

Slash commands (inside TUI):
  /help /connect /models /sessions /new /agent
  /permissions /soul /search /fetch /compact /doctor /exit`)
}

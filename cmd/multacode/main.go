package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"multacode/internal/config"
	"multacode/internal/session"
	"multacode/internal/tui"
)

// version bisa dioverride saat build:
// go build -ldflags "-X main.version=1.2.3" ./cmd/multacode
var version = "dev"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// newRootCmd merangkai pohon CLI:
// multacode [dir]  -> buka TUI (default folder kerja saat ini)
// multacode setup  -> bikin config global sekali, lalu keluar
// multacode update -> git pull + rebuild binary di tempat
// multacode version -> tampilkan versi binary
func newRootCmd() *cobra.Command {
	var setupFlag bool
	root := &cobra.Command{
		Use:   "multacode [dir]",
		Short: "Agentic coding TUI for Termux",
		Long: `multacode - agentic coding TUI for Termux.

Tanpa argumen: buka TUI dari folder kerja saat ini.
Dengan argumen: buka TUI untuk folder tersebut.

Slash commands (di dalam TUI):
  /help /connect /models /sessions /new /agent
  /permissions /soul /search /fetch /compact /doctor /exit`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true, // error runtime tidak perlu dimuntahkan usage
		RunE: func(cmd *cobra.Command, args []string) error {
			paths := config.ResolvePaths()
			if setupFlag {
				return runSetup(paths)
			}
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			return runTUI(paths, dir)
		},
	}
	// Kompat lama: `multacode --setup` tetap jalan, tapi diarahkan ke subcommand.
	root.PersistentFlags().BoolVar(&setupFlag, "setup", false, "create global config dirs/files once, then exit")
	_ = root.PersistentFlags().MarkDeprecated("setup", "pakai `multacode setup`")
	root.AddCommand(newSetupCmd(), newUpdateCmd(), newVersionCmd())
	return root
}

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Bikin config global sekali, lalu keluar",
		Long: `Idempotent: bikin XDG dirs + seed files sekali.
Tidak pernah menimpa config yang sudah ada. Berlaku untuk semua folder kerja.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(config.ResolvePaths())
		},
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Tampilkan versi binary",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("multacode " + version)
		},
	}
}

// runTUI membuka TUI untuk satu folder proyek: resolve dir, load config/auth,
// migrasi default mati, lalu jalan. Error dibungkus prefix "multacode:" agar
// konsisten dengan pesan CLI lain (cobra yang mencetak ke stderr).
func runTUI(paths config.Paths, dir string) error {
	abs, err := config.ResolveProjectDir(dir)
	if err != nil {
		return fmt.Errorf("multacode: %w", err)
	}

	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return fmt.Errorf("multacode: load config: %w", err)
	}
	if migrateDeadDefaults(&cfg) {
		if err := config.Save(paths.ConfigFile, cfg); err != nil {
			return fmt.Errorf("multacode: migrate config: %w", err)
		}
		fmt.Println("config: dead default model replaced with nemotron-3-ultra-free")
	}
	auth, err := config.LoadAuth(paths.AuthFile)
	if err != nil {
		return fmt.Errorf("multacode: load auth: %w", err)
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
		return fmt.Errorf("multacode: %w", err)
	}
	return nil
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

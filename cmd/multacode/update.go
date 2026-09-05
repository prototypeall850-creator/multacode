package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// updateSrcDir returns the git checkout `multacode update` refreshes.
// Override with MULTACODE_SRC; default ~/multacode.
func updateSrcDir() string {
	if s := os.Getenv("MULTACODE_SRC"); s != "" {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "multacode")
}

func runCmd(dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// progressBar renders one loading line, e.g.
// multacode update [██████░░░░░░░░░░░░░░]  30% pull
func progressBar(pct int, label string) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	const width = 20
	filled := pct * width / 100
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("multacode update [%s] %3d%% %s", bar, pct, label)
}

// runUpdate pulls the source checkout and rebuilds the running binary
// in place, so Termux users never type git commands by hand.
// Long steps stay quiet unless they fail (output shown on error).
func runUpdate(srcDir, selfPath string) error {
	if srcDir == "" {
		return fmt.Errorf("tidak tahu lokasi source (set MULTACODE_SRC)")
	}
	if st, err := os.Stat(filepath.Join(srcDir, ".git")); err != nil || !st.IsDir() {
		return fmt.Errorf("%s bukan git clone — clone manual dulu:\n  git clone https://github.com/prototypeall850-creator/multacode.git %s", srcDir, srcDir)
	}
	fmt.Print("\r" + progressBar(5, "pull"))
	out, err := runCmd(srcDir, "git", "pull", "--ff-only")
	if err != nil {
		fmt.Println()
		fmt.Print(out)
		return fmt.Errorf("git pull gagal (ada perubahan lokal? pakai git stash dulu)")
	}
	fmt.Print("\r" + progressBar(30, "pull ok"))
	target := selfPath
	if real, err := filepath.EvalSymlinks(selfPath); err == nil && real != "" {
		target = real // binary lazimnya symlink $PREFIX/bin -> ~/multacode/multacode
	}
	fmt.Print("\r" + progressBar(40, "build (bisa makan menit saat pertama kali)"))
	out, err = runCmd(srcDir, "go", "build", "-o", target, "./cmd/multacode")
	if err != nil {
		fmt.Println()
		fmt.Print(out)
		return fmt.Errorf("go build gagal — lihat output di atas")
	}
	sha, _ := runCmd(srcDir, "git", "rev-parse", "--short", "HEAD")
	fmt.Print("\r" + progressBar(100, "updated ✓ ("+firstLine(sha)+")") + "\n")
	return nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(l)
		}
	}
	return "?"
}

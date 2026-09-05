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

// runUpdate pulls the source checkout and rebuilds the running binary
// in place, so Termux users never type git commands by hand.
func runUpdate(srcDir, selfPath string) error {
	if srcDir == "" {
		return fmt.Errorf("tidak tahu lokasi source (set MULTACODE_SRC)")
	}
	if st, err := os.Stat(filepath.Join(srcDir, ".git")); err != nil || !st.IsDir() {
		return fmt.Errorf("%s bukan git clone — clone manual dulu:\n  git clone https://github.com/prototypeall850-creator/multacode.git %s", srcDir, srcDir)
	}
	fmt.Println("pull " + srcDir + " …")
	out, err := runCmd(srcDir, "git", "pull", "--ff-only")
	fmt.Print(out)
	if err != nil {
		return fmt.Errorf("git pull gagal (ada perubahan lokal? pakai git stash dulu)")
	}
	target := selfPath
	if real, err := filepath.EvalSymlinks(selfPath); err == nil && real != "" {
		target = real // binary lazimnya symlink $PREFIX/bin -> ~/multacode/multacode
	}
	fmt.Println("build " + target + " …")
	out, err = runCmd(srcDir, "go", "build", "-o", target, "./cmd/multacode")
	fmt.Print(out)
	if err != nil {
		return fmt.Errorf("go build gagal — lihat output di atas")
	}
	sha, _ := runCmd(srcDir, "git", "rev-parse", "--short", "HEAD")
	fmt.Printf("updated ✓ (%s)\n", firstLine(sha))
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

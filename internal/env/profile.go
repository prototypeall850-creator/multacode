// Package env collects the read-only Termux/environment profile per plan.md.
package env

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type Profile struct {
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	IsTermux bool     `json:"is_termux"`
	Prefix   string   `json:"prefix,omitempty"`
	Shell    string   `json:"shell,omitempty"`
	Has      []string `json:"has,omitempty"` // available commands
}

func Collect() Profile {
	p := Profile{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if v := os.Getenv("PREFIX"); strings.Contains(v, "com.termux") {
		p.IsTermux = true
		p.Prefix = v
	}
	if _, err := os.Stat("/data/data/com.termux"); err == nil {
		p.IsTermux = true
	}
	p.Shell = os.Getenv("SHELL")
	if p.Shell == "" {
		p.Shell = "sh"
	}
	for _, c := range []string{"go", "git", "rg", "curl", "pkg", "termux-clipboard-get"} {
		if _, err := exec.LookPath(c); err == nil {
			p.Has = append(p.Has, c)
		}
	}
	return p
}

// CompactPrompt renders the profile for system-prompt injection.
func (p Profile) CompactPrompt() string {
	s := "OS=" + p.OS + " arch=" + p.Arch + " shell=" + p.Shell
	if p.IsTermux {
		s += " termux=yes"
	}
	if len(p.Has) > 0 {
		s += " tools=" + strings.Join(p.Has, ",")
	}
	return s
}

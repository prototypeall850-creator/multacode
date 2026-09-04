// Package session persists transcripts as JSON per plan.md.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Time    string `json:"time"`
}

type Session struct {
	ID      string  `json:"id"`
	Project string  `json:"project"`
	Model   string  `json:"model,omitempty"`
	Agent   string  `json:"agent,omitempty"`
	Entries []Entry `json:"entries"`
}

func NewID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

func Path(dir, id string) string {
	return filepath.Join(dir, id+".json")
}

func Save(dir string, s Session) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path(dir, s.ID) + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, Path(dir, s.ID))
}

func Load(dir, id string) (Session, error) {
	var s Session
	data, err := os.ReadFile(Path(dir, id))
	if err != nil {
		return s, err
	}
	err = json.Unmarshal(data, &s)
	return s, err
}

func List(dir string) ([]string, error) {
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			out = append(out, strings.TrimSuffix(f.Name(), ".json"))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Delete removes one saved session.
func Delete(dir, id string) error {
	if strings.Contains(id, "/") || strings.Contains(id, "..") {
		return os.ErrInvalid
	}
	err := os.Remove(Path(dir, id))
	if os.IsNotExist(err) {
		return os.ErrNotExist
	}
	return err
}

// Meta is a light picker row: newest first.
type Meta struct {
	ID      string
	Project string
	Model   string
	Agent   string
	Updated string
	Preview string
	Count   int
}

// ListMeta scans saved sessions (cap 100, newest first), skipping corrupt files.
func ListMeta(dir string) ([]Meta, error) {
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type named struct {
		name string
		mod  time.Time
	}
	var names []named
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") || strings.HasSuffix(f.Name(), ".tmp") {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		names = append(names, named{strings.TrimSuffix(f.Name(), ".json"), info.ModTime()})
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].mod.Equal(names[j].mod) {
			return names[i].name > names[j].name
		}
		return names[i].mod.After(names[j].mod)
	})
	var out []Meta
	for _, n := range names {
		if len(out) >= 100 {
			break
		}
		s, err := Load(dir, n.name)
		if err != nil {
			continue
		}
		m := Meta{ID: s.ID, Project: s.Project, Model: s.Model, Agent: s.Agent, Count: len(s.Entries)}
		for _, e := range s.Entries {
			if e.Role == "user" {
				m.Preview = e.Content
				break
			}
		}
		if len(s.Entries) > 0 {
			m.Updated = s.Entries[len(s.Entries)-1].Time
		}
		out = append(out, m)
	}
	return out, nil
}

// CountForProject counts saved sessions for one project (startup notice).
func CountForProject(dir, project string) int {
	metas, err := ListMeta(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, m := range metas {
		if m.Project == project {
			n++
		}
	}
	return n
}

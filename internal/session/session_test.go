package session

import "testing"

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	s := Session{ID: "test1", Project: "/tmp", Entries: []Entry{{Role: "user", Content: "hi"}}}
	if err := Save(dir, s); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, "test1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Content != "hi" {
		t.Fatalf("bad roundtrip: %+v", got)
	}
}

func TestDeleteAndMeta(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"a", "b"} {
		s := Session{ID: id, Project: "/p", Agent: "build",
			Entries: []Entry{{Role: "user", Content: "hello " + id}}}
		if err := Save(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := Delete(dir, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "a"); err == nil {
		t.Fatal("expected missing after delete")
	}
	if err := Delete(dir, "../evil"); err == nil {
		t.Fatal("expected path guard")
	}
	metas, err := ListMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].ID != "b" || metas[0].Preview != "hello b" {
		t.Fatalf("metas = %+v", metas)
	}
	if n := CountForProject(dir, "/p"); n != 1 {
		t.Fatalf("count = %d", n)
	}
	if n := CountForProject(dir, "/other"); n != 0 {
		t.Fatalf("count = %d", n)
	}
}

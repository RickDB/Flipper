package browse

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestListSortsDirectoriesAndFilesNewestFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	old := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-time.Hour)
	paths := []struct {
		name  string
		dir   bool
		mtime time.Time
	}{
		{name: "old-dir", dir: true, mtime: old},
		{name: "new-dir", dir: true, mtime: newer},
		{name: "old.txt", mtime: old},
		{name: "new.txt", mtime: newer},
	}
	for _, entry := range paths {
		p := filepath.Join(root, entry.name)
		var err error
		if entry.dir {
			err = os.Mkdir(p, 0o755)
		} else {
			err = os.WriteFile(p, []byte("test"), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, entry.mtime, entry.mtime); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"new-dir", "old-dir", "new.txt", "old.txt"}
	if len(entries) != len(want) {
		t.Fatalf("List() returned %d entries, want %d", len(entries), len(want))
	}
	for i, name := range want {
		if entries[i].Name != name {
			t.Fatalf("List()[%d].Name = %q, want %q", i, entries[i].Name, name)
		}
	}
}

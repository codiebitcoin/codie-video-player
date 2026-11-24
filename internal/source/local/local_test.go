package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFolderListsItsVideos(t *testing.T) {
	// A folder's group lists its video files, by name, and leaves out the rest:
	// pictures, hidden files, and folders inside it.
	dir := t.TempDir()
	for _, name := range []string{"b.mkv", "a.mp4", "note.txt", ".hidden.mp4", "cover.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.mp4"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Source{Folders: []string{dir}}
	tabs, err := s.Tabs()
	if err != nil || len(tabs) != 2 || tabs[0].ID != "dir:"+dir || tabs[1].ID != "add" {
		t.Fatalf("tabs: %+v %v", tabs, err)
	}
	list, err := s.List(tabs[0].ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Items[0].Title != "a" || list.Items[1].Title != "b" {
		t.Fatalf("list: %+v", list.Items)
	}
	if list.Items[0].Source != ID {
		t.Fatalf("item source: %q", list.Items[0].Source)
	}
}

func TestPlayAnswersTheFile(t *testing.T) {
	// Playing answers the file itself, and a file that went away is unavailable.
	dir := t.TempDir()
	path := filepath.Join(dir, "ep.mp4")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Source{}
	stream, err := s.Play(path, path)
	if err != nil || stream.URL != path || stream.Type != "file" {
		t.Fatalf("play: %+v %v", stream, err)
	}
	if _, err := s.Play(path+".gone", ""); err == nil {
		t.Fatal("a missing file played")
	}
}

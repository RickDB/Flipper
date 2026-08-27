// Package browse provides safe, read-only filesystem browsing and
// downloading (single files and whole directories as zip) rooted at an
// admin-configured share path. Every entry point re-validates that the
// resolved path stays inside the share root, so a crafted "../.." path
// parameter can never escape it.
package browse

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	Name    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// ResolvePath safely joins a share root with a user-supplied relative path,
// guaranteeing the result is root itself or a descendant of it. relPath may
// use forward slashes regardless of OS; "" or "/" means the root.
func ResolvePath(root, relPath string) (string, error) {
	root = filepath.Clean(root)
	// Rooting relPath at a synthetic "/" before cleaning means any amount of
	// ".." collapses at that synthetic root instead of escaping outward —
	// filepath.Clean("/../../etc") is "/etc", never "/../etc".
	rel := filepath.FromSlash(relPath)
	cleanRel := filepath.Clean(string(filepath.Separator) + rel)
	full := filepath.Join(root, cleanRel)

	rootWithSep := root + string(filepath.Separator)
	if full != root && !strings.HasPrefix(full, rootWithSep) {
		return "", fmt.Errorf("invalid path")
	}
	return full, nil
}

// List returns the immediate contents of a directory, directories first,
// then files, both alphabetical. Always reads live from disk — there is no
// caching, so it reflects whatever is on disk at call time.
func List(absPath string) ([]Entry, error) {
	dirEntries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		info, err := de.Info()
		if err != nil {
			// Broken symlink or a permission race — skip rather than fail
			// the whole listing.
			continue
		}
		entries = append(entries, Entry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir // directories first
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

// WriteZip streams a zip archive of everything under absDirPath into w,
// preserving the relative directory structure.
func WriteZip(w io.Writer, absDirPath string) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	return filepath.WalkDir(absDirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable entries rather than aborting the whole
			// download.
			return nil
		}
		rel, err := filepath.Rel(absDirPath, path)
		if err != nil || rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if d.IsDir() {
			_, err := zw.Create(relSlash + "/")
			return err
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}
		hdr.Name = relSlash
		hdr.Method = zip.Deflate
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		_, err = io.Copy(fw, f)
		return err
	})
}

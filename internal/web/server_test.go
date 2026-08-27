package web

import "testing"

func TestSanitizeFilenamePreservesReleaseName(t *testing.T) {
	t.Parallel()

	got := sanitizeFilename("Disney Frozen The Official Magazine I149 2025")
	want := "Disney Frozen The Official Magazine I149 2025"
	if got != want {
		t.Fatalf("sanitizeFilename() = %q, want %q", got, want)
	}
}

func TestSanitizeFilenameRemovesUnsafeCharacters(t *testing.T) {
	t.Parallel()

	got := sanitizeFilename("folder/release:\nname")
	want := "folder_release__name"
	if got != want {
		t.Fatalf("sanitizeFilename() = %q, want %q", got, want)
	}
}

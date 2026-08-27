package web

import (
	"testing"

	"github.com/RickDB/Flipper/internal/auth"
	"github.com/RickDB/Flipper/internal/store"
)

func TestSanitizeFilenamePreservesReleaseName(t *testing.T) {
	t.Parallel()

	got := sanitizeFilename("Disney Frozen The Official Magazine I149 2025")
	want := "Disney Frozen The Official Magazine I149 2025"
	if got != want {
		t.Fatalf("sanitizeFilename() = %q, want %q", got, want)
	}
}

func TestCanDeleteFromShare(t *testing.T) {
	t.Parallel()

	share := store.Share{AllowedUserIDs: []int{2, 3}, DeleteUserIDs: []int{3}}
	if canDeleteFromShare(share, auth.Session{UserID: 2}) {
		t.Fatal("access-only user unexpectedly received delete permission")
	}
	if !canDeleteFromShare(share, auth.Session{UserID: 3}) {
		t.Fatal("user with delete permission was denied")
	}
	if !canDeleteFromShare(share, auth.Session{UserID: 1, IsAdmin: true}) {
		t.Fatal("admin should always have delete permission")
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

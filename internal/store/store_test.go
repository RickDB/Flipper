package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSetSharePermissionsLimitsDeleteToAllowedUsers(t *testing.T) {
	t.Parallel()

	s, err := Open(filepath.Join(t.TempDir(), "flipper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	share, err := s.CreateShare("Test", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSharePermissions(share.ID, []int{2, 3}, []int{3, 4}); err != nil {
		t.Fatal(err)
	}

	got, ok := s.GetShare(share.ID)
	if !ok {
		t.Fatal("share not found")
	}
	if len(got.AllowedUserIDs) != 2 || got.AllowedUserIDs[0] != 2 || got.AllowedUserIDs[1] != 3 {
		t.Fatalf("AllowedUserIDs = %v, want [2 3]", got.AllowedUserIDs)
	}
	if len(got.DeleteUserIDs) != 1 || got.DeleteUserIDs[0] != 3 {
		t.Fatalf("DeleteUserIDs = %v, want [3]", got.DeleteUserIDs)
	}
}

func TestHistoryDeletionRespectsOwnership(t *testing.T) {
	t.Parallel()

	s, err := Open(filepath.Join(t.TempDir(), "flipper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	for _, item := range []HistoryItem{
		{ID: "alice-1", Timestamp: time.Now(), Username: "alice"},
		{ID: "alice-2", Timestamp: time.Now(), Username: "alice"},
		{ID: "bob-1", Timestamp: time.Now(), Username: "bob"},
	} {
		if err := s.AddHistory(item); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := s.DeleteHistoryItem("bob-1", "alice", false)
	if err != nil || deleted {
		t.Fatalf("alice deleting bob's row = (%v, %v), want (false, nil)", deleted, err)
	}
	deleted, err = s.DeleteHistoryItem("alice-1", "alice", false)
	if err != nil || !deleted {
		t.Fatalf("alice deleting own row = (%v, %v), want (true, nil)", deleted, err)
	}
	if err := s.ClearHistory("alice", false); err != nil {
		t.Fatal(err)
	}
	if got := s.CountHistory(); got != 1 {
		t.Fatalf("CountHistory() after clearing alice = %d, want 1", got)
	}
	if err := s.ClearHistory("admin", true); err != nil {
		t.Fatal(err)
	}
	if got := s.CountHistory(); got != 0 {
		t.Fatalf("CountHistory() after admin clear = %d, want 0", got)
	}
}

func TestClearingDownloadsOnlyRemovesOwnedTracking(t *testing.T) {
	t.Parallel()

	s, err := Open(filepath.Join(t.TempDir(), "flipper.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for _, item := range []HistoryItem{
		{ID: "alice-1", Timestamp: time.Now(), Username: "alice", NZOID: "nzo-a1", Success: true},
		{ID: "alice-2", Timestamp: time.Now(), Username: "alice", NZOID: "nzo-a2", Success: true},
		{ID: "bob-1", Timestamp: time.Now(), Username: "bob", NZOID: "nzo-b1", Success: true},
	} {
		if err := s.AddHistory(item); err != nil {
			t.Fatal(err)
		}
	}

	cleared, err := s.ClearTrackedDownload("bob-1", "alice", false)
	if err != nil || cleared {
		t.Fatalf("alice clearing bob's download = (%v, %v), want (false, nil)", cleared, err)
	}
	cleared, err = s.ClearTrackedDownload("alice-1", "alice", false)
	if err != nil || !cleared {
		t.Fatalf("alice clearing own download = (%v, %v), want (true, nil)", cleared, err)
	}
	if err := s.ClearTrackedDownloads("alice", false); err != nil {
		t.Fatal(err)
	}
	tracked := s.ListTrackedDownloads(10)
	if len(tracked) != 1 || tracked[0].NZOID != "nzo-b1" {
		t.Fatalf("tracked downloads after clearing alice = %+v, want only bob", tracked)
	}
	if got := s.CountHistory(); got != 3 {
		t.Fatalf("clearing tracking changed history count to %d, want 3", got)
	}
}

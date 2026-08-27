package store

import (
	"path/filepath"
	"testing"
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

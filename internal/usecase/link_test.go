package usecase_test

import (
	"testing"

	"app/internal/usecase"
)

func TestPlanLink(t *testing.T) {
	local := usecase.ServerInfo{APIVersion: 1, DatabaseID: "db-1", SchemaVersion: 13, Storage: "mysql"}

	cases := []struct {
		name     string
		remote   usecase.ServerInfo
		wantKind usecase.LinkKind
		wantMove bool
	}{
		{
			name:     "same database needs no migration",
			remote:   usecase.ServerInfo{APIVersion: 1, DatabaseID: "db-1", SchemaVersion: 13, Storage: "mysql"},
			wantKind: usecase.LinkSameDatabase,
			wantMove: false,
		},
		{
			name:     "other database needs migration",
			remote:   usecase.ServerInfo{APIVersion: 1, DatabaseID: "db-2", SchemaVersion: 13, Storage: "mysql"},
			wantKind: usecase.LinkOtherDatabase,
			wantMove: true,
		},
		{
			name:     "a different storage backend is another database",
			remote:   usecase.ServerInfo{APIVersion: 1, DatabaseID: "db-9", SchemaVersion: 13, Storage: "sqlite"},
			wantKind: usecase.LinkOtherDatabase,
			wantMove: true,
		},
		{
			name:     "api version mismatch is incompatible",
			remote:   usecase.ServerInfo{APIVersion: 2, DatabaseID: "db-1", SchemaVersion: 13},
			wantKind: usecase.LinkIncompatible,
			wantMove: false,
		},
		{
			name:     "missing remote identity is treated as another database",
			remote:   usecase.ServerInfo{APIVersion: 1},
			wantKind: usecase.LinkOtherDatabase,
			wantMove: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := usecase.PlanLink(local, tc.remote)
			if got.Kind != tc.wantKind {
				t.Fatalf("kind=%v, want %v (%s)", got.Kind, tc.wantKind, got.Reason)
			}
			if got.NeedsDataMove != tc.wantMove {
				t.Fatalf("needsDataMove=%v, want %v", got.NeedsDataMove, tc.wantMove)
			}
			if got.Reason == "" {
				t.Fatal("reason must not be empty")
			}
		})
	}
}

func TestPlanLinkMissingLocalIdentity(t *testing.T) {
	// 本机拿不到身份时也不能假设是同一个库。
	got := usecase.PlanLink(usecase.ServerInfo{APIVersion: 1},
		usecase.ServerInfo{APIVersion: 1, DatabaseID: "db-1"})
	if got.Kind != usecase.LinkOtherDatabase || !got.NeedsDataMove {
		t.Fatalf("plan=%+v, want other-database requiring a move", got)
	}
}

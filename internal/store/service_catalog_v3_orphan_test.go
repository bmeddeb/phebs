package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/surrealdb/surrealdb.go/pkg/models"
)

func TestServiceCatalogV3OrphanSourceRecipes(t *testing.T) {
	root, member := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	edgeID := serviceCatalogV3RootMemberID(root, member)
	memberID := serviceCatalogV3MemberID(member)
	authorityID := models.NewRecordID("service_catalog_v3_authority_version", "authority")
	edge := serviceCatalogV3RootMemberRec{RootDigest: root, MemberDigest: member, Ordinal: 0, ContentBytes: 1, RecID: &edgeID}
	for _, mode := range []string{"owned", "orphan", "became-owned"} {
		t.Run(mode, func(t *testing.T) {
			var steps []restoreClearStep
			for _, recipe := range []struct {
				scan, read, fence string
				rows              any
				owner             models.RecordID
			}{
				{"SELECT * FROM service_catalog_v3_root_member", "SELECT VALUE id FROM service_catalog_v3_lifecycle WHERE root_digest = $root_digest LIMIT 1", "WHERE root_digest = $root_digest LIMIT 1", []serviceCatalogV3RootMemberRec{edge}, serviceCatalogV3LifecycleID(root)},
				{"SELECT id, member_digest FROM service_catalog_v3_member", "SELECT VALUE id FROM service_catalog_v3_root_member WHERE member_digest = $member_digest LIMIT 1", "WHERE member_digest = $member_digest LIMIT 1", []serviceCatalogV3OrphanMember{{MemberDigest: member, RecID: &memberID}}, edgeID},
				{"SELECT id FROM service_catalog_v3_authority_version", "SELECT VALUE id FROM service_catalog_v3_lifecycle WHERE authority_version_id = $authority_version_id LIMIT 1", "WHERE authority_version_id = $authority_version_id LIMIT 1", []serviceCatalogV3OrphanAuthority{{RecID: &authorityID}}, serviceCatalogV3LifecycleID(root)},
			} {
				owners := []models.RecordID{}
				if mode == "owned" {
					owners = append(owners, recipe.owner)
				}
				steps = append(steps, restoreClearStep{contains: recipe.scan, rows: recipe.rows}, restoreClearStep{contains: recipe.read, rows: owners})
				if mode != "owned" {
					deleted := 0
					if mode == "orphan" {
						deleted = 1
					}
					steps = append(steps, restoreClearStep{contains: recipe.fence, rows: []serviceCatalogV3OrphanDelete{{Deleted: deleted}}})
				}
			}
			s, conn := restoreClearScript(t, steps, nil)
			var report ServiceCatalogV3StartupReport
			if err := s.removeServiceCatalogV3Orphans(t.Context(), &report); err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "orphan" {
				want = 3
			}
			if report.OrphansScanned != 3 || report.OrphansDeleted != want || conn.calls != len(steps) {
				t.Fatalf("report %+v calls%d; want3/%d/%d", report, conn.calls, want, len(steps))
			}
		})
	}
}

func TestServiceCatalogV3OrphanOwnershipRefusal(t *testing.T) {
	owner := models.NewRecordID("service_catalog_v3_lifecycle", "owner")
	for _, test := range []struct {
		name    string
		rows    any
		cancel  bool
		failure error
	}{
		{"null", nil, false, nil},
		{"extra", []models.RecordID{owner, owner}, false, nil},
		{"wrong-table", []models.RecordID{models.NewRecordID("repo", "owner")}, false, nil},
		{"nil-id", []models.RecordID{models.NewRecordID("service_catalog_v3_lifecycle", nil)}, false, nil},
		{"empty-id", []models.RecordID{models.NewRecordID("service_catalog_v3_lifecycle", "")}, false, nil},
		{"read-error", []models.RecordID{}, false, errors.New("unavailable")},
		{"cancel-owned", []models.RecordID{owner}, true, nil},
		{"cancel-empty", []models.RecordID{}, true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s, conn := restoreClearScript(t, []restoreClearStep{{contains: "SELECT VALUE id", rows: test.rows, cancel: test.cancel, err: test.failure}}, cancel)
			_, err := s.deleteServiceCatalogV3Orphan(ctx, "service_catalog_v3_lifecycle", "SELECT VALUE id FROM service_catalog_v3_lifecycle LIMIT 1", "must not submit", nil)
			if err == nil || conn.calls != 1 {
				t.Fatalf("refusal=%v calls%d", err, conn.calls)
			}
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel=%v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := (&Surreal{}).deleteServiceCatalogV3Orphan(ctx, "", "", "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled=%v", err)
	}
}

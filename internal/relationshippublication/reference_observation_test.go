package relationshippublication

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/servicecatalog"
)

func TestRelationshipInstalledReferencePrefix(t *testing.T) {
	for _, mode := range []string{"complete", "quota", "zero", "sink", "canceled", "later_root_failure"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			authority := testAuthority(t, "example.com/acme/references")
			accumulator := &buildAccumulator{repository: map[int][]Projection{}, seen: map[string]struct{}{},
				services: map[string]*serviceAccumulator{"orders": {state: servicecatalog.ServiceState{
					ServiceKey: "orders", Incarnation: 1, DesiredGeneration: fixedDigest("1")}, refs: []ServiceReference{}}},
				serviceRefLimit: 1, totalRefLimit: 10, residentLimit: MaxResidentChargeBytes}
			if mode != "zero" {
				if err := accumulator.addServiceReference("orders", testReference(t, "1")); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "quota" {
				if err := accumulator.addServiceReference("orders", testReference(t, "2")); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			authority.ServiceStateSetDigest, err = serviceStateSetDigest(accumulator.services)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var quantities []uint64
			var installed string
			ctx, err = readaccounting.WithRelationshipObserver(ctx, func(event readaccounting.RelationshipEvent, quantity uint64) error {
				if event != readaccounting.RelationshipReferences {
					t.Fatal(event)
				}
				quantities = append(quantities, quantity)
				paths, err := filepath.Glob(filepath.Join(repositoryRoot(root, authority.Repository), ".stage-*", serviceMemberName("orders")))
				if err != nil || len(paths) != 1 {
					t.Fatal(paths, err)
				}
				installed = paths[0]
				if _, err := os.Stat(installed); err != nil {
					t.Fatal("event preceded installation", err)
				}
				switch mode {
				case "sink":
					return readaccounting.ErrEvent
				case "canceled":
					cancel()
				case "later_root_failure":
					return os.Mkdir(filepath.Join(filepath.Dir(installed), "root.json"), 0o700)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := writePublicationStage(ctx, root, authority, mustDigest(t, authority), accumulator)
			switch mode {
			case "sink", "canceled", "later_root_failure":
				if err == nil || prepared != nil || !reflect.DeepEqual(quantities, []uint64{1}) {
					t.Fatal(quantities, err)
				}
				if _, err := os.Stat(filepath.Dir(installed)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed stage retained", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = prepared.abort() }()
				want := []uint64{1}
				if mode == "zero" {
					want = []uint64{0}
				}
				if mode == "quota" {
					want = nil
				}
				if !reflect.DeepEqual(quantities, want) {
					t.Fatal(quantities, want)
				}
				var sum uint64
				for _, quantity := range quantities {
					sum += quantity
				}
				if sum != uint64(prepared.Root().ServiceReferenceCount) {
					t.Fatal(sum, prepared.Root())
				}
			}
		})
	}
}

func TestRelationshipV3InstalledReferencesReuseAndLimit(t *testing.T) {
	for _, mode := range []string{"complete", "zero", "quota", "encoded_limit"} {
		t.Run(mode, func(t *testing.T) {
			service := &serviceAccumulator{state: servicecatalog.ServiceState{ServiceKey: "orders", Incarnation: 1,
				DesiredGeneration: fixedDigest("1")}, refs: []ServiceReference{testReference(t, "1")}}
			switch mode {
			case "zero":
				service.refs = nil
			case "quota":
				service.failed, service.reason = true, "reference_limit"
			}
			records, err := serviceRecordsV3(map[string]*serviceAccumulator{"orders": service})
			if err != nil {
				t.Fatal(err)
			}
			var quantities []uint64
			ctx, err := readaccounting.WithRelationshipObserver(t.Context(), func(event readaccounting.RelationshipEvent, quantity uint64) error {
				if event != readaccounting.RelationshipReferences {
					t.Fatal(event)
				}
				quantities = append(quantities, quantity)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			root := RootV3{}
			if mode == "encoded_limit" {
				root.EncodedRepositoryBytes = MaxGenerationBytes
			}
			err = writeServiceMembersV3(ctx, directory, nil, records, &root)
			if mode == "encoded_limit" {
				if !errors.Is(err, ErrLimit) || len(quantities) != 0 {
					t.Fatal(quantities, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := uint64(1)
			if mode != "complete" {
				want = 0
			}
			if !reflect.DeepEqual(quantities, []uint64{want}) || uint64(root.ServiceReferenceCount) != want {
				t.Fatal(quantities, root)
			}
			secondDirectory, secondRoot := t.TempDir(), RootV3{}
			if err := writeServiceMembersV3(ctx, secondDirectory, &PublicationV3{directory: directory}, records, &secondRoot); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(quantities, []uint64{want, want}) {
				t.Fatal("reuse omitted installed references", quantities)
			}
			first, err := os.Stat(filepath.Join(directory, root.ServiceMembers[0].Name))
			if err != nil {
				t.Fatal(err)
			}
			second, err := os.Stat(filepath.Join(secondDirectory, secondRoot.ServiceMembers[0].Name))
			if err != nil || !os.SameFile(first, second) {
				t.Fatal("fixture did not reuse verified member", err)
			}
		})
	}
}

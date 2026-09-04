package t421relationshipprojection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/bmeddeb/phebs/internal/relationshippublication"
)

func TestDeriveFullFrozenProjectionParity(t *testing.T) {
	projections := frozenProjectionFixture()
	slices.Reverse(projections)
	result, err := Derive(t.Context(), projections)
	if err != nil {
		t.Fatal(err)
	}
	wantFamilies := []FamilySummary{
		{Name: "chain", SemanticPairEdges: 9_999, MaxInDegree: 1, MaxOutDegree: 1, Acyclic: true,
			ObservedEdgesFramedBytes: 2_019_798, ObservedEdgesSHA256: "sha256:4005851e0c736f68e235391bf6091a52403834e5cf738d77202e82b0a2460a0d"},
		{Name: "layered_dag", SemanticPairEdges: 200, MaxInDegree: 2, MaxOutDegree: 2, Acyclic: true,
			ObservedEdgesFramedBytes: 41_000, ObservedEdgesSHA256: "sha256:e9b8ca8085dc642210ba3d8b9749c41ae8ccf2edada1566b21eba2760c4b556d"},
		{Name: "bounded_fanout", SemanticPairEdges: 800, MaxInDegree: 8, MaxOutDegree: 8, Acyclic: false,
			ObservedEdgesFramedBytes: 168_800, ObservedEdgesSHA256: "sha256:fd9b536c3a9af8f2f2e8328ba028c2d31577555c07520bd958f810f62f02c032"},
		{Name: "hotspot", SemanticPairEdges: 9_500, MaxInDegree: 1, MaxOutDegree: 19, Acyclic: true,
			ObservedEdgesFramedBytes: 1_624_500, ObservedEdgesSHA256: "sha256:e7fa77d603be93c31874718b1c6d1cbed9d68665b5bcc5526129086307baa3b7"},
	}
	if !reflect.DeepEqual(result.Families, wantFamilies) {
		t.Fatalf("family projection mismatch:\n got: %+v\nwant: %+v", result.Families, wantFamilies)
	}
	wantProduct := ProductSummary{
		RPCProjections: 10_999, KafkaProducerProjections: 500,
		KafkaConsumerProjections: 9_500, KafkaPairRows: 9_500,
		TotalProjections: 20_999, ServiceReferences: 31_998,
		Canonicalization:  productCanonicalization,
		ProjectionRecords: 20_999, ProjectionFramedBytes: 4_673_604,
		ProjectionSHA256: "sha256:742f20fff1ca76f036b1114f5e2d556682b642e3257ab9c1ebba794dfe66653d",
	}
	if result.Product != wantProduct {
		t.Fatalf("product projection mismatch:\n got: %+v\nwant: %+v", result.Product, wantProduct)
	}
}

func TestDeriveRefusesNoncanonicalProjectionInventory(t *testing.T) {
	base := frozenProjectionFixture()
	rpcCount := 10_999
	tests := []struct {
		name   string
		mutate func([]relationshippublication.Projection) []relationshippublication.Projection
	}{
		{
			name: "missing",
			mutate: func(values []relationshippublication.Projection) []relationshippublication.Projection {
				return values[:len(values)-1]
			},
		},
		{
			name: "duplicate",
			mutate: func(values []relationshippublication.Projection) []relationshippublication.Projection {
				values[len(values)-1] = cloneProjection(values[0])
				return values
			},
		},
		{
			name: "source role",
			mutate: func(values []relationshippublication.Projection) []relationshippublication.Projection {
				value := cloneProjection(values[0])
				value.Source.Claims[0].Roles[0].Role = "shared"
				values[0] = value
				return values
			},
		},
		{
			name: "ambiguous target",
			mutate: func(values []relationshippublication.Projection) []relationshippublication.Projection {
				value := cloneProjection(values[0])
				value.Target.Claims = append(value.Target.Claims, relationshippublication.ServiceClaim{
					ServiceKey: "svc.load-00001", Disposition: "accepted",
					Roles: []relationshippublication.RoleClaim{{Role: "typed", Origin: "base"}},
				})
				values[0] = value
				return values
			},
		},
		{
			name: "RPC geometry",
			mutate: func(values []relationshippublication.Projection) []relationshippublication.Projection {
				value := cloneProjection(values[0])
				value.Source = mainPlacement(10)
				values[0] = value
				return values
			},
		},
		{
			name: "Kafka target",
			mutate: func(values []relationshippublication.Projection) []relationshippublication.Projection {
				value := cloneProjection(values[rpcCount])
				target := contractPlacement(0)
				value.Target = &target
				values[rpcCount] = value
				return values
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := append([]relationshippublication.Projection(nil), base...)
			values = test.mutate(values)
			if _, err := Derive(t.Context(), values); !errors.Is(err, ErrInvalid) {
				t.Fatalf("noncanonical inventory error = %v", err)
			}
		})
	}
}

func TestDeriveHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Derive(ctx, frozenProjectionFixture()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled projection error = %v", err)
	}
}

func frozenProjectionFixture() []relationshippublication.Projection {
	result := make([]relationshippublication.Projection, 0, 20_999)
	serial := 0
	addRPC := func(family string, provider, consumer int) {
		method := fmt.Sprintf("BoundedFanoutP%05d", provider)
		if family == "layered_dag" {
			method = fmt.Sprintf("LayeredDagP%05d", provider)
		}
		target := contractPlacement(provider)
		result = append(result, relationshippublication.Projection{
			Schema: relationshippublication.ProjectionSchema, Kind: "rpc",
			PostingDigest: testDigest(fmt.Sprintf("posting-%d", serial)),
			Class:         "resolved", Plane: "grpc",
			LookupKey: fmt.Sprintf(
				"/t421.service%05d.Service%05d/%s", provider, provider, method,
			),
			Source: mainPlacement(consumer), Target: &target,
			Digest: testDigest(fmt.Sprintf("projection-%d", serial)),
		})
		serial++
	}
	for provider := 0; provider+1 < serviceCount; provider++ {
		addRPC("chain", provider, provider+1)
	}
	for provider := 0; provider < 100; provider++ {
		addRPC("layered_dag", provider, 100+provider)
		addRPC("layered_dag", provider, 100+(provider+1)%100)
	}
	for provider := 0; provider < serviceCount; provider++ {
		if provider >= 50 && provider < serviceCount-50 {
			continue
		}
		for slot := 1; slot <= 8; slot++ {
			addRPC("bounded_fanout", provider, (provider+slot+1)%serviceCount)
		}
	}
	for group := 0; group < serviceCount/20; group++ {
		base := group * 20
		for slot := 0; slot < 20; slot++ {
			plane := "consumer"
			if slot == 0 {
				plane = "producer"
			}
			result = append(result, relationshippublication.Projection{
				Schema: relationshippublication.ProjectionSchema, Kind: "kafka",
				PostingDigest: testDigest(fmt.Sprintf("posting-%d", serial)),
				Class:         "literal", Plane: plane,
				LookupKey: fmt.Sprintf("t421.hotspot.%04d", group),
				Source:    mainPlacement(base + slot),
				Digest:    testDigest(fmt.Sprintf("projection-%d", serial)),
			})
			serial++
		}
	}
	return result
}

func mainPlacement(service int) relationshippublication.Placement {
	return relationshippublication.Placement{
		Path: fmt.Sprintf("services/service-%05d/main.go", service),
		Claims: []relationshippublication.ServiceClaim{{
			ServiceKey: serviceKey(service), Disposition: "accepted",
			Roles: []relationshippublication.RoleClaim{{Role: "primary", Origin: "base"}},
		}},
	}
}

func contractPlacement(service int) relationshippublication.Placement {
	return relationshippublication.Placement{
		Path: fmt.Sprintf("contracts/service-%05d/api.proto", service),
		Claims: []relationshippublication.ServiceClaim{{
			ServiceKey: serviceKey(service), Disposition: "accepted",
			Roles: []relationshippublication.RoleClaim{
				{Role: "supporting", Origin: "base"},
				{Role: "typed", Origin: "base"},
			},
		}},
	}
}

func cloneProjection(value relationshippublication.Projection) relationshippublication.Projection {
	value.Source.Claims = cloneClaims(value.Source.Claims)
	if value.Target != nil {
		target := *value.Target
		target.Claims = cloneClaims(target.Claims)
		value.Target = &target
	}
	return value
}

func cloneClaims(values []relationshippublication.ServiceClaim) []relationshippublication.ServiceClaim {
	result := make([]relationshippublication.ServiceClaim, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Roles = slices.Clone(value.Roles)
	}
	return result
}

func testDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

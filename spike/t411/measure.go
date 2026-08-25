package t411

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"time"

	"github.com/bmeddeb/phebs/spike/t323"
)

const (
	frozenT323ReceiptSHA256 = "sha256:899492dcfe2f768de7e75003ff5d420655cbfeb8c44d9a76505bf6d6b8dededd"
	frozenT323BundleSHA256  = "sha256:8d70693ee440ff7683f8c3a39cc9b6565dd265cbc546d40e961759f2237617fa"
)

func Measure(repositoryRoot string) (Envelope, Receipt, error) {
	repositoryRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return Envelope{}, Receipt{}, err
	}
	inputs, err := readInputs(repositoryRoot)
	if err != nil {
		return Envelope{}, Receipt{}, err
	}
	first, err := BuildEnvelope()
	if err != nil {
		return Envelope{}, Receipt{}, err
	}
	second, err := BuildEnvelope()
	if err != nil {
		return Envelope{}, Receipt{}, err
	}
	firstBytes, err := MarshalCanonical(first)
	if err != nil {
		return Envelope{}, Receipt{}, err
	}
	secondBytes, err := MarshalCanonical(second)
	if err != nil {
		return Envelope{}, Receipt{}, err
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		return Envelope{}, Receipt{}, errors.New("two T41.1 envelope builds differ")
	}
	measurements := make([]ProfileMeasurement, 0, len(first.Profiles))
	for _, profile := range first.Profiles {
		measurement, err := measureProfile(profile)
		if err != nil {
			return Envelope{}, Receipt{}, err
		}
		measurements = append(measurements, measurement)
	}
	receipt := Receipt{
		Schema: ReceiptSchema, MeasuredOn: "2026-08-25", Inputs: inputs,
		Environment: Environment{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
			LogicalCPUs: runtime.NumCPU(), ProcessPeakRSSBytes: processPeakRSSBytes(),
			RSSMethod: "getrusage-self-after-validation-before-artifact-write",
		},
		Envelope: ArtifactIdentity{Bytes: len(firstBytes), SHA256: SHA256(firstBytes)},
		Profiles: measurements, Boundary: first.Boundary,
		Decision: frozenDecision(), Claims: neutralClaims(),
	}
	if err := ValidateReceipt(receipt, first); err != nil {
		return Envelope{}, Receipt{}, err
	}
	receipt.Environment.ProcessPeakRSSBytes = processPeakRSSBytes()
	if receipt.Environment.ProcessPeakRSSBytes < 1 {
		return Envelope{}, Receipt{}, errors.New("T41.1 process high-water RSS is unavailable")
	}
	return first, receipt, nil
}

func Author(repositoryRoot, destination string) (Receipt, error) {
	envelope, receipt, err := Measure(repositoryRoot)
	if err != nil {
		return Receipt{}, err
	}
	envelopeBytes, err := MarshalCanonical(envelope)
	if err != nil {
		return Receipt{}, err
	}
	receiptBytes, err := MarshalCanonical(receipt)
	if err != nil {
		return Receipt{}, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return Receipt{}, err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return Receipt{}, err
	}
	if err := os.WriteFile(filepath.Join(destination, "envelope.json"), envelopeBytes, 0o644); err != nil {
		return Receipt{}, err
	}
	if err := os.WriteFile(filepath.Join(destination, "receipt.json"), receiptBytes, 0o644); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func ValidateReceipt(receipt Receipt, envelope Envelope) error {
	if err := ValidateEnvelope(envelope); err != nil {
		return err
	}
	envelopeBytes, err := MarshalCanonical(envelope)
	if err != nil {
		return err
	}
	if receipt.Schema != ReceiptSchema || receipt.MeasuredOn != "2026-08-25" ||
		receipt.Inputs.T323ReceiptSHA256 != frozenT323ReceiptSHA256 ||
		receipt.Inputs.T323BundleSHA256 != frozenT323BundleSHA256 ||
		receipt.Environment.GOOS == "" || receipt.Environment.GOARCH == "" ||
		receipt.Environment.GoVersion == "" || receipt.Environment.LogicalCPUs < 1 ||
		receipt.Environment.ProcessPeakRSSBytes < 1 ||
		receipt.Environment.RSSMethod != "getrusage-self-after-validation-before-artifact-write" ||
		receipt.Envelope.Bytes != len(envelopeBytes) || receipt.Envelope.SHA256 != SHA256(envelopeBytes) ||
		len(receipt.Profiles) != len(envelope.Profiles) || !reflect.DeepEqual(receipt.Boundary, envelope.Boundary) ||
		!reflect.DeepEqual(receipt.Decision, frozenDecision()) || !validClaims(receipt.Claims) {
		return errors.New("T41.1 receipt envelope is invalid")
	}
	for index, measurement := range receipt.Profiles {
		profile := envelope.Profiles[index]
		digest, err := ProfileDigest(profile)
		if err != nil {
			return err
		}
		if measurement.Name != profile.Name || measurement.ProfileDigest != digest ||
			measurement.WallMicros < 1 || measurement.GoAllocatedBytes < 1 ||
			!reflect.DeepEqual(measurement.Serialization, serializationMetrics(profile)) ||
			!reflect.DeepEqual(measurement.Projection, profile.Publication) ||
			!reflect.DeepEqual(measurement.StoreTransaction, storeEstimate(profile)) ||
			!reflect.DeepEqual(measurement.Filesystem, filesystemEstimate(profile)) ||
			!reflect.DeepEqual(measurement.Lifecycle, lifecycleEstimate(profile)) {
			return fmt.Errorf("T41.1 measurement %q is invalid", measurement.Name)
		}
	}
	return nil
}

func measureProfile(profile Profile) (ProfileMeasurement, error) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	rebuilt, err := buildAcceptedProfile(profile.AcceptedServices)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	if !reflect.DeepEqual(profile, rebuilt) {
		return ProfileMeasurement{}, errors.New("measured profile differs from frozen authority")
	}
	wall := time.Since(start).Microseconds()
	runtime.ReadMemStats(&after)
	digest, err := ProfileDigest(profile)
	if err != nil {
		return ProfileMeasurement{}, err
	}
	return ProfileMeasurement{
		Name: profile.Name, ProfileDigest: digest, WallMicros: max(wall, int64(1)),
		GoAllocatedBytes: max(after.TotalAlloc-before.TotalAlloc, uint64(1)),
		Serialization:    serializationMetrics(profile), Projection: profile.Publication,
		StoreTransaction: storeEstimate(profile), Filesystem: filesystemEstimate(profile),
		Lifecycle: lifecycleEstimate(profile),
	}, nil
}

func serializationMetrics(profile Profile) []ByteMetric {
	return []ByteMetric{
		{Kind: "logical_catalog", Measurement: "exact_canonical", Bytes: int64(profile.LogicalCatalog.Bytes)},
		{Kind: "catalog_root", Measurement: "exact_encoded", Bytes: int64(profile.Publication.Root.Bytes)},
		{Kind: "service_members", Measurement: "exact_encoded_aggregate", Bytes: int64(profile.Publication.ServiceMemberBytes)},
		{Kind: "placement_members", Measurement: "exact_encoded_aggregate", Bytes: int64(profile.Publication.PlacementMemberBytes)},
		{Kind: "publication", Measurement: "exact_encoded_aggregate", Bytes: int64(profile.Publication.EncodedBytes)},
		{Kind: "fixture_content", Measurement: "exact_generated", Bytes: profile.Fixture.ContentBytes},
		{Kind: "relationship_empty", Measurement: "exact_canonical", Bytes: profile.Relationships[0].Bytes},
		{Kind: "relationship_mixed", Measurement: "exact_canonical", Bytes: profile.Relationships[1].Bytes},
		{Kind: "relationship_dense", Measurement: "exact_canonical", Bytes: profile.Relationships[2].Bytes},
	}
}

func storeEstimate(profile Profile) StoreEstimate {
	pointerBytes, _ := json.Marshal(struct {
		RootSHA256 string `json:"root_sha256"`
	}{RootSHA256: profile.Publication.Root.SHA256})
	return StoreEstimate{
		ImmutableRows: profile.Publication.TotalMembers + 1,
		LargestRowBytes: max(
			profile.Publication.Root.Bytes,
			max(profile.Publication.MaxServiceMemberBytes, profile.Publication.MaxPlacementMemberBytes),
		),
		PointerSwapRows: 1, PointerSwapBytes: len(pointerBytes),
	}
}

func filesystemEstimate(profile Profile) FilesystemEstimate {
	return FilesystemEstimate{RegularFiles: profile.Fixture.RegularFiles, LogicalBytes: profile.Fixture.ContentBytes}
}

func lifecycleEstimate(profile Profile) LifecycleEstimate {
	return LifecycleEstimate{
		RootRows: 1, MemberRows: profile.Publication.TotalMembers,
		CollectRows:  profile.Publication.TotalMembers + 1,
		CollectBytes: profile.Publication.EncodedBytes,
	}
}

func frozenDecision() CapDecision {
	return CapDecision{
		AcceptedFloor: AcceptedServiceFloor, AcceptedTarget: AcceptedServiceTarget,
		MaxTotalServiceRecords: MaxTotalServices, MaxMemberships: MaxMemberships,
		MaxDistinctPaths: MaxDistinctPaths, MaxSuccessorEdges: MaxSuccessorEdges,
		MaxServiceSuccessors: MaxServiceSuccessors, MaxLogicalBytes: MaxLogicalBytes,
		MaxPublicationBytes: MaxPublicationBytes, MaxClaimsPerPlacement: MaxClaimsPerPlacement,
		MaxClaimsPerBucket:         MaxClaimsPerBucket,
		RelationshipRepresentation: "placement-claim-buckets-v1",
		HardPreGrowthRefusal:       true,
	}
}

func readInputs(repositoryRoot string) (InputIdentity, error) {
	receiptBytes, err := os.ReadFile(filepath.Join(repositoryRoot, "spike", "t323", "receipt.json"))
	if err != nil {
		return InputIdentity{}, err
	}
	if digest := SHA256(receiptBytes); digest != frozenT323ReceiptSHA256 {
		return InputIdentity{}, fmt.Errorf("T32.3 receipt digest %s differs from frozen %s", digest, frozenT323ReceiptSHA256)
	}
	receipt, err := t323.DecodeStrict[t323.Receipt](receiptBytes)
	if err != nil {
		return InputIdentity{}, err
	}
	if receipt.Schema != t323.ReceiptSchema || !receipt.Claims.Synthetic ||
		receipt.Claims.EstablishesTargetSLO || receipt.Claims.EstablishesAccuracy ||
		receipt.Claims.SelectsTopology || receipt.Claims.ProductionRegistration {
		return InputIdentity{}, errors.New("T32.3 receipt is not preserved neutral input")
	}
	bundleBytes, err := os.ReadFile(filepath.Join(repositoryRoot, "spike", "t323", receipt.Bundle.Path))
	if err != nil {
		return InputIdentity{}, err
	}
	if digest := SHA256(bundleBytes); digest != frozenT323BundleSHA256 || digest != receipt.Bundle.SHA256 {
		return InputIdentity{}, errors.New("T32.3 bundle is not preserved neutral input")
	}
	return InputIdentity{
		T323ReceiptSHA256: frozenT323ReceiptSHA256, T323BundleSHA256: frozenT323BundleSHA256,
	}, nil
}

func processPeakRSSBytes() int64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	value := int64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		value *= 1_024
	}
	return value
}

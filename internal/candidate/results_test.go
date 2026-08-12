package candidate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
)

func TestDomainResultPlanAndRootAreDeterministicAndBindEveryAuthority(t *testing.T) {
	domain := resultTestDomain(t, 2, "admitted")
	authority := resultTestAuthority()
	reservations := []DomainResultTotals{
		{Facts: 2, Rows: 4, References: 1, CanonicalBytes: 32, EncodedBytes: 40, MemberBytes: 40, Members: 1},
		{Facts: 1, Rows: 2, CanonicalBytes: 16, EncodedBytes: 20, MemberBytes: 20, Members: 1},
	}
	firstPlan, err := BuildDomainResultPlan(domain, authority, reservations)
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := BuildDomainResultPlan(domain, authority, slices.Clone(reservations))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstPlan, secondPlan) || firstPlan.Digest == "" {
		t.Fatalf("plans differ:\n%+v\n%+v", firstPlan, secondPlan)
	}
	if err := ValidateDomainResultPlan(firstPlan, domain); err != nil {
		t.Fatal(err)
	}
	if firstPlan.Repository != domain.index.Repository ||
		firstPlan.CandidateManifestDigest != domain.index.CandidateManifestDigest ||
		firstPlan.CandidateGenerationDigest != domain.index.CandidateGenerationDigest ||
		firstPlan.CandidatePartitionRootDigest != domain.publication.root.Digest ||
		firstPlan.CandidatePolicyDigest != domain.publication.root.PolicyDigest ||
		firstPlan.SourceGenerationDigest != authority.SourceGenerationDigest ||
		firstPlan.ObservationGenerationDigest != authority.ObservationGenerationDigest ||
		firstPlan.Domain != domain.index.Domain || firstPlan.Version != domain.index.Version ||
		firstPlan.ExtractorVersion != authority.ExtractorVersion ||
		firstPlan.ExtractionPolicyDigest != authority.ExtractionPolicyDigest ||
		firstPlan.DomainScheduleDigest != domain.index.ScheduleDigest {
		t.Fatalf("plan omitted authority: %+v", firstPlan)
	}

	results := make([]PartitionResult, len(firstPlan.Expected))
	for ordinal, reservation := range reservations {
		results[ordinal], err = BuildPartitionResult(firstPlan, ordinal, PartitionResultSpec{
			Disposition: PartitionResultSuccess, Totals: reservation,
			ContentDigest: testResultDigest(byte(ordinal + 8)),
		})
		if err != nil {
			t.Fatal(err)
		}
		again, buildErr := BuildPartitionResult(firstPlan, ordinal, PartitionResultSpec{
			Disposition: PartitionResultSuccess, Totals: reservation,
			ContentDigest: testResultDigest(byte(ordinal + 8)),
		})
		if buildErr != nil || !reflect.DeepEqual(results[ordinal], again) {
			t.Fatalf("partition result is nondeterministic: %+v / %+v / %v", results[ordinal], again, buildErr)
		}
		v1Identity, identityErr := PartitionResultIdentity(domain.index.Partitions[ordinal], results[ordinal].Digest)
		if identityErr != nil || results[ordinal].Identity != v1Identity {
			t.Fatalf("result identity did not preserve T40.8 v1: %q / %q / %v", results[ordinal].Identity, v1Identity, identityErr)
		}
	}
	firstRoot, err := BuildDomainResultRoot(firstPlan, results)
	if err != nil {
		t.Fatal(err)
	}
	withExactRetry := []PartitionResult{results[0], results[0], results[1]}
	secondRoot, err := BuildDomainResultRoot(secondPlan, withExactRetry)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstRoot, secondRoot) || firstRoot.Disposition != PartitionResultSuccess ||
		firstRoot.ResultCount != 2 || firstRoot.Totals != (DomainResultTotals{
		Facts: 3, Rows: 6, References: 1,
		CanonicalBytes: 48, EncodedBytes: 60, MemberBytes: 60, Members: 2,
	}) {
		t.Fatalf("roots differ or totals are wrong:\n%+v\n%+v", firstRoot, secondRoot)
	}
	if err := ValidateDomainResultRoot(firstRoot, firstPlan); err != nil {
		t.Fatal(err)
	}
	firstBytes, _ := json.Marshal(firstRoot)
	secondBytes, _ := json.Marshal(secondRoot)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("deterministic roots are not byte-equal")
	}
}

func TestPartitionResultStatesAreClosedAndCannotBeRelabeled(t *testing.T) {
	domain := resultTestDomain(t, 1, "admitted")
	reservation := DomainResultTotals{
		Facts: 2, Rows: 4, References: 2,
		CanonicalBytes: 64, EncodedBytes: 80, MemberBytes: 80, Members: 1,
	}
	plan, err := BuildDomainResultPlan(domain, resultTestAuthority(), []DomainResultTotals{reservation})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		spec PartitionResultSpec
	}{
		{"success", PartitionResultSpec{Disposition: PartitionResultSuccess, Totals: reservation, ContentDigest: testResultDigest(8)}},
		{"empty", PartitionResultSpec{Disposition: PartitionResultEmpty}},
		{"terminal", PartitionResultSpec{Disposition: PartitionResultTerminalRefusal, Reason: "fact_limit"}},
		{"retryable", PartitionResultSpec{Disposition: PartitionResultRetryable, Reason: "temporary_store_failure"}},
	}
	identities := make(map[string]string, len(tests))
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, buildErr := BuildPartitionResult(plan, 0, test.spec)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			root, rootErr := BuildDomainResultRoot(plan, []PartitionResult{result})
			if rootErr != nil || root.Disposition != test.spec.Disposition {
				t.Fatalf("root = %+v, %v", root, rootErr)
			}
			identities[test.name] = result.Identity
			forged := result
			forged.Disposition = PartitionResultRetryable
			if test.spec.Disposition == PartitionResultRetryable {
				forged.Disposition = PartitionResultEmpty
			}
			if _, forgedErr := BuildDomainResultRoot(plan, []PartitionResult{forged}); !errors.Is(forgedErr, ErrInvalidDomainResult) {
				t.Fatalf("relabeled result error = %v", forgedErr)
			}
		})
	}
	seen := make(map[string]struct{}, len(identities))
	for name, identity := range identities {
		if _, duplicate := seen[identity]; duplicate {
			t.Fatalf("closed state %q reused another state's identity", name)
		}
		seen[identity] = struct{}{}
	}

	invalid := []PartitionResultSpec{
		{Disposition: "unknown"},
		{Disposition: PartitionResultSuccess, ContentDigest: testResultDigest(9)},
		{Disposition: PartitionResultEmpty, Totals: DomainResultTotals{Facts: 1}},
		{Disposition: PartitionResultUnavailablePrerequisite, Reason: "missing"},
		{Disposition: PartitionResultUnavailablePrerequisite, Reason: "typed_input_absent"},
		{Disposition: PartitionResultTerminalRefusal},
		{Disposition: PartitionResultRetryable, Reason: "bad\nreason"},
	}
	for _, spec := range invalid {
		if _, buildErr := BuildPartitionResult(plan, 0, spec); !errors.Is(buildErr, ErrInvalidDomainResult) {
			t.Fatalf("invalid state %+v error = %v", spec, buildErr)
		}
	}

	for name, mutate := range map[string]func(*DomainResultTotals){
		"facts":           func(value *DomainResultTotals) { value.Facts++ },
		"rows":            func(value *DomainResultTotals) { value.Rows++ },
		"references":      func(value *DomainResultTotals) { value.References++ },
		"canonical_bytes": func(value *DomainResultTotals) { value.CanonicalBytes++ },
		"encoded_bytes":   func(value *DomainResultTotals) { value.EncodedBytes++ },
		"member_bytes":    func(value *DomainResultTotals) { value.MemberBytes++ },
		"members":         func(value *DomainResultTotals) { value.Members++ },
	} {
		t.Run("reservation_one_over_"+name, func(t *testing.T) {
			totals := reservation
			mutate(&totals)
			if _, buildErr := BuildPartitionResult(plan, 0, PartitionResultSpec{
				Disposition: PartitionResultSuccess, Totals: totals,
				ContentDigest: testResultDigest(10),
			}); !errors.Is(buildErr, ErrInvalidDomainResult) {
				t.Fatalf("one-over result error = %v", buildErr)
			}
		})
	}
}

func TestDomainResultPlanTamperingCannotReachRootAuthority(t *testing.T) {
	domain := resultTestDomain(t, 2, "admitted")
	reservations := []DomainResultTotals{{Facts: 1}, {Rows: 1}}
	plan, err := BuildDomainResultPlan(domain, resultTestAuthority(), reservations)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]PartitionResult, len(plan.Expected))
	for ordinal := range results {
		results[ordinal], err = BuildPartitionResult(plan, ordinal, PartitionResultSpec{
			Disposition: PartitionResultSuccess, Totals: reservations[ordinal],
			ContentDigest: testResultDigest(byte(ordinal + 11)),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for name, tamper := range map[string]func(*DomainResultPlan){
		"source_generation": func(value *DomainResultPlan) { value.SourceGenerationDigest = testResultDigest(14) },
		"reservation":       func(value *DomainResultPlan) { value.Expected[0].Reservation.Facts++ },
		"range":             func(value *DomainResultPlan) { value.Expected[1].SourceStart-- },
		"order": func(value *DomainResultPlan) {
			value.Expected[0], value.Expected[1] = value.Expected[1], value.Expected[0]
		},
	} {
		t.Run(name, func(t *testing.T) {
			forged := plan
			forged.Expected = slices.Clone(plan.Expected)
			tamper(&forged)
			if _, rootErr := BuildDomainResultRoot(forged, results); !errors.Is(rootErr, ErrInvalidDomainResult) {
				t.Fatalf("tampered plan reached root authority: %v", rootErr)
			}
		})
	}
}

func TestDomainResultRootRejectsMissingExtraReorderedAndTamperedResults(t *testing.T) {
	domain := resultTestDomain(t, 3, "admitted")
	reservations := []DomainResultTotals{
		{Facts: 1, Rows: 2, Members: 1, MemberBytes: 10, EncodedBytes: 10, CanonicalBytes: 8},
		{Facts: 1, Rows: 2, Members: 1, MemberBytes: 10, EncodedBytes: 10, CanonicalBytes: 8},
		{Facts: 1, Rows: 2, Members: 1, MemberBytes: 10, EncodedBytes: 10, CanonicalBytes: 8},
	}
	plan, err := BuildDomainResultPlan(domain, resultTestAuthority(), reservations)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]PartitionResult, 3)
	for ordinal := range results {
		results[ordinal], err = BuildPartitionResult(plan, ordinal, PartitionResultSpec{
			Disposition: PartitionResultSuccess, Totals: reservations[ordinal],
			ContentDigest: testResultDigest(byte(ordinal + 1)),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, submitted := range map[string][]PartitionResult{
		"nil":       nil,
		"empty":     {},
		"missing":   results[:2],
		"reordered": {results[1], results[0], results[2]},
	} {
		t.Run(name, func(t *testing.T) {
			if _, buildErr := BuildDomainResultRoot(plan, submitted); !errors.Is(buildErr, ErrInvalidDomainResult) {
				t.Fatalf("error = %v", buildErr)
			}
		})
	}
	extra := results[0]
	extra.PartitionOrdinal = len(results)
	if _, err := BuildDomainResultRoot(plan, append(slices.Clone(results), extra)); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("extra result error = %v", err)
	}
	differentRetry, err := BuildPartitionResult(plan, 0, PartitionResultSpec{Disposition: PartitionResultEmpty})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDomainResultRoot(plan, []PartitionResult{results[0], differentRetry, results[1], results[2]}); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("non-byte-equal duplicate error = %v", err)
	}
	tampered := slices.Clone(results)
	tampered[0].Totals.Facts--
	if _, err := BuildDomainResultRoot(plan, tampered); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("tampered result error = %v", err)
	}
	root, err := BuildDomainResultRoot(plan, results)
	if err != nil {
		t.Fatal(err)
	}
	root.Totals.Facts--
	if err := ValidateDomainResultRoot(root, plan); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("tampered root error = %v", err)
	}
	root, err = BuildDomainResultRoot(plan, results)
	if err != nil {
		t.Fatal(err)
	}
	root.Disposition = PartitionResultUnavailablePrerequisite
	root.Digest = domainResultRootDigest(root)
	if err := ValidateDomainResultRoot(root, plan); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("admitted unavailable root error = %v", err)
	}
	root, err = BuildDomainResultRoot(plan, results)
	if err != nil {
		t.Fatal(err)
	}
	root.Results[0], root.Results[1] = root.Results[1], root.Results[0]
	root.Digest = domainResultRootDigest(root)
	if err := ValidateDomainResultRoot(root, plan); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("reordered root error = %v", err)
	}
}

func TestDomainResultExactAggregateBoundsAndOneOverAreClosed(t *testing.T) {
	domain := resultTestDomainWithTyped(t, MaxDomainResultMemberPartitions)
	limits := FrozenDomainResultLimits()
	reservations := make([]DomainResultTotals, MaxDomainResultPartitions)
	distributeResultScalar(reservations, limits.Facts, func(value *DomainResultTotals, amount int64) { value.Facts = amount }, int64(PartitionMaxFacts))
	distributeResultScalar(reservations, limits.Rows, func(value *DomainResultTotals, amount int64) { value.Rows = amount }, int64(PartitionMaxRows))
	distributeResultScalar(reservations, limits.References, func(value *DomainResultTotals, amount int64) { value.References = amount }, int64(PartitionMaxReferences))
	distributeResultScalar(reservations, limits.CanonicalBytes, func(value *DomainResultTotals, amount int64) { value.CanonicalBytes = amount }, limits.CanonicalBytes)
	distributeResultScalar(reservations, limits.EncodedBytes, func(value *DomainResultTotals, amount int64) { value.EncodedBytes = amount }, limits.EncodedBytes)
	distributeResultScalar(reservations, limits.MemberBytes, func(value *DomainResultTotals, amount int64) { value.MemberBytes = amount }, limits.MemberBytes)
	for index := 0; index < MaxDomainResultMemberPartitions; index++ {
		reservations[index].Members = 1
	}
	plan, err := BuildDomainResultPlan(domain, resultTestAuthority(), reservations)
	if err != nil || plan.Reserved != (DomainResultTotals{
		Facts: limits.Facts, Rows: limits.Rows, References: limits.References,
		CanonicalBytes: limits.CanonicalBytes, EncodedBytes: limits.EncodedBytes,
		MemberBytes: limits.MemberBytes, Members: limits.Members,
	}) {
		t.Fatalf("exact plan = %+v, %v", plan.Reserved, err)
	}
	results := make([]PartitionResult, len(plan.Expected))
	for ordinal := range results {
		spec := PartitionResultSpec{
			Disposition: PartitionResultSuccess, Totals: reservations[ordinal],
			ContentDigest: testResultDigest(byte(ordinal%15 + 1)),
		}
		if reservations[ordinal] == (DomainResultTotals{}) {
			spec = PartitionResultSpec{Disposition: PartitionResultEmpty}
		}
		results[ordinal], err = BuildPartitionResult(plan, ordinal, spec)
		if err != nil {
			t.Fatalf("result %d: %v", ordinal, err)
		}
	}
	root, err := BuildDomainResultRoot(plan, results)
	if err != nil || root.Totals != plan.Reserved || root.ResultCount != MaxDomainResultPartitions {
		t.Fatalf("exact root = %+v, %v", root, err)
	}

	for name, test := range map[string]struct {
		mutate    func([]DomainResultTotals)
		dimension pipelinerefusal.Dimension
	}{
		"facts":        {func(values []DomainResultTotals) { values[len(values)-1].Facts++ }, pipelinerefusal.DimensionDomainResultFacts},
		"rows":         {func(values []DomainResultTotals) { values[len(values)-1].Rows++ }, pipelinerefusal.DimensionDomainResultRows},
		"references":   {func(values []DomainResultTotals) { values[len(values)-1].References++ }, pipelinerefusal.DimensionDomainResultReferences},
		"canonical":    {func(values []DomainResultTotals) { values[len(values)-1].CanonicalBytes++ }, pipelinerefusal.DimensionDomainCanonicalBytes},
		"encoded":      {func(values []DomainResultTotals) { values[len(values)-1].EncodedBytes++ }, pipelinerefusal.DimensionDomainEncodedBytes},
		"member_bytes": {func(values []DomainResultTotals) { values[len(values)-1].MemberBytes++ }, pipelinerefusal.DimensionCandidateMemberBytes},
		"members":      {func(values []DomainResultTotals) { values[len(values)-1].Members++ }, pipelinerefusal.DimensionCandidateMembers},
	} {
		t.Run(name, func(t *testing.T) {
			oneOver := slices.Clone(reservations)
			test.mutate(oneOver)
			_, buildErr := BuildDomainResultPlan(domain, resultTestAuthority(), oneOver)
			var measurement *pipelinerefusal.Measurement
			if !errors.Is(buildErr, ErrInvalidDomainResult) || !errors.As(buildErr, &measurement) ||
				measurement.Dimension != test.dimension || measurement.Observed != measurement.Limit+1 {
				t.Fatalf("one-over error = %v", buildErr)
			}
		})
	}
	if _, err := BuildDomainResultPlan(
		resultTestDomain(t, MaxDomainResultPartitions+1, "admitted"),
		resultTestAuthority(), make([]DomainResultTotals, MaxDomainResultPartitions+1),
	); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("partition one-over error = %v", err)
	}
	if _, err := BuildDomainResultPlan(
		resultTestDomain(t, MaxDomainResultMemberPartitions+1, "admitted"),
		resultTestAuthority(), make([]DomainResultTotals, MaxDomainResultMemberPartitions+1),
	); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("member-partition one-over error = %v", err)
	}
	partitionQuotaOneOver := slices.Clone(reservations)
	partitionQuotaOneOver[0].Facts = int64(PartitionMaxFacts) + 1
	if _, err := BuildDomainResultPlan(domain, resultTestAuthority(), partitionQuotaOneOver); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("partition quota one-over error = %v", err)
	}
	tooManyRetries := make([]PartitionResult, MaxDomainResultInventoryEntries+1)
	for index := range tooManyRetries {
		tooManyRetries[index] = results[0]
	}
	if _, err := BuildDomainResultRoot(plan, tooManyRetries); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("inventory one-over error = %v", err)
	}

	typedPlan, err := BuildDomainResultPlan(
		resultTestDomainWithTyped(t, 1), resultTestAuthority(),
		[]DomainResultTotals{{}, {}},
	)
	if err != nil {
		t.Fatal(err)
	}
	extraTyped := typedPlan.Expected[len(typedPlan.Expected)-1]
	extraTyped.Ordinal++
	extraTyped.PartitionDigest = testResultDigest(15)
	extraTyped.Digest = partitionExpectationDigest(extraTyped)
	typedPlan.Expected = append(typedPlan.Expected, extraTyped)
	typedPlan.Digest = domainResultPlanDigest(typedPlan)
	if _, err := BuildDomainResultRoot(typedPlan, []PartitionResult{}); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("typed-partition one-over error = %v", err)
	}
}

func TestDomainResultMaximumStringsAndExactReplayFitInventory(t *testing.T) {
	domain := resultTestDomainWithTyped(t, MaxDomainResultMemberPartitions)
	reservations := make([]DomainResultTotals, MaxDomainResultPartitions)
	plan, err := BuildDomainResultPlan(domain, resultTestAuthority(), reservations)
	if err != nil {
		t.Fatal(err)
	}
	plan.Repository = strings.Repeat("\"", 1024)
	plan.Domain = strings.Repeat("\\", 128)
	plan.Version = strings.Repeat("\"", 128)
	plan.ExtractorVersion = strings.Repeat("\\", 256)
	plan.Digest = domainResultPlanDigest(plan)

	results := make([]PartitionResult, len(plan.Expected))
	resultBytes := int64(0)
	for ordinal := range results {
		results[ordinal], err = BuildPartitionResult(plan, ordinal, PartitionResultSpec{
			Disposition: PartitionResultTerminalRefusal,
			Reason:      strings.Repeat("\\", 256),
		})
		if err != nil {
			t.Fatalf("maximum-string result %d: %v", ordinal, err)
		}
		raw, marshalErr := sparseCanonical(results[ordinal])
		if marshalErr != nil || int64(len(raw)) > MaxPartitionResultBytes {
			t.Fatalf("maximum-string result %d bytes = %d, %v", ordinal, len(raw), marshalErr)
		}
		resultBytes += int64(len(raw))
	}
	if MaxDomainResultInventoryBytes != int64(MaxDomainResultInventoryEntries)*MaxPartitionResultBytes ||
		resultBytes*2 > MaxDomainResultInventoryBytes {
		t.Fatalf("inventory fence cannot admit its declared controls: actual=%d limit=%d", resultBytes*2, MaxDomainResultInventoryBytes)
	}
	root, err := BuildDomainResultRoot(plan, results)
	if err != nil || root.ResultCount != MaxDomainResultPartitions {
		t.Fatalf("maximum unique set = %d, %v", root.ResultCount, err)
	}
	exactReplay := append(slices.Clone(results), results...)
	replayed, err := BuildDomainResultRoot(plan, exactReplay)
	if err != nil || !reflect.DeepEqual(root, replayed) {
		t.Fatalf("maximum exact replay differs: %v", err)
	}
	if _, err := BuildDomainResultRoot(plan, append(exactReplay, results[0])); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("maximum replay one-over error = %v", err)
	}
}

func TestDomainResultValidationIsChildFirstAndReadersFenceRawBytes(t *testing.T) {
	domain := resultTestDomain(t, 1, "admitted")
	plan, err := BuildDomainResultPlan(domain, resultTestAuthority(), []DomainResultTotals{{Facts: 1}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := BuildPartitionResult(plan, 0, PartitionResultSpec{
		Disposition: PartitionResultSuccess, Totals: DomainResultTotals{Facts: 1},
		ContentDigest: testResultDigest(12),
	})
	if err != nil {
		t.Fatal(err)
	}
	root, err := BuildDomainResultRoot(plan, []PartitionResult{result})
	if err != nil {
		t.Fatal(err)
	}

	planRaw, _ := sparseCanonical(plan)
	decodedPlan, err := DecodeDomainResultPlan(bytes.NewReader(planRaw), domain)
	if err != nil || !reflect.DeepEqual(decodedPlan, plan) {
		t.Fatalf("decoded plan differs: %v", err)
	}
	rootRaw, _ := sparseCanonical(root)
	decodedRoot, err := DecodeDomainResultRoot(bytes.NewReader(rootRaw), plan)
	if err != nil || !reflect.DeepEqual(decodedRoot, root) {
		t.Fatalf("decoded root differs: %v", err)
	}
	if _, err := DecodeDomainResultPlan(
		strings.NewReader(strings.Repeat(" ", int(MaxDomainResultPlanBytes)+1)), domain,
	); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("oversized plan decode error = %v", err)
	}
	if _, err := DecodeDomainResultRoot(
		strings.NewReader(strings.Repeat(" ", int(MaxDomainResultRootBytes)+1)), plan,
	); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("oversized root decode error = %v", err)
	}

	malformedPlan := plan
	malformedPlan.Expected = slices.Clone(plan.Expected)
	malformedPlan.Expected[0].PartitionDigest = strings.Repeat("x", int(MaxDomainResultPlanBytes)+1)
	if _, err := BuildDomainResultRoot(malformedPlan, []PartitionResult{result}); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("unbounded plan child error = %v", err)
	}
	malformedRoot := root
	malformedRoot.Repository = strings.Repeat("x", int(MaxDomainResultRootBytes)+1)
	if err := ValidateDomainResultRoot(malformedRoot, plan); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("unbounded root scalar error = %v", err)
	}
}

func TestDomainResultNilAndZeroPartitionStatesAreExact(t *testing.T) {
	if _, err := BuildDomainResultPlan(
		resultTestDomain(t, 0, "empty"), resultTestAuthority(), nil,
	); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("nil reservations error = %v", err)
	}
	emptyPlan, err := BuildDomainResultPlan(
		resultTestDomain(t, 0, "empty"), resultTestAuthority(), []DomainResultTotals{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildDomainResultRoot(emptyPlan, nil); !errors.Is(err, ErrInvalidDomainResult) {
		t.Fatalf("nil result inventory error = %v", err)
	}
	emptyRoot, err := BuildDomainResultRoot(emptyPlan, []PartitionResult{})
	if err != nil || emptyRoot.Disposition != PartitionResultEmpty || emptyRoot.Results == nil {
		t.Fatalf("empty root = %+v, %v", emptyRoot, err)
	}
	unavailablePlan, err := BuildDomainResultPlan(
		resultTestDomain(t, 0, "unavailable"), resultTestAuthority(), []DomainResultTotals{},
	)
	if err != nil {
		t.Fatal(err)
	}
	unavailableRoot, err := BuildDomainResultRoot(unavailablePlan, []PartitionResult{})
	if err != nil || unavailableRoot.Disposition != PartitionResultUnavailablePrerequisite {
		t.Fatalf("unavailable root = %+v, %v", unavailableRoot, err)
	}
}

func resultTestDomain(t *testing.T, partitionCount int, availability string) *SparseDomain {
	t.Helper()
	return resultTestDomainShape(t, partitionCount, false, availability)
}

func resultTestDomainWithTyped(t *testing.T, memberCount int) *SparseDomain {
	t.Helper()
	return resultTestDomainShape(t, memberCount, true, "admitted")
}

func resultTestDomainShape(t *testing.T, memberCount int, typed bool, availability string) *SparseDomain {
	t.Helper()
	quotas := FrozenExtractionPartitionQuotas()
	repository := "example.invalid/t409"
	manifestDigest := testResultDigest(1)
	generationDigest := testResultDigest(2)
	partitionCount := memberCount
	if typed {
		partitionCount++
	}
	partitions := make([]ExtractionPartition, partitionCount)
	for ordinal := 0; ordinal < memberCount; ordinal++ {
		partitions[ordinal] = ExtractionPartition{
			Schema: ExtractionPartitionSchema, Repository: repository,
			CandidateManifestDigest:   manifestDigest,
			CandidateGenerationDigest: generationDigest,
			Domain:                    "proto-declarations", Version: "v1", Plane: PlaneRepository,
			Kind: PartitionKindCandidateMember, Ordinal: ordinal,
			SourceStart: ordinal, SourceEnd: ordinal + 1,
			ByteKind: "repository_declared",
			Member: &Artifact{
				Name: "candidate-" + leftPadResultOrdinal(ordinal) + ".ndjson", Ordinal: ordinal,
				RecordCount: 1, DeclaredBytes: 1, ContentBytes: 1,
				ContentDigest: testResultDigest(byte(ordinal%15 + 1)),
			},
			AdmittedRecords: 1, AdmittedDeclaredBytes: 1, Quotas: quotas,
		}
		partitions[ordinal].Digest = sparsePartitionDigest(partitions[ordinal])
	}
	var typedInputs []SparseTypedInput
	if typed {
		typedInput := SparseTypedInput{
			Kind: "scip", Path: "index.scip", ObjectID: strings.Repeat("a", 40),
			DeclaredBytes: 1, Present: true,
		}
		typedInputs = []SparseTypedInput{typedInput}
		typedScopeValues := make([]sparseTypedSource, memberCount)
		for index := range typedScopeValues {
			path := "source-" + leftPadResultOrdinal(index) + ".go"
			typedScopeValues[index] = sparseTypedSource{
				pathIdentity: sha256.Sum256([]byte(path)), path: path,
				objectID: strings.Repeat("a", 40), declaredBytes: 1,
			}
		}
		typedScope, err := canonicalTypedScope(typedScopeValues)
		if err != nil {
			t.Fatal(err)
		}
		typedScopeDescriptor := SparseTypedScopeDescriptor{
			Name: sparseTypedScopeName(0), Records: memberCount,
			ContentBytes: int64(len(typedScope)), ContentDigest: sparseContentDigest(typedScope),
		}
		ordinal := len(partitions) - 1
		partitions[ordinal] = ExtractionPartition{
			Schema: ExtractionPartitionSchema, Repository: repository,
			CandidateManifestDigest:   manifestDigest,
			CandidateGenerationDigest: generationDigest,
			Domain:                    "proto-declarations", Version: "v1", Plane: PlaneRepository,
			Kind: PartitionKindTypedInput, Ordinal: ordinal,
			SourceStart: memberCount, SourceEnd: memberCount,
			ByteKind: "typed_input_declared", TypedInput: &typedInputs[0],
			TypedScope:            &typedScopeDescriptor,
			AdmittedDeclaredBytes: 1, Quotas: quotas,
		}
		partitions[ordinal].Digest = sparsePartitionDigest(partitions[ordinal])
	}
	index := SparseDomainIndex{
		Schema: SparseDomainSchema, Repository: repository,
		CandidateManifestDigest:   manifestDigest,
		CandidateGenerationDigest: generationDigest,
		Domain:                    "proto-declarations", Version: "v1", PolicyOrdinal: 0,
		Plane: PlaneRepository, Quotas: quotas, Partitions: partitions,
	}
	index.ScheduleDigest = sparseDomainScheduleDigest(index)
	index.Digest = sparseDomainDigest(index)
	descriptor := SparseDomainDescriptor{
		Domain: index.Domain, Version: index.Version, PolicyOrdinal: 0, Plane: index.Plane,
		Availability: availability, TypedInputs: typedInputs, PartitionCount: partitionCount,
		AdmittedRecords: memberCount, AdmittedDeclaredBytes: int64(partitionCount),
		CandidateMemberReadBytes: int64(memberCount), IndexName: "candidate-domain-000.json",
		IndexBytes: 1, IndexDigest: index.Digest, IndexContentDigest: testResultDigest(3),
		DomainScheduleDigest: index.ScheduleDigest,
	}
	if typed {
		typedScope := *partitions[len(partitions)-1].TypedScope
		descriptor.TypedScope = &typedScope
	}
	if availability == "empty" {
		descriptor.AdmittedRecords = 0
		descriptor.AdmittedDeclaredBytes = 0
		descriptor.CandidateMemberReadBytes = 0
	}
	if availability == "unavailable" {
		descriptor.UnavailableReason = "typed_input_absent"
		descriptor.AdmittedRecords = 0
		descriptor.AdmittedDeclaredBytes = 0
		descriptor.CandidateMemberReadBytes = 0
		descriptor.TypedInputs = []SparseTypedInput{{Kind: "scip", Present: false}}
	}
	return &SparseDomain{
		publication: &SparsePublication{root: SparseRoot{
			Repository: repository, CandidateManifestDigest: manifestDigest,
			CandidateGenerationDigest: generationDigest,
			PolicyDigest:              testResultDigest(4), Digest: testResultDigest(5),
		}},
		descriptor: descriptor,
		index:      index,
	}
}

func resultTestAuthority() DomainResultAuthority {
	return DomainResultAuthority{
		SourceGenerationDigest:      testResultDigest(6),
		ObservationGenerationDigest: testResultDigest(7),
		ExtractorVersion:            "proto-declarations-v1",
		ExtractionPolicyDigest:      testResultDigest(8),
	}
}

func testResultDigest(value byte) string {
	return "sha256:" + strings.Repeat(string("0123456789abcdef"[value%16]), 64)
}

func leftPadResultOrdinal(value int) string {
	text := fmt.Sprintf("%06d", value)
	return text
}

func distributeResultScalar(
	values []DomainResultTotals,
	total int64,
	set func(*DomainResultTotals, int64),
	perPartition int64,
) {
	remaining := total
	for index := range values {
		amount := min(remaining, perPartition)
		set(&values[index], amount)
		remaining -= amount
	}
	if remaining != 0 {
		panic("test distribution exceeds partition inventory")
	}
}

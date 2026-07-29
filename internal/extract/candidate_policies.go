package extract

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract/scipsource"
)

const (
	protoContractEnumeration  = "proto-contract-paths-v1"
	grpcConsumerEnumeration   = "grpc-consumer-paths-v1"
	thriftContractEnumeration = "thrift-contract-paths-v1"
	thriftConsumerEnumeration = "thrift-consumer-paths-v1"
	scipProtoEnumeration      = "scip-proto-field-paths-v1"
	scipThriftEnumeration     = "scip-thrift-field-paths-v1"
	goCallerEnumeration       = "go-caller-paths-v1"
	kafkaGoEnumeration        = "kafka-go-paths-v1"
	fixedRootSymlinkPolicy    = "fixed-root-regular-v1"
)

// CandidatePolicies is the shared, versioned path-policy registry for the
// trusted census and extraction harness. Enumerate selects the bounded
// repository view an extractor may walk; Required remains the extractor's
// narrower read-every-candidate promise. T30.4 always builds repository-view
// records even when InUnit is false. T30.5 owns unit selection.
func CandidatePolicies(extractors []Extractor) ([]candidate.Policy, error) {
	policies := make([]candidate.Policy, 0, len(extractors))
	seen := make(map[string]struct{}, len(extractors))
	for index, extractor := range extractors {
		if extractor == nil {
			return nil, fmt.Errorf("candidate policy extractor %d is nil", index)
		}
		domain, version, err := extractorMetadata(extractor)
		if err != nil {
			return nil, fmt.Errorf("candidate policy extractor %d: %w", index, err)
		}
		if !validToken(domain) || !validToken(version) {
			return nil, fmt.Errorf(
				"candidate policy extractor %d has invalid domain or version", index)
		}
		if _, duplicate := seen[domain]; duplicate {
			return nil, fmt.Errorf("duplicate candidate policy domain %q", domain)
		}
		seen[domain] = struct{}{}
		policy := candidate.Policy{
			Domain: domain, Version: version,
			Required:      extractor.Candidate,
			Plane:         candidate.PlaneLocal,
			SymlinkPolicy: fixedRootSymlinkPolicy,
			RejectSymlink: fixedRootCandidateSymlink,
		}
		switch domain {
		case "proto-contract":
			policy.EnumerationPolicy = protoContractEnumeration
			policy.Enumerate = hasSuffix(".proto")
		case "grpc-consumer":
			policy.EnumerationPolicy = grpcConsumerEnumeration
			policy.Enumerate = hasSuffix(".go")
		case "thrift-contract":
			policy.EnumerationPolicy = thriftContractEnumeration
			policy.Enumerate = hasSuffix(".thrift")
		case "thrift-consumer":
			policy.EnumerationPolicy = thriftConsumerEnumeration
			policy.Enumerate = hasSuffix(".go")
		case "scip-proto-field":
			policy.EnumerationPolicy = scipProtoEnumeration
			policy.Enumerate = func(filePath string) bool {
				return filePath == scipIndexPath ||
					scipsource.Eligible(scipsource.ProtoField, filePath) ||
					path.Base(filePath) == "buf.yaml"
			}
		case "scip-thrift-field":
			policy.EnumerationPolicy = scipThriftEnumeration
			policy.Enumerate = func(filePath string) bool {
				return filePath == scipIndexPath ||
					scipsource.Eligible(scipsource.Go, filePath)
			}
		case "grpc-caller", "thrift-caller":
			policy.EnumerationPolicy = goCallerEnumeration
			policy.Plane = candidate.PlaneCaller
			policy.Enumerate = callerCandidatePath
		case "kafka-producer", "kafka-consumer":
			policy.EnumerationPolicy = kafkaGoEnumeration
			policy.Enumerate = hasSuffix(".go")
		default:
			return nil, fmt.Errorf(
				"extractor domain %q has no candidate enumeration policy", domain)
		}
		policies = append(policies, policy)
	}
	if _, err := candidate.PolicyIdentities(policies); err != nil {
		return nil, fmt.Errorf("candidate policies: %w", err)
	}
	return policies, nil
}

func hasSuffix(suffix string) func(string) bool {
	return func(filePath string) bool {
		return strings.HasSuffix(filePath, suffix)
	}
}

func callerCandidatePath(filePath string) bool {
	if filePath == scipIndexPath || filePath == "go.mod" ||
		strings.HasSuffix(filePath, "/go.mod") ||
		scipsource.Eligible(scipsource.Go, filePath) {
		return true
	}
	for _, snapshotPath := range attributionSnapshotPaths {
		if filePath == snapshotPath {
			return true
		}
	}
	return false
}

func fixedRootCandidateSymlink(filePath string) bool {
	if filePath == scipIndexPath {
		return true
	}
	for _, snapshotPath := range attributionSnapshotPaths {
		if filePath == snapshotPath {
			return true
		}
	}
	return false
}

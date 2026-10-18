package resolvermaterialize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/sdk"
	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
	"reflect"
)

type consumerUnitPlan struct {
	input    inputBlob
	mappings []extract.ConsumerUnitMapping
	files    map[string]string
}

type consumerUnitRecord struct {
	Schema           string                       `json:"schema"`
	RecordSchema     string                       `json:"record_schema"`
	Pack             string                       `json:"pack"`
	PackVersion      string                       `json:"pack_version"`
	Kind             string                       `json:"kind"`
	State            string                       `json:"state,omitempty"`
	Reason           string                       `json:"reason,omitempty"`
	InputPath        string                       `json:"input_path,omitempty"`
	InputObjectID    string                       `json:"input_object_id,omitempty"`
	InputDigest      string                       `json:"input_digest,omitempty"`
	MappingTotal     int                          `json:"mapping_total,omitempty"`
	ExcludedMappings int                          `json:"excluded_mappings,omitempty"`
	Mapping          *extract.ConsumerUnitMapping `json:"mapping,omitempty"`
	SourceObjectID   string                       `json:"source_object_id,omitempty"`
}

func discoverConsumerUnits(ctx context.Context, file extract.CandidateManifestFile, budget *blobBudget) (*consumerUnitPlan, error) {
	input, err := loadFixedInput(ctx, file, budget, extract.MaxConsumerUnitSnapshotBytes)
	if err != nil {
		return nil, err
	}
	plan := &consumerUnitPlan{input: input, files: make(map[string]string)}
	if !input.present {
		return plan, nil
	}
	if input.reason != "" {
		return nil, fmt.Errorf("unit snapshot: %s", input.reason)
	}
	projection, err := extract.DecodeConsumerUnitSnapshot(ctx, input.content)
	if err != nil {
		return nil, fmt.Errorf("unit snapshot: %w", err)
	}
	plan.mappings = projection.Mappings()
	for _, mapping := range plan.mappings {
		plan.files[mapping.Path] = ""
	}
	return plan, nil
}

func (units *consumerUnitPlan) observe(file extract.CandidateManifestFile, plan *generatedPlan) error {
	if units == nil {
		return nil
	}
	if _, wanted := units.files[file.Path]; !wanted {
		return nil
	}
	// The caller plane is a repository overlay; InUnit=false is not a reason
	// to discard a path already admitted by its exact caller candidate view.
	if !strings.HasSuffix(file.Path, ".go") ||
		(file.SourceLane != candidate.SourceLaneBase && file.SourceLane != candidate.SourceLaneGoTest) {
		return nil
	}
	if file.Mode != "" && file.Mode != "100644" && file.Mode != "100755" {
		return errors.New("unit source candidate is not a regular blob")
	}
	if !gitobj.IsObjectID(file.ObjectID) {
		return errors.New("unit source candidate has no immutable object")
	}
	if plan.layoutProblem != "" {
		return fmt.Errorf("unit source layout: %s", plan.layoutProblem)
	}
	if plan.layout != nil {
		found := false
		for _, root := range plan.layout.Roots {
			if root.Kind == "source" && pathWithinRoot(file.Path, root.Path) {
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	if prior := units.files[file.Path]; prior != "" {
		return errors.New("unit source candidate is duplicated")
	}
	units.files[file.Path] = file.ObjectID
	return nil
}

func unitRecord(kind string) consumerUnitRecord {
	return consumerUnitRecord{Schema: resolvercatalog.RecordSchema, RecordSchema: ConsumerUnitRecordSchema, Pack: GoModulePackName, PackVersion: PackVersion, Kind: kind}
}

func emitConsumerUnits(ctx context.Context, stage *resolvercatalog.Stage, plan *generatedPlan) error {
	metadata, err := encodeMetadata(memberMetadata{Schema: MemberMetadataSchema, Pack: GoModulePackName, PackVersion: PackVersion, RecordSchema: ConsumerUnitRecordSchema, Policy: FrozenMaterializationPolicy()})
	if err != nil {
		return err
	}
	return stage.AddMember(ctx, ConsumerUnitMemberName, metadata, func(write func(json.RawMessage) error) error {
		bytes := 0
		boundedWrite := func(record consumerUnitRecord) error {
			raw, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if len(raw)+1 > MaxConsumerUnitMemberBytes-bytes {
				return resolvercatalog.ErrLimit
			}
			bytes += len(raw) + 1
			return write(raw)
		}
		units := plan.units
		status := unitRecord("consumer_unit_input")
		status.State, status.Reason = StateUnavailable, "unit_snapshot_missing"
		if units.input.present {
			status.State, status.Reason = StateResolved, ""
			status.InputPath, status.InputObjectID, status.InputDigest = units.input.file.Path, units.input.file.ObjectID, units.input.digest
			status.MappingTotal = len(units.mappings)
			for _, mapping := range units.mappings {
				if units.files[mapping.Path] == "" {
					status.ExcludedMappings++
				}
			}
			if status.ExcludedMappings > 0 {
				status.Reason = "outside_admitted_caller_scope"
			}
		}
		if err := boundedWrite(status); err != nil {
			return err
		}
		for _, mapping := range units.mappings {
			objectID := units.files[mapping.Path]
			if objectID == "" {
				continue
			}
			row := unitRecord("consumer_unit_mapping")
			row.Mapping, row.SourceObjectID = &mapping, objectID
			if err := boundedWrite(row); err != nil {
				return err
			}
		}
		return nil
	})
}

type consumerUnitView struct {
	status        *consumerUnitRecord
	mappings      []extract.ConsumerUnitMapping
	objects       map[string]string
	encodedBytes  int
	identityBytes int
}

func (view *consumerUnitView) visit(raw json.RawMessage) error {
	if len(raw)+1 > MaxConsumerUnitMemberBytes-view.encodedBytes {
		return resolvercatalog.ErrLimit
	}
	view.encodedBytes += len(raw) + 1
	var record consumerUnitRecord
	if err := decodeExactRecord(raw, &record); err != nil {
		return err
	}
	if record.Schema != resolvercatalog.RecordSchema || record.RecordSchema != ConsumerUnitRecordSchema || record.Pack != GoModulePackName || record.PackVersion != PackVersion {
		return errors.New("consumer unit record has stale identity")
	}
	switch record.Kind {
	case "consumer_unit_input":
		if view.status != nil || record.Mapping != nil || record.SourceObjectID != "" || record.MappingTotal < 0 || record.MappingTotal > extract.MaxConsumerUnitMappings || record.ExcludedMappings < 0 || record.ExcludedMappings > record.MappingTotal {
			return errors.New("consumer unit status is invalid or duplicated")
		}
		switch record.State {
		case StateUnavailable:
			if record.Reason != "unit_snapshot_missing" || record.InputPath != "" || record.InputObjectID != "" || record.InputDigest != "" || record.MappingTotal != 0 {
				return errors.New("missing consumer unit input has invented authority")
			}
		case StateResolved:
			if record.InputPath != extract.ConsumerUnitSnapshotPath || !gitobj.IsObjectID(record.InputObjectID) || !validSHA256(record.InputDigest) || (record.ExcludedMappings == 0 && record.Reason != "") || (record.ExcludedMappings > 0 && record.Reason != "outside_admitted_caller_scope") {
				return errors.New("consumer unit input authority is invalid")
			}
		default:
			return errors.New("consumer unit input has unsupported state")
		}
		view.status = &record
	case "consumer_unit_mapping":
		if view.status == nil || view.status.State != StateResolved || record.Mapping == nil || !gitobj.IsObjectID(record.SourceObjectID) || record.State != "" || record.Reason != "" || record.InputPath != "" || record.InputObjectID != "" || record.InputDigest != "" || record.MappingTotal != 0 || record.ExcludedMappings != 0 {
			return errors.New("consumer unit mapping has no valid input authority")
		}
		if len(view.mappings) >= extract.MaxConsumerUnitMappings {
			return resolvercatalog.ErrLimit
		}
		charge := extract.ConsumerUnitIdentityBytes(*record.Mapping)
		if charge > extract.MaxConsumerUnitIdentityBytes-view.identityBytes {
			return resolvercatalog.ErrLimit
		}
		view.identityBytes += charge
		if view.objects == nil {
			view.objects = make(map[string]string)
		}
		if prior := view.objects[record.Mapping.Path]; prior != "" && prior != record.SourceObjectID {
			return errors.New("consumer unit source objects conflict")
		}
		view.objects[record.Mapping.Path] = record.SourceObjectID
		view.mappings = append(view.mappings, *record.Mapping)
	default:
		return errors.New("consumer unit record kind is invalid")
	}
	return nil
}

func (view *consumerUnitView) finish(ctx context.Context, publication *resolvercatalog.Publication) (func(string, string) (func(string, int) sdk.ConsumerUnitAttribution, error), error) {
	found := false
	for _, member := range publication.Manifest().Members {
		if member.Name != ConsumerUnitMemberName {
			continue
		}
		var metadata memberMetadata
		if err := decodeExactRecord(member.Metadata, &metadata); err != nil {
			return nil, err
		}
		if metadata.Schema != MemberMetadataSchema || metadata.Pack != GoModulePackName || metadata.PackVersion != PackVersion || metadata.Protocol != "" || metadata.RecordSchema != ConsumerUnitRecordSchema || !reflect.DeepEqual(metadata.Policy, FrozenMaterializationPolicy()) {
			return nil, errors.New("consumer unit member metadata is stale")
		}
		found = true
	}
	if !found || view.status == nil {
		return nil, errors.New("consumer unit member or status is missing")
	}
	if len(view.mappings)+view.status.ExcludedMappings != view.status.MappingTotal {
		return nil, errors.New("consumer unit input census differs")
	}
	if view.mappings == nil {
		view.mappings = []extract.ConsumerUnitMapping{}
	}
	projection, err := extract.OpenConsumerUnitProjection(ctx, view.mappings)
	if err != nil {
		return nil, err
	}
	objects := view.objects
	return func(path, objectID string) (func(string, int) sdk.ConsumerUnitAttribution, error) {
		if expected := objects[path]; expected != "" && expected != objectID {
			return nil, errors.New("consumer unit projection source object changed")
		}
		return projection.ConsumerUnits, nil
	}, nil
}

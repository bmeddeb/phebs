package resolvermaterialize

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/candidate"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/extract"
	"github.com/bmeddeb/phebs/internal/extract/extractors/gocaller"
	"github.com/bmeddeb/phebs/internal/resolvercatalog"
)

// PHEBS-48: package-scoped struct fields for the Thrift caller view. See the
// 2026-11-27 PLAN.md decision record. The projection is all or nothing: a hit
// bound writes one unsupported status row and no field rows.

const packageStructBudgetExhausted = "package_struct_budget_exhausted"

type packageStructKey struct{ directory, packageName string }

// packageStructBounds enforces the frozen projection bounds; tests lower it.
var packageStructBounds = struct {
	sources int
	bytes   int64
	fields  int
}{MaxPackageStructSources, MaxPackageStructSourceBytes, MaxPackageStructFields}

func emitPackageStructs(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	budget *materializationBudget,
	write func(json.RawMessage) error,
) error {
	if current.protocol != ProtocolThrift {
		return nil
	}
	status := packageStructRecord{
		Schema: resolvercatalog.RecordSchema, RecordSchema: PackageStructRecordSchema,
		Pack: current.pack.Name, PackVersion: current.pack.Version,
		Kind: "package_struct_scan", State: StateResolved, Protocol: current.protocol,
	}
	clients := budget.clientTypes[current.protocol]
	if clients.Empty() {
		return writeJSON(write, status)
	}
	rows, exhausted, err := projectPackageStructs(ctx, current, plan, budget, clients)
	if err != nil {
		return err
	}
	if exhausted {
		status.State, status.Reason = StateUnsupported, packageStructBudgetExhausted
		return writeJSON(write, status)
	}
	if err := writeJSON(write, status); err != nil {
		return err
	}
	for _, field := range rows {
		if err := writeJSON(write, packageStructRecord{
			Schema: resolvercatalog.RecordSchema, RecordSchema: PackageStructRecordSchema,
			Pack: current.pack.Name, PackVersion: current.pack.Version,
			Kind: "package_struct_field", State: StateResolved, Protocol: current.protocol,
			Directory: field.Directory, Package: field.Package, Struct: field.Struct,
			Field: field.Field, ImportPath: field.ImportPath, ClientType: field.ClientType,
			SourcePath: field.SourcePath, SourceObjectID: field.SourceObjectID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func projectPackageStructs(
	ctx context.Context,
	current adapter,
	plan *generatedPlan,
	budget *materializationBudget,
	clients *gocaller.ClientTypes,
) ([]gocaller.PackageStructField, bool, error) {
	var files []extract.CandidateManifestFile
	err := plan.manifest.ForEachRepositoryFile(
		ctx, current.callerDomain, current.callerVersion,
		func(file extract.CandidateManifestFile) error {
			if file.SourceLane != candidate.SourceLaneBase || !strings.HasSuffix(file.Path, ".go") {
				return nil
			}
			if _, generated := budget.generatedSourceObjects[file.Path]; generated {
				return nil
			}
			files = append(files, file)
			return nil
		},
	)
	if err != nil {
		return nil, false, fmt.Errorf("discover package struct sources: %w", err)
	}
	if len(files) > packageStructBounds.sources {
		return nil, true, nil
	}
	incomplete := make(map[string]struct{})
	typeNames := make(map[packageStructKey]map[string]int)
	fields := make(map[packageStructKey][]gocaller.PackageStructField)
	var totalBytes int64
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if err := validateInputFile(file); err != nil {
			return nil, false, err
		}
		directory := path.Dir(file.Path)
		if file.DeclaredBytes > int64(gocaller.MaxPackageStructSourceBytes) {
			incomplete[directory] = struct{}{}
			continue
		}
		if file.DeclaredBytes > packageStructBounds.bytes-totalBytes {
			return nil, true, nil
		}
		content, err := plan.blobs.read(ctx, plan.blobs.dir, file.ObjectID, file.DeclaredBytes)
		if err != nil {
			return nil, false, fmt.Errorf("read package struct source %q: %w", file.Path, err)
		}
		totalBytes += int64(len(content))
		if err := dispatchadmission.ObserveProductionResolverBlob(ctx, uint64(len(content))); err != nil {
			return nil, false, err
		}
		if int64(len(content)) != file.DeclaredBytes {
			return nil, false, fmt.Errorf(
				"package struct source %q length changed: candidate=%d blob=%d",
				file.Path, file.DeclaredBytes, len(content),
			)
		}
		described, ok, err := gocaller.DescribePackageStructFile(
			file.Path, file.ObjectID, string(content), clients,
		)
		if err != nil {
			incomplete[directory] = struct{}{}
			continue
		}
		if !ok {
			continue
		}
		key := packageStructKey{directory: directory, packageName: described.Package}
		names := typeNames[key]
		if names == nil {
			names = make(map[string]int)
			typeNames[key] = names
		}
		for _, name := range described.TypeNames {
			names[name]++
		}
		fields[key] = append(fields[key], described.Fields...)
	}
	var rows []gocaller.PackageStructField
	for key, values := range fields {
		if _, dropped := incomplete[key.directory]; dropped {
			continue
		}
		for _, field := range values {
			if typeNames[key][field.Struct] != 1 {
				continue
			}
			rows = append(rows, field)
		}
	}
	if len(rows) > packageStructBounds.fields {
		return nil, true, nil
	}
	sort.Slice(rows, func(i, j int) bool {
		return packageStructSortKey(rows[i]) < packageStructSortKey(rows[j])
	})
	for index := 1; index < len(rows); index++ {
		if packageStructSortKey(rows[index-1]) == packageStructSortKey(rows[index]) {
			return nil, false, errors.New("package struct field is duplicated")
		}
	}
	return rows, false, nil
}

func packageStructSortKey(field gocaller.PackageStructField) string {
	return strings.Join([]string{
		field.Directory, field.Package, field.Struct, field.Field,
		field.ImportPath, field.ClientType, field.SourcePath,
	}, "\x00")
}

func validatePackageStructRecord(
	record packageStructRecord,
	pack resolvercatalog.ResolverPack,
) error {
	if record.Schema != resolvercatalog.RecordSchema ||
		record.RecordSchema != PackageStructRecordSchema ||
		record.Pack != pack.Name || record.PackVersion != pack.Version ||
		record.Protocol != ProtocolThrift {
		return errors.New("caller resolver package struct record identity is invalid")
	}
	switch record.Kind {
	case "package_struct_scan":
		if record.State == StateResolved && record.Reason == "" ||
			record.State == StateUnsupported && record.Reason == packageStructBudgetExhausted {
			return nil
		}
	case "package_struct_field":
		if record.State == StateResolved && record.Reason == "" &&
			record.Directory != "" && record.Package != "" && record.Struct != "" &&
			record.ImportPath != "" && record.ClientType != "" &&
			record.SourcePath != "" && path.Dir(record.SourcePath) == record.Directory &&
			record.SourceObjectID != "" {
			return nil
		}
	}
	return errors.New("caller resolver package struct record is invalid")
}

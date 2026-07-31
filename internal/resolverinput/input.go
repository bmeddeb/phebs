// Package resolverinput defines the committed repository inputs shared by
// resolver-catalog materialization and the shipped extraction fallbacks.
package resolverinput

import (
	"encoding/json"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/module"
)

const (
	// MaxGoModuleBytes is the maximum go.mod size considered by the shipped
	// syntax fallback.
	MaxGoModuleBytes = 4 << 20

	LayoutSnapshotPath        = "layout-snapshot.json"
	GeneratedFromSnapshotPath = "generated-from-snapshot.json"

	LayoutSnapshotVersion        = "t20-layout-snapshot-v1"
	GeneratedFromSnapshotVersion = "t20-generated-from-v1"
)

// ModuleState is the outcome of interpreting a committed go.mod.
type ModuleState string

const (
	ModuleResolved    ModuleState = "resolved"
	ModuleUnavailable ModuleState = "unavailable"
	ModuleAmbiguous   ModuleState = "ambiguous"
	ModuleUnsupported ModuleState = "unsupported"
)

// ModuleReason explains a non-resolved ModuleClassification.
type ModuleReason string

const (
	ModuleMissingDirective   ModuleReason = "missing_module_directive"
	ModuleMultipleDirectives ModuleReason = "multiple_module_directives"
	ModuleMalformedDirective ModuleReason = "malformed_module_directive"
	ModuleFactoredDirective  ModuleReason = "factored_module_directive_unsupported"
	ModuleOversized          ModuleReason = "module_file_oversized"
	ModuleInvalidUTF8        ModuleReason = "module_file_invalid_utf8"
	ModuleInvalidPath        ModuleReason = "invalid_module_path"
)

// ModuleClassification preserves why a committed go.mod cannot supply a
// module path. ModulePath is populated only when State is ModuleResolved.
type ModuleClassification struct {
	State      ModuleState
	Reason     ModuleReason
	ModulePath string
}

// ClassifyGoModule interprets a deliberately small go.mod surface derived from
// the shipped Go caller syntax fallback. Its persisted single-line grammar
// accepts only an unquoted ASCII module-path token or one standard
// double-quoted token. It rejects factored blocks and malformed lexer tokens
// instead of persisting the fallback's legacy literal interpretation; it does
// not implement the broader go.mod grammar or consult the Go toolchain.
func ClassifyGoModule(content string) ModuleClassification {
	return classifyGoModule(content, true)
}

func classifyGoModule(
	content string,
	rejectFactoredDirective bool,
) ModuleClassification {
	if len(content) > MaxGoModuleBytes {
		return ModuleClassification{State: ModuleUnsupported, Reason: ModuleOversized}
	}
	if !utf8.ValidString(content) {
		return ModuleClassification{State: ModuleUnsupported, Reason: ModuleInvalidUTF8}
	}
	modulePath := ""
	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(strings.SplitN(line, "//", 2)[0]))
		if len(fields) == 0 || fields[0] != "module" {
			continue
		}
		if modulePath != "" {
			return ModuleClassification{
				State: ModuleAmbiguous, Reason: ModuleMultipleDirectives,
			}
		}
		if len(fields) != 2 {
			return ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			}
		}
		// A factored module block is legal go.mod syntax, but interpreting its
		// body is outside the deliberately small persisted resolver grammar.
		// Never seal the opening parenthesis as a resolved module identity.
		if rejectFactoredDirective && fields[1] == "(" {
			return ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleFactoredDirective,
			}
		}
		modulePath = fields[1]
		if strings.HasPrefix(modulePath, `"`) {
			unquoted, err := strconv.Unquote(modulePath)
			if err != nil {
				return ModuleClassification{
					State: ModuleUnsupported, Reason: ModuleMalformedDirective,
				}
			}
			modulePath = unquoted
		} else if rejectFactoredDirective && malformedPersistedModuleToken(modulePath) {
			return ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			}
		}
		if !validModulePath(modulePath) ||
			rejectFactoredDirective && module.CheckImportPath(modulePath) != nil {
			return ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleInvalidPath,
			}
		}
	}
	if modulePath == "" {
		return ModuleClassification{
			State: ModuleUnavailable, Reason: ModuleMissingDirective,
		}
	}
	return ModuleClassification{State: ModuleResolved, ModulePath: modulePath}
}

func malformedPersistedModuleToken(value string) bool {
	if strings.ContainsAny(value, "\"'`()[]{},") ||
		strings.Contains(value, "/*") || strings.Contains(value, "*/") {
		return true
	}
	for _, r := range value {
		if !strconv.IsPrint(r) {
			return true
		}
	}
	return false
}

// ParseGoModulePath is the compatibility view used by shipped extraction.
// Its factored-directive behavior deliberately remains the pre-T30.6g
// fallback behavior; changing extractor facts requires its own versioned
// ticket. Persisted resolver records use ClassifyGoModule's stricter surface.
func ParseGoModulePath(content string) (string, bool) {
	result := classifyGoModule(content, false)
	return result.ModulePath, result.State == ModuleResolved
}

// FindGoModule resolves the deepest module root enclosing filePath.
func FindGoModule(
	modules map[string]string,
	filePath string,
) (root, modulePath string, ok bool) {
	directory := path.Dir(filePath)
	for {
		if modulePath, ok := modules[directory]; ok {
			return directory, modulePath, true
		}
		if directory == "." {
			return "", "", false
		}
		directory = path.Dir(directory)
	}
}

// GoImportPath derives the import path used by the shipped generated-Go
// syntax fallback. A vendored file uses its path below vendor independently of
// any module declaration.
func GoImportPath(modules map[string]string, filePath string) string {
	if marker := strings.LastIndex(filePath, "/vendor/"); marker >= 0 {
		return path.Dir(strings.TrimPrefix(
			filePath[marker+len("/vendor/"):], "/",
		))
	}
	if strings.HasPrefix(filePath, "vendor/") {
		return strings.TrimPrefix(path.Dir(filePath), "vendor/")
	}
	moduleDir, modulePath, ok := FindGoModule(modules, filePath)
	if !ok {
		return ""
	}
	directory := path.Dir(filePath)
	if directory == moduleDir {
		return modulePath
	}
	if moduleDir == "." {
		return modulePath + "/" + directory
	}
	return modulePath + "/" + strings.TrimPrefix(directory, moduleDir+"/")
}

func validModulePath(value string) bool {
	if value == "" || len(value) > 1024 || strings.HasPrefix(value, "/") ||
		strings.HasSuffix(value, "/") || strings.Contains(value, `\`) ||
		strings.ContainsAny(value, " \t\r\n\x00") {
		return false
	}
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

type LayoutSnapshot struct {
	Version string       `json:"version"`
	Roots   []LayoutRoot `json:"roots"`
}

type LayoutRoot struct {
	Kind     string `json:"kind"`
	Path     string `json:"path"`
	Protocol string `json:"protocol,omitempty"`
}

type GeneratedFromSnapshot struct {
	Version     string                 `json:"version"`
	Mappings    []GeneratedFromMapping `json:"mappings"`
	Invocations []GeneratorInvocation  `json:"invocations,omitempty"`
}

type GeneratedFromMapping struct {
	Protocol              string `json:"protocol,omitempty"`
	GeneratedPath         string `json:"generated_path"`
	GeneratorRelativePath string `json:"generator_relative_path,omitempty"`
	DeclarationPath       string `json:"declaration_path"`
}

type GeneratorInvocation struct {
	Protocol                string `json:"protocol"`
	GeneratedRoot           string `json:"generated_root"`
	GeneratorInvocationRoot string `json:"generator_invocation_root"`
}

// SnapshotLimits bounds collection allocation while decoding the committed
// layout and generated-from snapshots. A zero limit accepts an empty
// collection and refuses its first element.
type SnapshotLimits struct {
	LayoutRoots          int
	GeneratedMappings    int
	GeneratorInvocations int
}

var (
	ErrLayoutRootLimit          = errors.New("layout snapshot root limit exceeded")
	ErrGeneratedMappingLimit    = errors.New("generated-from snapshot mapping limit exceeded")
	ErrGeneratorInvocationLimit = errors.New("generated-from snapshot invocation limit exceeded")
)

// DecodeLayoutSnapshot decodes exactly one layout snapshot, rejects unknown
// and duplicate object fields, and refuses roots before allocating beyond the
// configured limit.
func DecodeLayoutSnapshot(content string, limits SnapshotLimits) (LayoutSnapshot, error) {
	if limits.LayoutRoots < 0 {
		return LayoutSnapshot{}, errors.New("layout snapshot root limit is negative")
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	var result LayoutSnapshot
	isNull, err := beginSnapshotObject(decoder)
	if err != nil {
		return LayoutSnapshot{}, err
	}
	if !isNull {
		seen := make(map[string]struct{}, 2)
		for decoder.More() {
			field, err := snapshotField(decoder, seen)
			if err != nil {
				return LayoutSnapshot{}, err
			}
			switch field {
			case "version":
				err = decoder.Decode(&result.Version)
			case "roots":
				err = decodeLayoutRoots(decoder, limits.LayoutRoots, &result.Roots)
			default:
				err = unknownSnapshotField(field)
			}
			if err != nil {
				return LayoutSnapshot{}, err
			}
		}
		if err := endSnapshotObject(decoder); err != nil {
			return LayoutSnapshot{}, err
		}
	}
	if err := finishSnapshot(decoder); err != nil {
		return LayoutSnapshot{}, err
	}
	return result, nil
}

// DecodeGeneratedFromSnapshot decodes exactly one generated-from snapshot,
// rejects unknown and duplicate object fields, and refuses mappings and
// invocations before allocating beyond their configured limits.
func DecodeGeneratedFromSnapshot(
	content string,
	limits SnapshotLimits,
) (GeneratedFromSnapshot, error) {
	if limits.GeneratedMappings < 0 || limits.GeneratorInvocations < 0 {
		return GeneratedFromSnapshot{}, errors.New("generated-from snapshot limit is negative")
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	var result GeneratedFromSnapshot
	isNull, err := beginSnapshotObject(decoder)
	if err != nil {
		return GeneratedFromSnapshot{}, err
	}
	if !isNull {
		seen := make(map[string]struct{}, 3)
		for decoder.More() {
			field, err := snapshotField(decoder, seen)
			if err != nil {
				return GeneratedFromSnapshot{}, err
			}
			switch field {
			case "version":
				err = decoder.Decode(&result.Version)
			case "mappings":
				err = decodeGeneratedMappings(
					decoder, limits.GeneratedMappings, &result.Mappings,
				)
			case "invocations":
				err = decodeGeneratorInvocations(
					decoder, limits.GeneratorInvocations, &result.Invocations,
				)
			default:
				err = unknownSnapshotField(field)
			}
			if err != nil {
				return GeneratedFromSnapshot{}, err
			}
		}
		if err := endSnapshotObject(decoder); err != nil {
			return GeneratedFromSnapshot{}, err
		}
	}
	if err := finishSnapshot(decoder); err != nil {
		return GeneratedFromSnapshot{}, err
	}
	return result, nil
}

func decodeLayoutRoots(
	decoder *json.Decoder,
	limit int,
	out *[]LayoutRoot,
) error {
	isNull, err := beginSnapshotArray(decoder)
	if err != nil || isNull {
		return err
	}
	for decoder.More() {
		if len(*out) >= limit {
			return ErrLayoutRootLimit
		}
		var value LayoutRoot
		if err := decodeLayoutRoot(decoder, &value); err != nil {
			return err
		}
		*out = append(*out, value)
	}
	return endSnapshotArray(decoder)
}

func decodeLayoutRoot(decoder *json.Decoder, out *LayoutRoot) error {
	isNull, err := beginSnapshotObject(decoder)
	if err != nil {
		return err
	}
	if isNull {
		return errors.New("layout snapshot root must be an object")
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		field, err := snapshotField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "kind":
			err = decoder.Decode(&out.Kind)
		case "path":
			err = decoder.Decode(&out.Path)
		case "protocol":
			err = decoder.Decode(&out.Protocol)
		default:
			err = unknownSnapshotField(field)
		}
		if err != nil {
			return err
		}
	}
	return endSnapshotObject(decoder)
}

func decodeGeneratedMappings(
	decoder *json.Decoder,
	limit int,
	out *[]GeneratedFromMapping,
) error {
	isNull, err := beginSnapshotArray(decoder)
	if err != nil || isNull {
		return err
	}
	for decoder.More() {
		if len(*out) >= limit {
			return ErrGeneratedMappingLimit
		}
		var value GeneratedFromMapping
		if err := decodeGeneratedMapping(decoder, &value); err != nil {
			return err
		}
		*out = append(*out, value)
	}
	return endSnapshotArray(decoder)
}

func decodeGeneratedMapping(
	decoder *json.Decoder,
	out *GeneratedFromMapping,
) error {
	isNull, err := beginSnapshotObject(decoder)
	if err != nil {
		return err
	}
	if isNull {
		return errors.New("generated-from snapshot mapping must be an object")
	}
	seen := make(map[string]struct{}, 4)
	for decoder.More() {
		field, err := snapshotField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "protocol":
			err = decoder.Decode(&out.Protocol)
		case "generated_path":
			err = decoder.Decode(&out.GeneratedPath)
		case "generator_relative_path":
			err = decoder.Decode(&out.GeneratorRelativePath)
		case "declaration_path":
			err = decoder.Decode(&out.DeclarationPath)
		default:
			err = unknownSnapshotField(field)
		}
		if err != nil {
			return err
		}
	}
	return endSnapshotObject(decoder)
}

func decodeGeneratorInvocations(
	decoder *json.Decoder,
	limit int,
	out *[]GeneratorInvocation,
) error {
	isNull, err := beginSnapshotArray(decoder)
	if err != nil || isNull {
		return err
	}
	for decoder.More() {
		if len(*out) >= limit {
			return ErrGeneratorInvocationLimit
		}
		var value GeneratorInvocation
		if err := decodeGeneratorInvocation(decoder, &value); err != nil {
			return err
		}
		*out = append(*out, value)
	}
	return endSnapshotArray(decoder)
}

func decodeGeneratorInvocation(
	decoder *json.Decoder,
	out *GeneratorInvocation,
) error {
	isNull, err := beginSnapshotObject(decoder)
	if err != nil {
		return err
	}
	if isNull {
		return errors.New("generated-from snapshot invocation must be an object")
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		field, err := snapshotField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "protocol":
			err = decoder.Decode(&out.Protocol)
		case "generated_root":
			err = decoder.Decode(&out.GeneratedRoot)
		case "generator_invocation_root":
			err = decoder.Decode(&out.GeneratorInvocationRoot)
		default:
			err = unknownSnapshotField(field)
		}
		if err != nil {
			return err
		}
	}
	return endSnapshotObject(decoder)
}

func beginSnapshotObject(decoder *json.Decoder) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	if token == nil {
		return true, nil
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return false, errors.New("snapshot value must be an object")
	}
	return false, nil
}

func endSnapshotObject(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("snapshot object is not terminated")
	}
	return nil
}

func beginSnapshotArray(decoder *json.Decoder) (bool, error) {
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	if token == nil {
		return true, nil
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return false, errors.New("snapshot collection must be an array")
	}
	return false, nil
}

func endSnapshotArray(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return errors.New("snapshot array is not terminated")
	}
	return nil
}

func snapshotField(
	decoder *json.Decoder,
	seen map[string]struct{},
) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	field, ok := token.(string)
	if !ok {
		return "", errors.New("snapshot object field name is not a string")
	}
	if _, duplicate := seen[field]; duplicate {
		return "", errors.New("snapshot contains duplicate field " + strconv.Quote(field))
	}
	seen[field] = struct{}{}
	return field, nil
}

func unknownSnapshotField(field string) error {
	return errors.New("json: unknown field " + strconv.Quote(field))
}

func finishSnapshot(decoder *json.Decoder) error {
	_, err := decoder.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("snapshot contains more than one JSON value")
}

// DecodeSnapshot decodes exactly one JSON value and rejects unknown fields.
func DecodeSnapshot(content string, out any) error {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("snapshot contains more than one JSON value")
		}
		return err
	}
	return nil
}

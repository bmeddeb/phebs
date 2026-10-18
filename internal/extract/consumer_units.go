package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/bmeddeb/phebs/internal/extract/sdk"
)

const (
	ConsumerUnitSnapshotPath     = unitSnapshotPath
	MaxConsumerUnitMappings      = maxAttributionMappings
	MaxConsumerUnitIdentityBytes = 16 << 20
	MaxConsumerUnitSnapshotBytes = 4 << 20
)

// ConsumerUnitMapping is one normalized committed mapping. It grants no source
// presence or scope authority: its caller must bind Path to admitted immutable
// candidate metadata before installing this projection in a direct resolver.
type ConsumerUnitMapping struct {
	Path      string                    `json:"path"`
	Line      int                       `json:"line"`
	Candidate sdk.ConsumerUnitCandidate `json:"candidate"`
}

// ConsumerUnitProjection shares the existing snapshot semantics, including IDs,
// line precedence, ambiguity, unavailable mappings and defensive copies. Its
// immutable two-level index bounds lookup work to the selected location.
type ConsumerUnitProjection struct {
	paths map[string]map[int][]unitRecord
}

// DecodeConsumerUnitSnapshot validates syntax and normalizes mappings only.
// Source existence/classification remains the enclosing catalog's exact
// candidate-admission responsibility; this function performs no I/O.
func DecodeConsumerUnitSnapshot(ctx context.Context, content string) (*ConsumerUnitProjection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(content) > MaxConsumerUnitSnapshotBytes {
		return nil, errors.New("consumer unit snapshot exceeds byte bound")
	}
	input, err := decodeConsumerUnitSnapshot(ctx, content)
	if err != nil {
		return nil, err
	}
	if input.Version != unitSnapshotVersion {
		return nil, fmt.Errorf("consumer unit snapshot has unsupported version %q", input.Version)
	}
	return compileConsumerUnits(ctx, input.Mappings)
}

// OpenConsumerUnitProjection revalidates normalized catalog rows with the same
// decoder rules. It rejects noncanonical ordering, changed IDs and values.
func OpenConsumerUnitProjection(ctx context.Context, mappings []ConsumerUnitMapping) (*ConsumerUnitProjection, error) {
	if len(mappings) > MaxConsumerUnitMappings {
		return nil, errors.New("consumer unit mapping count exceeded")
	}
	input := make([]unitMappingInput, 0, len(mappings))
	for _, value := range mappings {
		input = append(input, unitMappingInput{
			SourcePath: value.Path, StartLine: value.Line,
			BuildTargets: value.Candidate.BuildTargets, Deployables: value.Candidate.Deployables,
			LogicalServices: value.Candidate.LogicalServices, Owners: value.Candidate.Owners,
		})
	}
	projection, err := compileConsumerUnits(ctx, input)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(projection.Mappings(), mappings) {
		return nil, errors.New("consumer unit catalog rows are noncanonical")
	}
	return projection, nil
}

func compileConsumerUnits(ctx context.Context, inputs []unitMappingInput) (*ConsumerUnitProjection, error) {
	source := &monorepoAttributionSource{units: make(map[string][]unitRecord)}
	identityBytes := 0
	charge := func(path string, candidate sdk.ConsumerUnitCandidate) error {
		// Conservative per-mapping allowance includes both temporary ordered
		// rows and retained path/line index buckets, OID slot and slice headers.
		amount := ConsumerUnitIdentityBytes(ConsumerUnitMapping{Path: path, Candidate: candidate})
		if amount > MaxConsumerUnitIdentityBytes-identityBytes {
			return errors.New("consumer unit retained identity bound exceeded")
		}
		identityBytes += amount
		return nil
	}
	if err := source.loadUnitsWithPresence(ctx, inputs, func(context.Context, string) (bool, error) { return true, nil }, charge); err != nil {
		return nil, err
	}
	projection := &ConsumerUnitProjection{paths: make(map[string]map[int][]unitRecord, len(source.units))}
	for path, records := range source.units {
		lines := make(map[int][]unitRecord)
		for _, record := range records {
			lines[record.line] = append(lines[record.line], record)
		}
		projection.paths[path] = lines
	}
	return projection, nil
}

func (projection *ConsumerUnitProjection) Mappings() []ConsumerUnitMapping {
	result := []ConsumerUnitMapping{}
	if projection != nil {
		for path, lines := range projection.paths {
			for line, records := range lines {
				for _, record := range records {
					result = append(result, ConsumerUnitMapping{Path: path, Line: line, Candidate: cloneUnitCandidate(record.candidate)})
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Path != result[j].Path {
			return result[i].Path < result[j].Path
		}
		if result[i].Line != result[j].Line {
			return result[i].Line < result[j].Line
		}
		return result[i].Candidate.ID < result[j].Candidate.ID
	})
	return result
}

func (projection *ConsumerUnitProjection) ConsumerUnits(path string, line int) sdk.ConsumerUnitAttribution {
	if checkCorpusPath(path) != nil || line < 1 || line > maxAttributionSourceLine {
		return sdk.ConsumerUnitAttribution{State: sdk.AttributionStateUnavailable, Reason: "invalid_source_location"}
	}
	if projection == nil {
		return sdk.ConsumerUnitAttribution{State: sdk.AttributionStateUnavailable, Reason: "no_unit_mapping"}
	}
	lines := projection.paths[path]
	selected := lines[line]
	if len(selected) == 0 {
		selected = lines[0]
	}
	if len(selected) == 0 {
		return sdk.ConsumerUnitAttribution{State: sdk.AttributionStateUnavailable, Reason: "no_unit_mapping"}
	}
	return consumerUnitAttribution(selected)
}

// ConsumerUnitIdentityBytes conservatively charges per-row retained and
// temporary path/line indexes, OID slot, IDs, vector headers and string data.
func ConsumerUnitIdentityBytes(mapping ConsumerUnitMapping) int {
	amount := 1024 + len(mapping.Path) + len(mapping.Candidate.ID)
	for _, values := range [][]string{mapping.Candidate.BuildTargets, mapping.Candidate.Deployables, mapping.Candidate.LogicalServices, mapping.Candidate.Owners} {
		for _, value := range values {
			amount += 16 + len(value)
		}
	}
	return amount
}

// Each collection refuses its overflow sentinel before decoding or appending
// another mapping/value. Unknown or repeated fields never reach a partial
// projection; only this new catalog decoder has these stricter input checks.
func decodeConsumerUnitSnapshot(ctx context.Context, content string) (unitSnapshotInput, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	var input unitSnapshotInput
	err := consumerUnitObject(decoder, func(name string) error {
		switch name {
		case "version":
			return decoder.Decode(&input.Version)
		case "mappings":
			return consumerUnitArray(decoder, maxAttributionMappings, func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				var mapping unitMappingInput
				if err := consumerUnitObject(decoder, func(field string) error {
					switch field {
					case "call_id":
						return decoder.Decode(&mapping.CallID)
					case "source_path":
						return decoder.Decode(&mapping.SourcePath)
					case "start_line":
						return decoder.Decode(&mapping.StartLine)
					case "state":
						return decoder.Decode(&mapping.State)
					case "source":
						return decoder.Decode(&mapping.Source)
					case "build_targets", "deployables", "logical_services", "owners":
						var values []string
						err := consumerUnitArray(decoder, maxAttributionValues, func() error {
							var value string
							if err := decoder.Decode(&value); err != nil {
								return err
							}
							values = append(values, value)
							return nil
						})
						if err != nil {
							return err
						}
						switch field {
						case "build_targets":
							mapping.BuildTargets = values
						case "deployables":
							mapping.Deployables = values
						case "logical_services":
							mapping.LogicalServices = values
						case "owners":
							mapping.Owners = values
						}
						return nil
					default:
						return fmt.Errorf("consumer unit snapshot has unknown mapping field %q", field)
					}
				}); err != nil {
					return err
				}
				input.Mappings = append(input.Mappings, mapping)
				return nil
			})
		default:
			return fmt.Errorf("consumer unit snapshot has unknown field %q", name)
		}
	})
	if err != nil {
		return unitSnapshotInput{}, fmt.Errorf("decode consumer unit snapshot: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return unitSnapshotInput{}, errors.New("consumer unit snapshot has trailing JSON")
	}
	return input, nil
}

func consumerUnitObject(decoder *json.Decoder, field func(string) error) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return errors.New("consumer unit snapshot expects an object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("consumer unit snapshot has invalid key")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("consumer unit snapshot has duplicate key")
		}
		seen[name] = struct{}{}
		if err := field(name); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('}') {
		return errors.New("consumer unit snapshot has incomplete object")
	}
	return nil
}

func consumerUnitArray(decoder *json.Decoder, maximum int, value func() error) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return nil
	}
	if token != json.Delim('[') {
		return errors.New("consumer unit snapshot expects an array")
	}
	count := 0
	for decoder.More() {
		if count >= maximum {
			return errors.New("consumer unit snapshot collection limit exceeded")
		}
		if err := value(); err != nil {
			return err
		}
		count++
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim(']') {
		return errors.New("consumer unit snapshot has incomplete array")
	}
	return nil
}

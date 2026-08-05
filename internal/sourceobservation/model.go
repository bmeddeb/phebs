// Package sourceobservation defines the bounded, target-independent Go source
// observation contract used by the shared observation plane. It deliberately
// records only relationship-relevant source shapes; it is not a serialized Go
// syntax tree.
package sourceobservation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

const (
	SchemaVersion = "phebs-go-source-observation-v1"
	PolicyVersion = "phebs-go-source-observation-policy-v1"

	MaxSourceBytes      = 4 << 20
	MaxImports          = 512
	MaxFunctions        = 4096
	MaxCalls            = 8192
	MaxTopicSites       = 8192
	MaxBindingsPerValue = 16
	MaxPropagationSteps = 16384
	MaxTextBytes        = 4096
)

var ErrLimit = errors.New("source observation limit exceeded")

type Span struct {
	StartByte int `json:"start_byte"`
	EndByte   int `json:"end_byte"`
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type Import struct {
	Path  string `json:"path"`
	Alias string `json:"alias"`
	Kind  string `json:"kind"` // named | dot | blank
}

type Binding struct {
	ImportPath  string `json:"import_path,omitempty"`
	TypeName    string `json:"type_name,omitempty"`
	Constructor string `json:"constructor,omitempty"`
	Origin      string `json:"origin"` // parameter | receiver | declaration | constructor | propagation
}

type Function struct {
	Name         string `json:"name"`
	ReceiverType string `json:"receiver_type,omitempty"`
	Span         Span   `json:"span"`
	Calls        []Call `json:"calls"`
	Gaps         []Gap  `json:"gaps"`
}

type Call struct {
	Selector      string    `json:"selector"`
	Span          Span      `json:"span"`
	Receiver      string    `json:"receiver"` // resolved | dynamic | unsupported
	Reason        string    `json:"reason,omitempty"`
	Bindings      []Binding `json:"bindings"`
	ArgumentCount int       `json:"argument_count"`
}

type TopicSite struct {
	Plane      string `json:"plane"` // producer | consumer
	Library    string `json:"library"`
	ImportPath string `json:"import_path"`
	Shape      string `json:"shape"`
	State      string `json:"state"`             // resolved | dynamic | unsupported
	Binding    string `json:"binding,omitempty"` // literal | same-file-const
	Topic      string `json:"topic,omitempty"`
	GroupID    string `json:"group_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Span       Span   `json:"span"`
}

type Gap struct {
	Kind   string `json:"kind"` // dynamic | unsupported
	Reason string `json:"reason"`
	Span   Span   `json:"span"`
}

type Observation struct {
	SchemaVersion string      `json:"schema_version"`
	PolicyVersion string      `json:"policy_version"`
	PolicyDigest  string      `json:"policy_digest"`
	ContentDigest string      `json:"content_digest"`
	SourceBytes   int         `json:"source_bytes"`
	Package       string      `json:"package"`
	Imports       []Import    `json:"imports"`
	Functions     []Function  `json:"functions"`
	TopicSites    []TopicSite `json:"topic_sites"`
}

// PolicyDigest binds every observation bound to semantic compatibility. A
// later publication may reuse bytes only when this digest is exact.
func PolicyDigest() string {
	identity := fmt.Sprintf(
		"%s\x00source=%d\x00imports=%d\x00functions=%d\x00calls=%d\x00topics=%d\x00bindings=%d\x00propagation=%d\x00text=%d",
		PolicyVersion, MaxSourceBytes, MaxImports, MaxFunctions, MaxCalls,
		MaxTopicSites, MaxBindingsPerValue, MaxPropagationSteps, MaxTextBytes,
	)
	return contentDigest(identity)
}

func contentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Canonical returns the deterministic closed JSON identity of an observation.
func Canonical(value Observation) ([]byte, error) {
	if err := Validate(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func Validate(value Observation) error {
	if value.SchemaVersion != SchemaVersion || value.PolicyVersion != PolicyVersion ||
		value.PolicyDigest != PolicyDigest() {
		return errors.New("unsupported source observation contract")
	}
	if value.SourceBytes < 0 || value.SourceBytes > MaxSourceBytes {
		return fmt.Errorf("%w: source bytes", ErrLimit)
	}
	if len(value.Package) == 0 || len(value.Package) > MaxTextBytes ||
		len(value.Imports) > MaxImports || len(value.Functions) > MaxFunctions ||
		len(value.TopicSites) > MaxTopicSites {
		return fmt.Errorf("%w: observation shape", ErrLimit)
	}
	if len(value.ContentDigest) != len("sha256:")+64 {
		return errors.New("invalid source content digest")
	}
	if value.ContentDigest[:len("sha256:")] != "sha256:" {
		return errors.New("invalid source content digest")
	}
	if _, err := hex.DecodeString(value.ContentDigest[len("sha256:"):]); err != nil {
		return errors.New("invalid source content digest")
	}
	priorImport := Import{}
	for i, item := range value.Imports {
		if item.Path == "" || len(item.Path) > MaxTextBytes || item.Alias == "" ||
			len(item.Alias) > MaxTextBytes ||
			(item.Kind != "named" && item.Kind != "dot" && item.Kind != "blank") {
			return errors.New("invalid source import")
		}
		if i > 0 && !importLess(priorImport, item) {
			return errors.New("source imports are not canonical")
		}
		priorImport = item
	}
	callCount := 0
	for i := range value.Functions {
		fn := &value.Functions[i]
		if fn.Name == "" || len(fn.Name) > MaxTextBytes || len(fn.ReceiverType) > MaxTextBytes ||
			!validSpan(fn.Span, value.SourceBytes) {
			return errors.New("invalid source function")
		}
		callCount += len(fn.Calls)
		if callCount > MaxCalls {
			return fmt.Errorf("%w: calls", ErrLimit)
		}
		for _, call := range fn.Calls {
			if call.Selector == "" || len(call.Selector) > MaxTextBytes ||
				!validSpan(call.Span, value.SourceBytes) || len(call.Bindings) > MaxBindingsPerValue ||
				len(call.Reason) > MaxTextBytes || call.ArgumentCount < 0 ||
				(call.Receiver != "resolved" && call.Receiver != "dynamic" && call.Receiver != "unsupported") {
				return errors.New("invalid source call")
			}
			if call.Receiver == "resolved" && call.Reason != "" || call.Receiver != "resolved" && call.Reason == "" {
				return errors.New("source call receiver classification is incomplete")
			}
			for index, binding := range call.Bindings {
				if err := validateBinding(binding); err != nil {
					return err
				}
				if index > 0 && !bindingLess(call.Bindings[index-1], binding) {
					return errors.New("source call bindings are not canonical")
				}
			}
		}
		for _, gap := range fn.Gaps {
			if (gap.Kind != "dynamic" && gap.Kind != "unsupported") || gap.Reason == "" ||
				len(gap.Reason) > MaxTextBytes || !validSpan(gap.Span, value.SourceBytes) {
				return errors.New("invalid source gap")
			}
		}
	}
	for _, site := range value.TopicSites {
		if (site.Plane != "producer" && site.Plane != "consumer") ||
			(site.State != "resolved" && site.State != "dynamic" && site.State != "unsupported") ||
			!validSpan(site.Span, value.SourceBytes) || site.Shape == "" || site.Library == "" ||
			len(site.Shape) > MaxTextBytes || len(site.Reason) > MaxTextBytes ||
			len(site.Library) > MaxTextBytes || len(site.ImportPath) > MaxTextBytes ||
			len(site.Topic) > 249 || len(site.GroupID) > MaxTextBytes {
			return errors.New("invalid source topic site")
		}
		if site.State == "resolved" && (site.Topic == "" || site.Reason != "") {
			return errors.New("resolved topic site is incomplete")
		}
		if site.State != "resolved" && (site.Topic != "" || site.Reason == "") {
			return errors.New("unresolved topic site is incomplete")
		}
		if site.Binding != "" && site.Binding != "literal" && site.Binding != "same-file-const" {
			return errors.New("invalid source topic binding")
		}
		if site.State == "resolved" && site.Binding == "" || site.State != "resolved" && site.Binding != "" {
			return errors.New("source topic binding classification is incomplete")
		}
	}
	return nil
}

func validateBinding(value Binding) error {
	if value.ImportPath == "" || len(value.ImportPath) > MaxTextBytes ||
		len(value.TypeName) > MaxTextBytes || len(value.Constructor) > MaxTextBytes ||
		(value.TypeName == "") == (value.Constructor == "") {
		return errors.New("invalid source receiver binding")
	}
	switch value.Origin {
	case "parameter", "receiver", "declaration", "constructor", "propagation":
		return nil
	default:
		return errors.New("invalid source receiver binding origin")
	}
}

func bindingLess(left, right Binding) bool {
	if left.ImportPath != right.ImportPath {
		return left.ImportPath < right.ImportPath
	}
	if left.TypeName != right.TypeName {
		return left.TypeName < right.TypeName
	}
	if left.Constructor != right.Constructor {
		return left.Constructor < right.Constructor
	}
	return left.Origin < right.Origin
}

func validSpan(value Span, sourceBytes int) bool {
	return value.StartByte >= 0 && value.EndByte > value.StartByte &&
		value.EndByte <= sourceBytes && value.StartLine > 0 && value.EndLine >= value.StartLine
}

func importLess(left, right Import) bool {
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.Alias != right.Alias {
		return left.Alias < right.Alias
	}
	return left.Kind < right.Kind
}

func canonicalize(value *Observation) {
	sort.Slice(value.Imports, func(i, j int) bool { return importLess(value.Imports[i], value.Imports[j]) })
	for i := range value.Functions {
		fn := &value.Functions[i]
		if fn.Calls == nil {
			fn.Calls = []Call{}
		}
		if fn.Gaps == nil {
			fn.Gaps = []Gap{}
		}
		for j := range fn.Calls {
			if fn.Calls[j].Bindings == nil {
				fn.Calls[j].Bindings = []Binding{}
			}
			sort.Slice(fn.Calls[j].Bindings, func(a, b int) bool {
				return bindingLess(fn.Calls[j].Bindings[a], fn.Calls[j].Bindings[b])
			})
		}
	}
	if value.Imports == nil {
		value.Imports = []Import{}
	}
	if value.Functions == nil {
		value.Functions = []Function{}
	}
	if value.TopicSites == nil {
		value.TopicSites = []TopicSite{}
	}
}

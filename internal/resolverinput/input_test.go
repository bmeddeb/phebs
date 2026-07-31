package resolverinput

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestClassifyGoModule(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name                 string
		content              string
		want                 ModuleClassification
		compatibilityDiffers bool
		wantCompatibility    ModuleClassification
	}{
		{
			name:    "resolved",
			content: "module example.invalid/root\n\ngo 1.26\n",
			want: ModuleClassification{
				State: ModuleResolved, ModulePath: "example.invalid/root",
			},
		},
		{
			name:    "local main-module import path",
			content: "module c++\n",
			want: ModuleClassification{
				State: ModuleResolved, ModulePath: "c++",
			},
		},
		{
			name:    "missing",
			content: "go 1.26\nrequire example.invalid/dependency v1.0.0\n",
			want: ModuleClassification{
				State: ModuleUnavailable, Reason: ModuleMissingDirective,
			},
		},
		{
			name:    "malformed",
			content: "module example.invalid/root unexpected\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
		},
		{
			name:    "legal factored directive is outside persisted grammar",
			content: "module (\n\texample.invalid/root\n)\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleFactoredDirective,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State: ModuleResolved, ModulePath: "(",
			},
		},
		{
			name:    "single quoted token is malformed persisted syntax",
			content: "module 'example.invalid/root'\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State: ModuleResolved, ModulePath: "'example.invalid/root'",
			},
		},
		{
			name:    "backtick token is malformed persisted syntax",
			content: "module `example.invalid/root`\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State: ModuleResolved, ModulePath: "`example.invalid/root`",
			},
		},
		{
			name:    "embedded double quote is malformed persisted syntax",
			content: "module example.invalid/\"root\"\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State: ModuleResolved, ModulePath: "example.invalid/\"root\"",
			},
		},
		{
			name:    "lexer punctuation is malformed persisted syntax",
			content: "module example.invalid/root)\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State: ModuleResolved, ModulePath: "example.invalid/root)",
			},
		},
		{
			name:    "block comment text is malformed persisted syntax",
			content: "module example.invalid/root/*comment*/suffix\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State:      ModuleResolved,
				ModulePath: "example.invalid/root/*comment*/suffix",
			},
		},
		{
			name:    "control byte is malformed persisted syntax",
			content: "module example.invalid/\x01root\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State: ModuleResolved, ModulePath: "example.invalid/\x01root",
			},
		},
		{
			name: "multiple",
			content: "module example.invalid/first\n" +
				"module example.invalid/second\n",
			want: ModuleClassification{
				State: ModuleAmbiguous, Reason: ModuleMultipleDirectives,
			},
		},
		{
			name:    "quoted",
			content: "module \"example.invalid/quoted\" // comment\n",
			want: ModuleClassification{
				State: ModuleResolved, ModulePath: "example.invalid/quoted",
			},
		},
		{
			name:    "invalid quoted directive",
			content: "module \"example.invalid/unterminated\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleMalformedDirective,
			},
		},
		{
			name:    "invalid module path",
			content: "module example.invalid/../escape\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleInvalidPath,
			},
		},
		{
			name:    "invalid Go import path",
			content: "module example.invalid/root.\n",
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleInvalidPath,
			},
			compatibilityDiffers: true,
			wantCompatibility: ModuleClassification{
				State: ModuleResolved, ModulePath: "example.invalid/root.",
			},
		},
		{
			name:    "invalid UTF-8",
			content: string([]byte{'m', 'o', 'd', 'u', 'l', 'e', ' ', 0xff}),
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleInvalidUTF8,
			},
		},
		{
			name:    "oversized",
			content: strings.Repeat("x", MaxGoModuleBytes+1),
			want: ModuleClassification{
				State: ModuleUnsupported, Reason: ModuleOversized,
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyGoModule(testCase.content); got != testCase.want {
				t.Fatalf("ClassifyGoModule() = %+v, want %+v", got, testCase.want)
			}
			wantCompatibility := testCase.want
			if testCase.compatibilityDiffers {
				wantCompatibility = testCase.wantCompatibility
			}
			gotPath, gotOK := ParseGoModulePath(testCase.content)
			wantOK := wantCompatibility.State == ModuleResolved
			if gotPath != wantCompatibility.ModulePath || gotOK != wantOK {
				t.Fatalf("ParseGoModulePath() = %q, %v; want %q, %v",
					gotPath, gotOK, wantCompatibility.ModulePath, wantOK)
			}
		})
	}
}

func TestFindGoModuleUsesDeepestRoot(t *testing.T) {
	t.Parallel()
	modules := map[string]string{
		".":            "example.invalid/root",
		"services/api": "example.invalid/api",
	}
	testCases := []struct {
		name, filePath, wantRoot, wantModule string
		wantOK                               bool
	}{
		{
			name: "root", filePath: "pkg/client.go", wantRoot: ".",
			wantModule: "example.invalid/root", wantOK: true,
		},
		{
			name: "deepest", filePath: "services/api/gen/client.go",
			wantRoot: "services/api", wantModule: "example.invalid/api", wantOK: true,
		},
		{
			name: "module file", filePath: "services/api/go.mod",
			wantRoot: "services/api", wantModule: "example.invalid/api", wantOK: true,
		},
		{
			name: "missing", filePath: "client.go",
			wantRoot: ".", wantModule: "example.invalid/root", wantOK: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			gotRoot, gotModule, gotOK := FindGoModule(modules, testCase.filePath)
			if gotRoot != testCase.wantRoot || gotModule != testCase.wantModule ||
				gotOK != testCase.wantOK {
				t.Fatalf("FindGoModule(%q) = %q, %q, %v; want %q, %q, %v",
					testCase.filePath, gotRoot, gotModule, gotOK,
					testCase.wantRoot, testCase.wantModule, testCase.wantOK)
			}
		})
	}
}

func TestGoImportPath(t *testing.T) {
	t.Parallel()
	modules := map[string]string{
		".":            "example.invalid/root",
		"services/api": "example.invalid/api",
	}
	testCases := []struct {
		name, filePath, want string
	}{
		{name: "root module", filePath: "client.go", want: "example.invalid/root"},
		{name: "root subdirectory", filePath: "pkg/client.go", want: "example.invalid/root/pkg"},
		{name: "nested module", filePath: "services/api/gen/client.go", want: "example.invalid/api/gen"},
		{name: "root vendor", filePath: "vendor/google.golang.org/grpc/client.go", want: "google.golang.org/grpc"},
		{name: "nested vendor", filePath: "services/api/vendor/example.invalid/lib/client.go", want: "example.invalid/lib"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := GoImportPath(modules, testCase.filePath); got != testCase.want {
				t.Fatalf("GoImportPath(%q) = %q, want %q", testCase.filePath, got, testCase.want)
			}
		})
	}
}

func TestDecodeSnapshotStrict(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name, content string
		newTarget     func() any
		want          any
		wantErr       bool
	}{
		{
			name:      "layout",
			content:   `{"version":"t20-layout-snapshot-v1","roots":[{"kind":"idl","path":"proto","protocol":"grpc"}]}`,
			newTarget: func() any { return &LayoutSnapshot{} },
			want: &LayoutSnapshot{
				Version: LayoutSnapshotVersion,
				Roots:   []LayoutRoot{{Kind: "idl", Path: "proto", Protocol: "grpc"}},
			},
		},
		{
			name: "generated-from",
			content: `{"version":"t20-generated-from-v1","mappings":[` +
				`{"generated_path":"gen/api.pb.go","declaration_path":"proto/api.proto"}],` +
				`"invocations":[{"protocol":"grpc","generated_root":"gen",` +
				`"generator_invocation_root":"proto"}]}`,
			newTarget: func() any { return &GeneratedFromSnapshot{} },
			want: &GeneratedFromSnapshot{
				Version: GeneratedFromSnapshotVersion,
				Mappings: []GeneratedFromMapping{{
					GeneratedPath: "gen/api.pb.go", DeclarationPath: "proto/api.proto",
				}},
				Invocations: []GeneratorInvocation{{
					Protocol: "grpc", GeneratedRoot: "gen",
					GeneratorInvocationRoot: "proto",
				}},
			},
		},
		{
			name:      "unknown field",
			content:   `{"version":"t20-layout-snapshot-v1","roots":[],"extra":true}`,
			newTarget: func() any { return &LayoutSnapshot{} },
			wantErr:   true,
		},
		{
			name:      "trailing value",
			content:   `{"version":"t20-layout-snapshot-v1","roots":[]} {}`,
			newTarget: func() any { return &LayoutSnapshot{} },
			wantErr:   true,
		},
		{
			name:      "malformed",
			content:   `{"version":`,
			newTarget: func() any { return &LayoutSnapshot{} },
			wantErr:   true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			target := testCase.newTarget()
			err := DecodeSnapshot(testCase.content, target)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("DecodeSnapshot() error = %v, wantErr %v", err, testCase.wantErr)
			}
			if !testCase.wantErr && !reflect.DeepEqual(target, testCase.want) {
				t.Fatalf("DecodeSnapshot() = %#v, want %#v", target, testCase.want)
			}
		})
	}
}

func TestDecodeSnapshotsBoundCollectionsDuringDecode(t *testing.T) {
	t.Parallel()
	const limit = 3
	testCases := []struct {
		name, element string
		wrap          func(string) string
		decode        func(string) (int, error)
		wantLimit     error
	}{
		{
			name:    "layout roots",
			element: `{"kind":"source","path":"source"}`,
			wrap: func(elements string) string {
				return `{"version":"t20-layout-snapshot-v1","roots":[` + elements + `]}`
			},
			decode: func(content string) (int, error) {
				value, err := DecodeLayoutSnapshot(
					content, SnapshotLimits{LayoutRoots: limit},
				)
				return len(value.Roots), err
			},
			wantLimit: ErrLayoutRootLimit,
		},
		{
			name: "generated mappings",
			element: `{"generated_path":"gen/api.go",` +
				`"declaration_path":"idl/api.proto"}`,
			wrap: func(elements string) string {
				return `{"version":"t20-generated-from-v1","mappings":[` +
					elements + `]}`
			},
			decode: func(content string) (int, error) {
				value, err := DecodeGeneratedFromSnapshot(content, SnapshotLimits{
					GeneratedMappings: limit, GeneratorInvocations: limit,
				})
				return len(value.Mappings), err
			},
			wantLimit: ErrGeneratedMappingLimit,
		},
		{
			name: "generator invocations",
			element: `{"protocol":"grpc","generated_root":"gen",` +
				`"generator_invocation_root":"idl"}`,
			wrap: func(elements string) string {
				return `{"version":"t20-generated-from-v1","mappings":[],` +
					`"invocations":[` + elements + `]}`
			},
			decode: func(content string) (int, error) {
				value, err := DecodeGeneratedFromSnapshot(content, SnapshotLimits{
					GeneratedMappings: limit, GeneratorInvocations: limit,
				})
				return len(value.Invocations), err
			},
			wantLimit: ErrGeneratorInvocationLimit,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			for _, count := range []int{limit, limit + 1} {
				t.Run(fmt.Sprintf("count_%d", count), func(t *testing.T) {
					t.Parallel()
					elements := strings.TrimSuffix(
						strings.Repeat(testCase.element+",", count), ",",
					)
					gotCount, err := testCase.decode(testCase.wrap(elements))
					if count == limit {
						if err != nil {
							t.Fatal(err)
						}
						if gotCount != limit {
							t.Fatalf("decoded elements = %d, want %d", gotCount, limit)
						}
						return
					}
					if !errors.Is(err, testCase.wantLimit) {
						t.Fatalf("decode error = %v, want %v", err, testCase.wantLimit)
					}
				})
			}
		})
	}
}

func TestDecodeSnapshotsRejectDuplicateAndNonCanonicalFields(t *testing.T) {
	t.Parallel()
	limits := SnapshotLimits{
		LayoutRoots: 4, GeneratedMappings: 4, GeneratorInvocations: 4,
	}
	testCases := []struct {
		name, content string
		decode        func(string) error
	}{
		{
			name: "layout top-level duplicate",
			content: `{"version":"t20-layout-snapshot-v1",` +
				`"version":"t20-layout-snapshot-v1","roots":[]}`,
			decode: func(content string) error {
				_, err := DecodeLayoutSnapshot(content, limits)
				return err
			},
		},
		{
			name: "layout root duplicate",
			content: `{"version":"t20-layout-snapshot-v1","roots":[` +
				`{"kind":"source","path":"one","path":"two"}]}`,
			decode: func(content string) error {
				_, err := DecodeLayoutSnapshot(content, limits)
				return err
			},
		},
		{
			name: "generated top-level duplicate",
			content: `{"version":"t20-generated-from-v1",` +
				`"mappings":[],"mappings":[]}`,
			decode: func(content string) error {
				_, err := DecodeGeneratedFromSnapshot(content, limits)
				return err
			},
		},
		{
			name: "mapping duplicate",
			content: `{"version":"t20-generated-from-v1","mappings":[{` +
				`"generated_path":"gen/one.go","declaration_path":"idl/api.proto",` +
				`"generated_path":"gen/two.go"}]}`,
			decode: func(content string) error {
				_, err := DecodeGeneratedFromSnapshot(content, limits)
				return err
			},
		},
		{
			name: "invocation duplicate",
			content: `{"version":"t20-generated-from-v1","mappings":[],` +
				`"invocations":[{"protocol":"grpc","protocol":"thrift",` +
				`"generated_root":"gen","generator_invocation_root":"idl"}]}`,
			decode: func(content string) error {
				_, err := DecodeGeneratedFromSnapshot(content, limits)
				return err
			},
		},
		{
			name: "case variant is unknown",
			content: `{"version":"t20-generated-from-v1","mappings":[{` +
				`"Generated_Path":"gen/api.go","declaration_path":"idl/api.proto"}]}`,
			decode: func(content string) error {
				_, err := DecodeGeneratedFromSnapshot(content, limits)
				return err
			},
		},
		{
			name:    "trailing value",
			content: `{"version":"t20-layout-snapshot-v1","roots":[]} {}`,
			decode: func(content string) error {
				_, err := DecodeLayoutSnapshot(content, limits)
				return err
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if err := testCase.decode(testCase.content); err == nil {
				t.Fatal("decode succeeded, want strict rejection")
			}
		})
	}
}

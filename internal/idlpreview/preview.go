// Package idlpreview validates bounded caller-owned proposal source without
// publishing it as repository evidence.
package idlpreview

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bufbuild/protocompile/parser"
	"github.com/bufbuild/protocompile/reporter"
	"go.uber.org/thriftrw/idl"

	"github.com/bmeddeb/phebs/internal/idlpreflight"
)

const (
	MaxFiles          = 256
	MaxAggregateBytes = 32 << 20
)

type Source struct {
	Path    string
	Content string
}

// Validate canonicalizes ordering, enforces the production proposal bounds,
// applies the shared lexical preflight, then parses each file independently.
// It performs no include resolution, filesystem read, network call, or code
// generation.
func Validate(
	ctx context.Context,
	protocol string,
	sources []Source,
) error {
	if len(sources) == 0 || len(sources) > MaxFiles {
		return fmt.Errorf("proposal files must contain 1..%d entries", MaxFiles)
	}
	sources = slices.Clone(sources)
	slices.SortFunc(sources, func(left, right Source) int {
		return strings.Compare(left.Path, right.Path)
	})
	totalBytes := 0
	for index, source := range sources {
		if index > 0 && source.Path == sources[index-1].Path {
			return fmt.Errorf("proposal repeats path %q", source.Path)
		}
		if err := validatePath(protocol, source.Path); err != nil {
			return err
		}
		if source.Content == "" || !utf8.ValidString(source.Content) {
			return fmt.Errorf("proposal file %q is not non-empty UTF-8", source.Path)
		}
		if len(source.Content) > idlpreflight.MaxFileBytes {
			return fmt.Errorf(
				"%s: source exceeds %d-byte %s parser limit",
				source.Path,
				idlpreflight.MaxFileBytes,
				protocol,
			)
		}
		totalBytes += len(source.Content)
		if totalBytes > MaxAggregateBytes {
			return fmt.Errorf(
				"proposal source set exceeds %d bytes",
				MaxAggregateBytes,
			)
		}
	}
	for _, source := range sources {
		switch protocol {
		case "protobuf":
			if err := idlpreflight.Validate(
				ctx,
				idlpreflight.Protobuf,
				source.Content,
			); err != nil {
				return fmt.Errorf("%s: %w", source.Path, err)
			}
			handler := reporter.NewHandler(nil)
			node, err := parser.Parse(
				source.Path,
				strings.NewReader(source.Content),
				handler,
			)
			if err != nil {
				return fmt.Errorf("%s: parse: %w", source.Path, err)
			}
			if node == nil {
				return fmt.Errorf("%s: parse returned no file", source.Path)
			}
		case "thrift":
			if err := idlpreflight.Validate(
				ctx,
				idlpreflight.Thrift,
				source.Content,
			); err != nil {
				return fmt.Errorf("%s: %w", source.Path, err)
			}
			node, err := idl.Parse([]byte(source.Content))
			if err != nil {
				return fmt.Errorf("%s: parse: %w", source.Path, err)
			}
			if node == nil {
				return fmt.Errorf("%s: parse returned no file", source.Path)
			}
		default:
			return fmt.Errorf("unsupported proposal protocol %q", protocol)
		}
	}
	return nil
}

func validatePath(protocol, value string) error {
	extension := ".proto"
	if protocol == "thrift" {
		extension = ".thrift"
	} else if protocol != "protobuf" {
		return fmt.Errorf("unsupported proposal protocol %q", protocol)
	}
	if value == "" || len(value) > 4_096 || !utf8.ValidString(value) ||
		path.Clean(value) != value || path.IsAbs(value) ||
		strings.Contains(value, "\\") || !strings.HasSuffix(value, extension) {
		return fmt.Errorf(
			"path %q is not a canonical relative %s path",
			value,
			extension,
		)
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("path %q contains an unsafe segment", value)
		}
	}
	for _, character := range value {
		if character < ' ' || character == 0x7f {
			return errors.New("proposal path contains control characters")
		}
	}
	return nil
}

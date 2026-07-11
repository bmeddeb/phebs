// Package codenav serves precise code navigation from committed SCIP indexes.
package codenav

import (
	"errors"
	"fmt"
	"strings"
)

const (
	DefaultIndexPath = "index.scip"

	// MaxReferenceLocations bounds one response after deterministic selection.
	MaxReferenceLocations = 500

	DefaultMaxIndexBytes       int64 = 64 << 20  // 64 MiB committed protobuf
	DefaultMaxSourceBytes      int64 = 10 << 20  // 10 MiB per converted source file
	DefaultMaxQuerySourceBytes int64 = 32 << 20  // 32 MiB across one lookup
	DefaultMaxCacheBytes       int64 = 512 << 20 // 512 MiB accounted snapshots

	DefaultMaxDocuments     = 100_000
	DefaultMaxOccurrences   = 1_000_000
	DefaultMaxSymbols       = 1_000_000
	DefaultMaxRelationships = 2_000_000
	DefaultMaxHoverBytes    = 64 << 10 // 64 KiB per occurrence/symbol hover
	DefaultMaxHoverParts    = 256
	DefaultMaxSymbolBytes   = 16 << 10 // 16 KiB per SCIP symbol
)

var (
	ErrInvalidInput        = errors.New("invalid code navigation input")
	ErrIndexTooLarge       = errors.New("SCIP index too large")
	ErrSourceTooLarge      = errors.New("source file too large for position conversion")
	ErrQuerySourceBudget   = errors.New("code navigation source budget exceeded")
	ErrCacheBudget         = errors.New("code navigation cache budget exceeded")
	ErrSemanticLimit       = errors.New("SCIP semantic limit exceeded")
	ErrHoverTooLarge       = errors.New("SCIP hover content too large")
	ErrRevisionNotFound    = errors.New("code navigation revision not found")
	ErrUnsupportedEncoding = errors.New("unsupported position encoding")
)

// PositionEncoding identifies the code units used for character offsets.
type PositionEncoding string

const (
	EncodingUTF8  PositionEncoding = "utf8"
	EncodingUTF16 PositionEncoding = "utf16"
	EncodingUTF32 PositionEncoding = "utf32"
)

func normalizeEncoding(encoding PositionEncoding) (PositionEncoding, error) {
	if encoding == "" {
		return EncodingUTF16, nil
	}
	switch PositionEncoding(strings.ToLower(string(encoding))) {
	case EncodingUTF8:
		return EncodingUTF8, nil
	case EncodingUTF16:
		return EncodingUTF16, nil
	case EncodingUTF32:
		return EncodingUTF32, nil
	default:
		return "", fmt.Errorf("encoding %q: %w", encoding, ErrUnsupportedEncoding)
	}
}

type CodePosition struct {
	Line      int32 `json:"line"`
	Character int32 `json:"character"`
}

type CodeRange struct {
	Start CodePosition `json:"start"`
	End   CodePosition `json:"end"`
}

// Compatibility aliases keep callers concise while Huma registers the
// collision-resistant declared schema names above.
type Position = CodePosition
type Range = CodeRange

// Query addresses a position in source at an immutable indexed revision.
// Line and Character are zero-based. Empty Encoding defaults to UTF-16, which
// matches browser and CodeMirror string offsets.
type Query struct {
	Repo      string           `json:"repo"`
	Revision  string           `json:"revision"`
	Path      string           `json:"path"`
	Line      int32            `json:"line"`
	Character int32            `json:"character"`
	Encoding  PositionEncoding `json:"encoding,omitempty"`
}

type Location struct {
	Repo     string           `json:"repo"`
	Revision string           `json:"revision"`
	Path     string           `json:"path"`
	Range    CodeRange        `json:"range"`
	Encoding PositionEncoding `json:"encoding"`
}

// Availability describes the cached SCIP snapshot for a repository revision.
type Availability struct {
	Available   bool   `json:"available"`
	Repo        string `json:"repo"`
	Revision    string `json:"revision"`
	Documents   int    `json:"documents,omitempty"`
	Occurrences int    `json:"occurrences,omitempty"`
}

type DefinitionResult struct {
	Available bool      `json:"available"`
	Symbol    string    `json:"symbol,omitempty"`
	Location  *Location `json:"location,omitempty"`
}

type ReferencesResult struct {
	Available bool       `json:"available"`
	Symbol    string     `json:"symbol,omitempty"`
	Locations []Location `json:"locations"`
	Truncated bool       `json:"truncated,omitempty"`
}

type HoverInfo struct {
	Symbol        string           `json:"symbol"`
	DisplayName   string           `json:"display_name,omitempty"`
	Kind          string           `json:"kind,omitempty"`
	Signature     string           `json:"signature,omitempty"`
	Language      string           `json:"language,omitempty"`
	Documentation []string         `json:"documentation,omitempty"`
	Range         CodeRange        `json:"range"`
	Encoding      PositionEncoding `json:"encoding"`
}

type HoverResult struct {
	Available bool       `json:"available"`
	Hover     *HoverInfo `json:"hover,omitempty"`
}

type Options struct {
	DataDir   string // bare mirrors live beneath DataDir/repos
	IndexPath string // repository-relative; defaults to index.scip

	MaxIndexBytes       int64 // maximum committed SCIP protobuf size
	MaxSourceBytes      int64 // maximum source blob used for range conversion
	MaxQuerySourceBytes int64 // aggregate converted-source bytes per lookup
	MaxCacheBytes       int64 // accounted LRU snapshot budget across repositories

	MaxDocuments     int // semantic records accepted from one index
	MaxOccurrences   int
	MaxSymbols       int
	MaxRelationships int
	MaxHoverBytes    int // merged hover payload bytes per symbol/occurrence
	MaxHoverParts    int // documentation strings per hover
	MaxSymbolBytes   int
}

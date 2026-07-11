package codenav

import (
	"context"
	"fmt"
	"unicode/utf8"
)

func (c *rangeConverter) position(filePath string, line, character int32, from, to PositionEncoding) (int32, error) {
	data, err := c.source(filePath)
	if err != nil {
		return 0, fmt.Errorf("read source for position conversion: %w", err)
	}
	lineBytes, err := sourceLine(data, line)
	if err != nil {
		return 0, err
	}
	return convertCharacter(lineBytes, character, from, to)
}

type rangeConverter struct {
	service             *Service
	ctx                 context.Context
	repo                string
	revision            string
	target              PositionEncoding
	sources             map[string][]byte
	sourceBytes         int64
	maxSourceBytes      int64
	maxQuerySourceBytes int64
}

func newRangeConverter(s *Service, ctx context.Context, repo, revision string, target PositionEncoding) *rangeConverter {
	return &rangeConverter{
		service: s, ctx: ctx, repo: repo, revision: revision, target: target,
		sources: make(map[string][]byte), maxSourceBytes: s.maxSourceBytes,
		maxQuerySourceBytes: s.maxQuerySourceBytes,
	}
}

func (c *rangeConverter) location(location indexedLocation) (Location, error) {
	r, err := c.codeRange(location.path, location.codeRange, location.encoding)
	if err != nil {
		return Location{}, err
	}
	return Location{
		Repo: c.repo, Revision: c.revision, Path: location.path,
		Range: r, Encoding: c.target,
	}, nil
}

func (c *rangeConverter) codeRange(filePath string, r Range, from PositionEncoding) (Range, error) {
	data, err := c.source(filePath)
	if err != nil {
		return Range{}, err
	}
	startLine, err := sourceLine(data, r.Start.Line)
	if err != nil {
		return Range{}, err
	}
	endLine, err := sourceLine(data, r.End.Line)
	if err != nil {
		return Range{}, err
	}
	start, err := convertCharacter(startLine, r.Start.Character, from, c.target)
	if err != nil {
		return Range{}, err
	}
	end, err := convertCharacter(endLine, r.End.Character, from, c.target)
	if err != nil {
		return Range{}, err
	}
	return Range{
		Start: Position{Line: r.Start.Line, Character: start},
		End:   Position{Line: r.End.Line, Character: end},
	}, nil
}

func (c *rangeConverter) source(filePath string) ([]byte, error) {
	if source, ok := c.sources[filePath]; ok {
		return source, nil
	}
	source, err := c.service.readBlob(c.ctx, c.repo, c.revision, filePath, c.maxSourceBytes, ErrSourceTooLarge)
	if err != nil {
		return nil, fmt.Errorf("read %s for range conversion: %w", filePath, err)
	}
	if !utf8.Valid(source) {
		return nil, fmt.Errorf("%s is not UTF-8: %w", filePath, ErrUnsupportedEncoding)
	}
	// Count allocated capacity because the cached slice retains the entire Git
	// output buffer, even when its length is slightly smaller.
	allocated := int64(cap(source))
	if allocated > c.maxQuerySourceBytes-c.sourceBytes {
		return nil, fmt.Errorf("converted source bytes exceed %d: %w", c.maxQuerySourceBytes, ErrQuerySourceBudget)
	}
	c.sourceBytes += allocated
	c.sources[filePath] = source
	return source, nil
}

func sourceLine(source []byte, line int32) ([]byte, error) {
	if line < 0 {
		return nil, fmt.Errorf("negative line: %w", ErrInvalidInput)
	}
	start := 0
	for current := int32(0); current < line; current++ {
		for start < len(source) && source[start] != '\n' {
			start++
		}
		if start >= len(source) {
			return nil, fmt.Errorf("line %d is outside source: %w", line, ErrInvalidInput)
		}
		start++
	}
	end := start
	for end < len(source) && source[end] != '\n' {
		end++
	}
	if start > len(source) {
		return nil, fmt.Errorf("line %d is outside source: %w", line, ErrInvalidInput)
	}
	return source[start:end], nil
}

func convertCharacter(line []byte, character int32, from, to PositionEncoding) (int32, error) {
	if character < 0 {
		return 0, fmt.Errorf("negative character: %w", ErrInvalidInput)
	}
	if !utf8.Valid(line) {
		return 0, fmt.Errorf("source is not UTF-8: %w", ErrUnsupportedEncoding)
	}
	var fromUnits, toUnits int32
	for len(line) > 0 {
		if fromUnits == character {
			return toUnits, nil
		}
		r, size := utf8.DecodeRune(line)
		fromUnits += runeUnits(r, size, from)
		toUnits += runeUnits(r, size, to)
		if fromUnits > character {
			return 0, fmt.Errorf("character %d splits a %s code point: %w", character, from, ErrInvalidInput)
		}
		line = line[size:]
	}
	if fromUnits == character {
		return toUnits, nil
	}
	return 0, fmt.Errorf("character %d is outside line: %w", character, ErrInvalidInput)
}

func runeUnits(r rune, utf8Size int, encoding PositionEncoding) int32 {
	switch encoding {
	case EncodingUTF8:
		return int32(utf8Size)
	case EncodingUTF16:
		if r > 0xffff {
			return 2
		}
		return 1
	case EncodingUTF32:
		return 1
	default:
		return 0
	}
}

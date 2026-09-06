package recovery

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"strings"
)

const (
	restoreReplayBufferBytes = 32 << 10
	restoreReplayRecordLimit = 512
	// The longest observed owned native declaration is 334 bytes. This limit
	// applies only to the closed declaration recipe, never record payloads.
	restoreReplayDefinitionBytes = 1024
	// This is a recognizer capability, not an archive or store admission limit.
	// Ordinary restoration of a deeper native value requires the native path.
	restoreReplayDepthLimit = 64
)

// restoreReplayUnsupported identifies source forms outside the proven native
// export subset. It is not a statement that arbitrary input is valid SurrealQL.
// Only a complete, identity-checked preflight may return this fallback result.
type restoreReplayUnsupported struct {
	Offset int64
	Form   string
}

func (err *restoreReplayUnsupported) Error() string {
	return fmt.Sprintf("native export form unsupported at byte %d: %s", err.Offset, err.Form)
}

type restoreReplaySpan struct{ Start, End int64 }

type restoreReplayUnit struct {
	Span       restoreReplaySpan
	Count      int
	Definition bool
}

type restoreReplayCensus struct {
	Units, Records, Definitions uint64
}

func (census *restoreReplayCensus) add(unit restoreReplayUnit) {
	census.Units++
	if unit.Definition {
		census.Definitions++
	} else {
		census.Records += uint64(unit.Count)
	}
}

// preparedRestoreReplay owns only an inspected file descriptor and a bounded
// recognizer. This is NOT immutable file custody or an execution authority.
// Rereading these offsets for HTTP after recognition would have a parse/use
// race. The executor instead spools bytes as the recognizer consumes them.
type preparedRestoreReplay struct {
	file        *os.File
	path        string
	info        os.FileInfo
	artifact    Artifact
	census      restoreReplayCensus
	scanner     *restoreReplayScanner
	reader      *contextReader
	digest      hash.Hash
	seen        restoreReplayCensus
	terminal    error
	spoolWriter *bufio.Writer
}

// prepareRestoreReplay fully scans and hashes the artifact before returning any
// units. Unsupported syntax still drains and verifies the entire file, so an
// I/O error, cancellation, or observed drift can never select native fallback.
// It does not create or mutate a restore target or change the archive bytes.
func prepareRestoreReplay(ctx context.Context, path string, artifact Artifact) (*preparedRestoreReplay, error) {
	if ctx == nil {
		return nil, errors.New("restore replay context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if artifact.Path != DatabaseName || artifact.Size <= 0 || artifact.Size > maxArtifactBytes || !validSHA256(artifact.SHA256) {
		return nil, errors.New("restore replay artifact identity is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect restore replay artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != artifact.Size {
		return nil, errors.New("restore replay artifact is special or has changed size")
	}
	file, err := openRecoveryRegular(path)
	if err != nil {
		return nil, fmt.Errorf("open restore replay artifact: %w", err)
	}
	prepared := &preparedRestoreReplay{file: file, path: path, info: info, artifact: artifact}
	success := false
	defer func() {
		if !success {
			_ = file.Close()
		}
	}()
	if err := prepared.checkFile(ctx); err != nil {
		return nil, err
	}
	digest := sha256.New()
	stream := io.TeeReader(io.LimitReader(contextReader{ctx: ctx, reader: file}, artifact.Size+1), digest)
	scanner := newRestoreReplayScanner(stream)
	var unsupported *restoreReplayUnsupported
	for {
		unit, err := scanner.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if !errors.As(err, &unsupported) {
				return nil, err
			}
			// Drain through bufio: buffered bytes have already entered the
			// digest, but an error returned beside them may still be pending.
			if err := scanner.drain(); err != nil {
				return nil, fmt.Errorf("finish unsupported restore export inspection: %w", err)
			}
			break
		}
		prepared.census.add(unit)
	}
	if err := prepared.checkFile(ctx); err != nil {
		return nil, err
	}
	if "sha256:"+hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
		return nil, errors.New("restore replay artifact digest changed")
	}
	if unsupported != nil {
		return nil, unsupported
	}
	success = true
	return prepared, nil
}

func (prepared *preparedRestoreReplay) checkFile(ctx context.Context) error {
	if ctx == nil {
		return errors.New("restore replay context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := prepared.file.Stat()
	if err != nil {
		return fmt.Errorf("inspect open restore replay artifact: %w", err)
	}
	named, err := os.Lstat(prepared.path)
	if err != nil {
		return fmt.Errorf("inspect named restore replay artifact: %w", err)
	}
	for _, info := range []os.FileInfo{current, named} {
		if !info.Mode().IsRegular() || !os.SameFile(prepared.info, info) ||
			info.Size() != prepared.info.Size() || info.Mode() != prepared.info.Mode() ||
			!info.ModTime().Equal(prepared.info.ModTime()) {
			return errors.New("restore replay artifact metadata changed")
		}
	}
	return nil
}

// next is single-owner/non-concurrent and re-recognizes the current bytes;
// offsets are never a preflight index. Each call owns its supplied context.
// Any late error is terminal, and never a native-fallback result. A future
// caller that has already submitted work must preserve its failed partial target.
func (prepared *preparedRestoreReplay) next(ctx context.Context) (restoreReplayUnit, error) {
	return prepared.nextCaptured(ctx, nil)
}

func (prepared *preparedRestoreReplay) nextCaptured(ctx context.Context, capture *bufio.Writer) (restoreReplayUnit, error) {
	if prepared.terminal != nil {
		return restoreReplayUnit{}, prepared.terminal
	}
	unit, err := prepared.nextUnit(ctx, capture)
	if err != nil {
		var unsupported *restoreReplayUnsupported
		if errors.As(err, &unsupported) {
			err = errors.New("restore replay source form changed after preflight")
		}
		prepared.terminal = err
	}
	return unit, err
}

func (prepared *preparedRestoreReplay) nextUnit(ctx context.Context, capture *bufio.Writer) (restoreReplayUnit, error) {
	if err := prepared.checkFile(ctx); err != nil {
		return restoreReplayUnit{}, err
	}
	if prepared.scanner == nil {
		if _, err := prepared.file.Seek(0, io.SeekStart); err != nil {
			return restoreReplayUnit{}, fmt.Errorf("rewind restore replay artifact: %w", err)
		}
		prepared.digest = sha256.New()
		prepared.reader = &contextReader{ctx: ctx, reader: prepared.file}
		prepared.scanner = newRestoreReplayScanner(io.TeeReader(
			io.LimitReader(prepared.reader, prepared.artifact.Size+1), prepared.digest,
		))
	}
	prepared.reader.ctx = ctx
	prepared.scanner.capture = capture
	prepared.scanner.captureErr = nil
	defer func() { prepared.scanner.capture = nil }()
	unit, err := prepared.scanner.next()
	if prepared.scanner.captureErr != nil {
		return restoreReplayUnit{}, prepared.scanner.captureErr
	}
	if errors.Is(err, io.EOF) {
		if prepared.seen != prepared.census || "sha256:"+hex.EncodeToString(prepared.digest.Sum(nil)) != prepared.artifact.SHA256 {
			return restoreReplayUnit{}, errors.New("restore replay completed identity differs from preflight")
		}
	}
	if checkErr := prepared.checkFile(ctx); checkErr != nil {
		return restoreReplayUnit{}, checkErr
	}
	if err == nil {
		prepared.seen.add(unit)
	}
	return unit, err
}

func (prepared *preparedRestoreReplay) close() error {
	prepared.terminal = os.ErrClosed
	return prepared.file.Close()
}

// restoreReplayScanner recognizes native literal INSERT arrays and the finite
// owned DEFINE recipe below. It does not parse arbitrary SQL or evaluate values.
type restoreReplayScanner struct {
	reader       *bufio.Reader
	offset       int64
	optionSeen   bool
	insideInsert bool
	capture      *bufio.Writer
	captureErr   error
}

func newRestoreReplayScanner(reader io.Reader) *restoreReplayScanner {
	return &restoreReplayScanner{reader: bufio.NewReaderSize(reader, restoreReplayBufferBytes)}
}

func (scanner *restoreReplayScanner) drain() error {
	// Do not let bufio.WriterTo delegate to Discard.ReaderFrom on its raw
	// source: that shortcut bypasses an error pending beside buffered bytes.
	_, err := io.Copy(io.Discard, struct{ io.Reader }{scanner.reader})
	return err
}

func (scanner *restoreReplayScanner) unsupported(form string) error {
	return &restoreReplayUnsupported{Offset: scanner.offset, Form: form}
}

func (scanner *restoreReplayScanner) peek() (byte, error) {
	if scanner.captureErr != nil {
		return 0, scanner.captureErr
	}
	raw, err := scanner.reader.Peek(1)
	if err != nil {
		return 0, err
	}
	return raw[0], nil
}

func (scanner *restoreReplayScanner) take() (byte, error) {
	value, err := scanner.reader.ReadByte()
	if err == nil {
		scanner.offset++
		if scanner.capture != nil {
			if writeErr := scanner.capture.WriteByte(value); writeErr != nil {
				scanner.captureErr = writeErr
				return 0, writeErr
			}
		}
	}
	return value, err
}

func (scanner *restoreReplayScanner) expect(want byte) error {
	value, err := scanner.take()
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	if err != nil {
		return err
	}
	if value != want {
		return scanner.unsupported("literal delimiter")
	}
	return nil
}

func (scanner *restoreReplayScanner) space() error {
	for {
		value, err := scanner.peek()
		if err != nil {
			return err
		}
		if value != ' ' && value != '\t' && value != '\r' && value != '\n' {
			return nil
		}
		_, _ = scanner.take()
	}
}

func (scanner *restoreReplayScanner) trivia() error {
	for {
		if err := scanner.space(); err != nil {
			return err
		}
		value, _ := scanner.peek()
		if value != '-' {
			return nil
		}
		if err := scanner.expect('-'); err != nil {
			return err
		}
		if err := scanner.expect('-'); err != nil {
			return err
		}
		for {
			value, err := scanner.take()
			if err != nil {
				return err
			}
			if value == '\n' {
				break
			}
		}
	}
}

func (scanner *restoreReplayScanner) keyword(word string) error {
	for i := range len(word) {
		if err := scanner.expect(word[i]); err != nil {
			return err
		}
	}
	return nil
}

func (scanner *restoreReplayScanner) next() (restoreReplayUnit, error) {
	unit, err := scanner.scanNext()
	if errors.Is(err, io.EOF) && scanner.insideInsert {
		err = io.ErrUnexpectedEOF
	}
	return unit, err
}

func (scanner *restoreReplayScanner) scanNext() (restoreReplayUnit, error) {
	var unit restoreReplayUnit
	if !scanner.insideInsert {
		if err := scanner.trivia(); err != nil {
			if errors.Is(err, io.EOF) && !scanner.optionSeen {
				return unit, io.ErrUnexpectedEOF
			}
			return unit, err
		}
		if !scanner.optionSeen {
			if err := scanner.keyword("OPTION IMPORT;"); err != nil {
				return unit, err
			}
			scanner.optionSeen = true
			if err := scanner.trivia(); err != nil {
				return unit, err
			}
		}
		if value, _ := scanner.peek(); value == 'D' {
			start := scanner.offset
			if err := scanner.definition(); err != nil {
				return unit, err
			}
			unit.Span = restoreReplaySpan{Start: start, End: scanner.offset}
			unit.Definition = true
			unit.Count = 1
			return unit, nil
		} else if value != 'I' {
			return unit, scanner.unsupported("unproven statement")
		}
		if err := scanner.keyword("INSERT"); err != nil {
			return unit, err
		}
		scanner.insideInsert = true
		if err := scanner.space(); err != nil {
			return unit, err
		}
		if err := scanner.expect('['); err != nil {
			return unit, err
		}
	}
	for unit.Count < restoreReplayRecordLimit {
		if err := scanner.space(); err != nil {
			return unit, err
		}
		start := scanner.offset
		if value, _ := scanner.peek(); value != '{' {
			return unit, scanner.unsupported("non-object export record")
		}
		if err := scanner.object(0); err != nil {
			return unit, err
		}
		if unit.Count == 0 {
			unit.Span.Start = start
		}
		unit.Span.End = scanner.offset
		unit.Count++
		if err := scanner.space(); err != nil {
			return unit, err
		}
		value, _ := scanner.peek()
		switch value {
		case ',':
			_, _ = scanner.take()
		case ']':
			_, _ = scanner.take()
			if err := scanner.space(); err != nil {
				return unit, err
			}
			if err := scanner.expect(';'); err != nil {
				return unit, err
			}
			scanner.insideInsert = false
			return unit, nil
		default:
			return unit, scanner.unsupported("export array delimiter")
		}
	}
	return unit, nil
}

func (scanner *restoreReplayScanner) quoted() error {
	quote, err := scanner.take()
	if err != nil {
		return err
	}
	for {
		value, err := scanner.take()
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		if err != nil {
			return err
		}
		if value == quote {
			return nil
		}
		if value == '\\' {
			if _, err := scanner.take(); err != nil {
				if errors.Is(err, io.EOF) {
					return io.ErrUnexpectedEOF
				}
				return err
			}
		}
	}
}

func restoreReplayIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func restoreReplayDigit(value byte) bool { return value >= '0' && value <= '9' }

func (scanner *restoreReplayScanner) identifier() error {
	value, err := scanner.peek()
	if err != nil {
		return err
	}
	if value == '`' || value == '\'' || value == '"' {
		return scanner.quoted()
	}
	if !restoreReplayIdentifierStart(value) {
		return scanner.unsupported("identifier")
	}
	for {
		value, err := scanner.peek()
		if err != nil || !restoreReplayIdentifierStart(value) && !restoreReplayDigit(value) {
			return err
		}
		_, _ = scanner.take()
	}
}

func (scanner *restoreReplayScanner) object(depth int) error {
	if depth == restoreReplayDepthLimit {
		return scanner.unsupported("literal nesting depth")
	}
	if err := scanner.expect('{'); err != nil {
		return err
	}
	if err := scanner.space(); err != nil {
		return err
	}
	if value, _ := scanner.peek(); value == '}' {
		return scanner.expect('}')
	}
	for {
		if err := scanner.identifier(); err != nil {
			return err
		}
		if err := scanner.space(); err != nil {
			return err
		}
		if err := scanner.expect(':'); err != nil {
			return err
		}
		if err := scanner.value(depth + 1); err != nil {
			return err
		}
		if err := scanner.space(); err != nil {
			return err
		}
		value, _ := scanner.peek()
		if value == '}' {
			return scanner.expect('}')
		}
		if err := scanner.expect(','); err != nil {
			return err
		}
		if err := scanner.space(); err != nil {
			return err
		}
	}
}

func (scanner *restoreReplayScanner) value(depth int) error {
	if depth == restoreReplayDepthLimit {
		return scanner.unsupported("literal nesting depth")
	}
	if err := scanner.space(); err != nil {
		return err
	}
	value, _ := scanner.peek()
	switch {
	case value == '{':
		return scanner.object(depth)
	case value == '[':
		_, _ = scanner.take()
		if err := scanner.space(); err != nil {
			return err
		}
		if value, _ := scanner.peek(); value == ']' {
			return scanner.expect(']')
		}
		for {
			if err := scanner.value(depth + 1); err != nil {
				return err
			}
			if err := scanner.space(); err != nil {
				return err
			}
			if value, _ := scanner.peek(); value == ']' {
				return scanner.expect(']')
			}
			if err := scanner.expect(','); err != nil {
				return err
			}
		}
	case value == '\'' || value == '"':
		return scanner.quoted()
	case value == '-' || value == '+' || restoreReplayDigit(value):
		return scanner.number()
	case restoreReplayIdentifierStart(value) || value == '`':
		// At most five bytes distinguish literal keywords; identifiers themselves
		// are streamed without a length cap or a retained token allocation.
		var word [5]byte
		length := 0
		if value == '`' {
			if err := scanner.quoted(); err != nil {
				return err
			}
			length = len(word) + 1
		} else {
			for {
				value, err := scanner.peek()
				if err != nil {
					return err
				}
				if !restoreReplayIdentifierStart(value) && !restoreReplayDigit(value) {
					break
				}
				if length < len(word) {
					word[length] = value
				}
				if length <= len(word) {
					length++
				}
				_, _ = scanner.take()
			}
		}
		value, _ := scanner.peek()
		if length == 1 && (word[0] == 'd' || word[0] == 'u' || word[0] == 'r') && (value == '\'' || value == '"') {
			return scanner.quoted()
		}
		if value == ':' {
			_, _ = scanner.take()
			value, err := scanner.peek()
			if err != nil {
				return err
			}
			if restoreReplayDigit(value) {
				return scanner.digits()
			}
			return scanner.identifier()
		}
		if length <= len(word) {
			switch string(word[:length]) {
			case "true", "false", "NULL", "NONE":
				return nil
			}
		}
		return scanner.unsupported("nonliteral value")
	default:
		return scanner.unsupported("nonliteral value")
	}
}

func (scanner *restoreReplayScanner) digits() error {
	count := false
	for {
		value, err := scanner.peek()
		if err != nil {
			return err
		}
		if !restoreReplayDigit(value) {
			if !count {
				return scanner.unsupported("numeric literal")
			}
			return nil
		}
		count = true
		_, _ = scanner.take()
	}
}

func (scanner *restoreReplayScanner) number() error {
	value, _ := scanner.peek()
	signed := value == '+' || value == '-'
	if signed {
		_, _ = scanner.take()
	}
	if err := scanner.digits(); err != nil {
		return err
	}
	value, _ = scanner.peek()
	decimal := false
	if value == 'd' {
		peeked, err := scanner.reader.Peek(3)
		if err != nil {
			return err
		}
		decimal = string(peeked) == "dec"
	}
	if !signed && !decimal && (value == 'n' || value == 'u' || value == 'm' || value == 's' || value == 'h' || value == 'd' || value == 'w' || value == 'y') {
		for {
			value, _ = scanner.take()
			switch value {
			case 'n', 'u':
				if err := scanner.expect('s'); err != nil {
					return err
				}
			case 'm':
				next, err := scanner.peek()
				if err != nil {
					return err
				}
				if next == 's' {
					_, _ = scanner.take()
				}
			case 's', 'h', 'd', 'w', 'y':
			default:
				return scanner.unsupported("duration unit")
			}
			next, err := scanner.peek()
			if err != nil {
				return err
			}
			if !restoreReplayDigit(next) {
				return nil
			}
			if err := scanner.digits(); err != nil {
				return err
			}
		}
	}
	if value == '.' {
		_, _ = scanner.take()
		if err := scanner.digits(); err != nil {
			return err
		}
	}
	if value, _ = scanner.peek(); value == 'e' || value == 'E' {
		_, _ = scanner.take()
		if value, _ = scanner.peek(); value == '-' || value == '+' {
			_, _ = scanner.take()
		}
		if err := scanner.digits(); err != nil {
			return err
		}
	}
	if value, _ = scanner.peek(); value == 'f' {
		_, _ = scanner.take()
	} else if value == 'd' {
		return scanner.keyword("dec")
	}
	return nil
}

// These exact tails and guard statements are native output from our own schema
// and migrations (SurrealDB 3.2.0), not a grammar for arbitrary expressions.
// The opt-in full-owned-export census must be rerun when source/engine changes.
func (scanner *restoreReplayScanner) definition() error {
	var raw [restoreReplayDefinitionBytes]byte
	length := 0
	ended := false
	for {
		value, err := scanner.take()
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if errors.Is(err, io.EOF) || value == '\n' {
			ended = errors.Is(err, io.EOF)
			break
		}
		if length == len(raw) {
			return scanner.unsupported("owned declaration byte capacity")
		}
		raw[length] = value
		length++
	}
	line := string(raw[:length])
	if !strings.HasSuffix(line, ";") {
		if ended {
			return io.ErrUnexpectedEOF
		}
		return scanner.unsupported("multiline declaration recipe")
	}
	if !restoreReplayOwnedDefinition(line) {
		return scanner.unsupported("unproven declaration recipe")
	}
	return nil
}

func restoreReplayOwnedName(name string, path bool) bool {
	for part := range strings.SplitSeq(name, ".") {
		if part == "*" && path {
			continue
		}
		if len(part) > 2 && part[0] == '`' && part[len(part)-1] == '`' {
			part = part[1 : len(part)-1]
		}
		if part == "" || !restoreReplayIdentifierStart(part[0]) {
			return false
		}
		for index := range len(part) {
			if !restoreReplayIdentifierStart(part[index]) && !restoreReplayDigit(part[index]) {
				return false
			}
		}
		if !path && part != name && "`"+part+"`" != name {
			return false
		}
	}
	return name != ""
}

func restoreReplayOwnedDefinition(line string) bool {
	if rest, ok := strings.CutPrefix(line, "DEFINE TABLE "); ok {
		name, tail, ok := strings.Cut(rest, " TYPE ")
		return ok && restoreReplayOwnedName(name, false) &&
			(tail == "ANY SCHEMALESS PERMISSIONS NONE;" || tail == "NORMAL SCHEMAFULL PERMISSIONS NONE;")
	}
	if rest, ok := strings.CutPrefix(line, "DEFINE FIELD OVERWRITE "); ok {
		name, rest, ok := strings.Cut(rest, " ON ")
		if !ok || !restoreReplayOwnedName(name, true) {
			return false
		}
		table, tail, ok := strings.Cut(rest, " TYPE ")
		if !ok || !restoreReplayOwnedName(table, false) {
			return false
		}
		tail, ok = strings.CutSuffix(tail, " PERMISSIONS FULL;")
		return ok && restoreReplayOwnedFieldTail(tail)
	}
	if rest, ok := strings.CutPrefix(line, "DEFINE INDEX "); ok {
		name, rest, ok := strings.Cut(rest, " ON ")
		if !ok || !restoreReplayOwnedName(name, false) {
			return false
		}
		table, fields, ok := strings.Cut(rest, " FIELDS ")
		if !ok || !restoreReplayOwnedName(table, false) {
			return false
		}
		fields, ok = strings.CutSuffix(fields, ";")
		if !ok {
			return false
		}
		fields = strings.TrimSuffix(fields, " UNIQUE")
		for field := range strings.SplitSeq(fields, ", ") {
			if !restoreReplayOwnedName(field, true) {
				return false
			}
		}
		return fields != ""
	}
	return restoreReplayOwnedEvent(line)
}

func restoreReplayOwnedFieldTail(tail string) bool {
	switch tail {
	case "array<object> ASSERT array::len($value) <= 16",
		"array<object> ASSERT array::len($value) <= 16384",
		"array<string>",
		"array<string> ASSERT array::len($value) <= 4000",
		"array<string> DEFAULT [] ASSERT $value = [] OR $value = ['investigation:write']",
		"bool",
		"bool DEFAULT false",
		"datetime",
		"int",
		"int ASSERT $value > 0 AND $value <= 1000000",
		"int ASSERT $value > 0 AND $value <= 4096",
		"int ASSERT $value > 0 AND $value <= 8",
		"int ASSERT $value > 0 AND $value <= 80000000",
		"int ASSERT $value >= 0",
		"int ASSERT $value >= 0 AND $value < 64",
		"int ASSERT $value >= 0 AND $value < 8",
		"int ASSERT $value >= 0 AND $value <= 12500",
		"int ASSERT $value >= 0 AND $value <= 25000",
		"int ASSERT $value >= 0 AND $value <= 33554432",
		"int ASSERT $value >= 0 AND $value <= 4000",
		"int ASSERT $value >= 0 AND $value <= 512",
		"int ASSERT $value >= 0 AND $value <= 64",
		"int ASSERT $value >= 0 AND $value <= 8",
		"int ASSERT $value >= 0 AND $value <= 98",
		"int ASSERT $value >= 1",
		"int ASSERT $value >= 1 AND $value <= 129",
		"int ASSERT $value >= 1 AND $value <= 16777216",
		"int ASSERT $value >= 1 AND $value <= 2097152",
		"int ASSERT $value >= 1 AND $value <= 262144",
		"int ASSERT $value INSIDE [0, 1, 2]",
		"int DEFAULT 1 ASSERT $value >= 1",
		"none | array<object>",
		"none | bool",
		"none | datetime",
		"none | int",
		"none | int ASSERT $value = NONE OR $value >= 0",
		"none | object",
		"none | string",
		"object",
		"string",
		"string ASSERT $value = 'phebs-caller-generation-publication-store-v1'",
		"string ASSERT $value = 'phebs-caller-leaf-store-v1'",
		"string ASSERT $value = 'phebs-extraction-domain-outcome-v1'",
		"string ASSERT $value = 'phebs-generation-schedule-v1'",
		"string ASSERT $value = 'phebs-service-catalog-publication-v1'",
		"string ASSERT $value = 'phebs-service-state-repository-v1'",
		"string ASSERT $value = 'phebs-service-state-v1'",
		"string ASSERT $value INSIDE ['accepted', 'proposal', 'conflict', 'rejected']",
		"string ASSERT $value INSIDE ['accepted', 'rejected', 'completed', 'reopened', 'waived']",
		"string ASSERT $value INSIDE ['active', 'superseded', 'settled']",
		"string ASSERT $value INSIDE ['admitted', 'terminal_generation_refusal']",
		"string ASSERT $value INSIDE ['base', 'override']",
		"string ASSERT $value INSIDE ['committed', 'operator', 'analysis-unit-v1']",
		"string ASSERT $value INSIDE ['cpu', 'io', 'memory', 'extraction']",
		"string ASSERT $value INSIDE ['current', 'desired', 'active']",
		"string ASSERT $value INSIDE ['current', 'stale', 'unavailable', 'conflict', 'removed']",
		"string ASSERT $value INSIDE ['draft', 'active', 'concluded', 'archived']",
		"string ASSERT $value INSIDE ['historical', 'collecting']",
		"string ASSERT $value INSIDE ['investigation', 'baseline', 'dossier']",
		"string ASSERT $value INSIDE ['pending', 'claimed', 'running', 'done', 'failed', 'canceled']",
		"string ASSERT $value INSIDE ['pending', 'running', 'done', 'failed', 'canceled']",
		"string ASSERT $value INSIDE ['phebs-resolver-catalog-store-v1', 'phebs-resolver-catalog-store-v2']",
		"string ASSERT $value INSIDE ['published', 'failed', 'canceled']",
		"string ASSERT $value INSIDE ['published', 'unavailable_prerequisite', 'terminal_generation_refusal', 'retryable_failure']",
		"string ASSERT $value INSIDE ['queued', 'enumerating', 'analyzing', 'publishing', 'published', 'failed', 'canceled']",
		"string ASSERT $value INSIDE ['reader']",
		"string ASSERT $value INSIDE ['reconcile', 'activate']",
		"string ASSERT $value INSIDE ['revocation', 'mandatory_deletion', 'legal_policy', 'approved_retention']",
		"string ASSERT $value INSIDE ['running', 'reconciled', 'activated', 'failed', 'superseded']",
		"string ASSERT $value INSIDE ['service', 'placement']",
		"string ASSERT $value INSIDE ['staged', 'published', 'aborted']",
		"string ASSERT $value INSIDE ['staged', 'published', 'superseded', 'aborted', 'deleting'] OR $this.evidence_format_version != 't12-evidence-v1'",
		"string ASSERT $value INSIDE ['succeeded', 'terminal_generation_refusal']",
		"string ASSERT $value INSIDE ['v2', 'v3']",
		"string ASSERT $value NOTINSIDE ['t12-store-v1', 't12-store-v2', 't12-store-v3', 't12-store-v4', 't12-store-v5', 't12-store-v6', 't12-store-v9', 't12-store-v8', 't12-store-v7']",
		"string ASSERT string::len($value) <= 8192",
		"string ASSERT string::len($value) = 71 AND string::starts_with($value, 'sha256:') AND string::is_hexadecimal(string::slice($value, 7)) AND string::lowercase($value) = $value":
		return true
	default:
		return false
	}
}

func restoreReplayOwnedEvent(line string) bool {
	switch line {
	case "DEFINE EVENT api_key_capabilities_immutable ON api_key WHEN $event = 'UPDATE' AND $before.capabilities != NONE AND $before.capabilities != $after.capabilities THEN { THROW 'phebs-permanent: API key capabilities are immutable' };":
		return true
	case "DEFINE EVENT caller_generation_admission_writer_v1 ON caller_generation_admission WHEN $event != 'DELETE' AND ($after.writer_schema ?? '') != 'phebs-caller-leaf-store-v1' THEN { THROW 'phebs-permanent: retired caller-leaf writer generation' };":
		return true
	case "DEFINE EVENT caller_generation_publication_writer_v1 ON caller_generation_publication WHEN $event != 'DELETE' AND ($after.writer_schema ?? '') != 'phebs-caller-generation-publication-store-v1' THEN { THROW 'phebs-permanent: retired caller-generation publication writer generation' };":
		return true
	case "DEFINE EVENT caller_leaf_outcome_writer_v1 ON caller_leaf_outcome WHEN $event != 'DELETE' AND ($after.writer_schema ?? '') != 'phebs-caller-leaf-store-v1' THEN { THROW 'phebs-permanent: retired caller-leaf writer generation' };":
		return true
	case "DEFINE EVENT extraction_run_writer_v10 ON extraction_run WHEN $event != 'DELETE' AND $after.store_schema_version INSIDE ['t12-store-v1', 't12-store-v2', 't12-store-v3', 't12-store-v4', 't12-store-v5', 't12-store-v6', 't12-store-v9', 't12-store-v8', 't12-store-v7'] THEN { THROW 'phebs-permanent: retired evidence writer generation' };":
		return true
	default:
		return false
	}
}

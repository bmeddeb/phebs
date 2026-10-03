package provider

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"

	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedsandbox"
)

const resultGzipSchema = "phebs-bazel-worker-result-gzip-v1"
const resultWireChunk = 32 << 10

// The envelope preserves the exact canonical Result-v1 JSON plus its LF. Its
// physical bytes spend the original shared allowance; decoded bytes never
// substitute for supervisor stdout/stderr or completion accounting.
type resultGzip struct {
	Schema       string `json:"schema"`
	DecodedBytes int64  `json:"decoded_bytes"`
	Gzip         []byte `json:"gzip"`
}

func encodeResultWire(ctx context.Context, raw []byte, remaining int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if remaining <= 0 || remaining > typedsandbox.OutputBytes || len(raw) == 0 || len(raw) >= typedsandbox.OutputBytes {
		return nil, typedindex.Capacity
	}
	if int64(len(raw))+1 <= remaining {
		return append(raw, '\n'), nil
	}
	frame := resultGzip{Schema: resultGzipSchema, DecodedBytes: int64(len(raw)) + 1, Gzip: []byte{}}
	empty, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	// JSON encodes the gzip bytes as padded base64; reserve its exact wrapper and
	// LF first, and stop compression before allocating an untransmittable payload.
	limit := 3 * ((remaining - int64(len(empty)) - 1) / 4)
	if limit <= 0 {
		return nil, typedindex.Capacity
	}
	compressed := resultGzipBuffer{ctx: ctx, limit: limit}
	z, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(raw); start += resultWireChunk {
		end := min(start+resultWireChunk, len(raw))
		if _, err = z.Write(raw[start:end]); err != nil {
			_ = z.Close()
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			_ = z.Close()
			return nil, err
		}
	}
	if _, err = z.Write([]byte{'\n'}); err != nil {
		_ = z.Close()
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	frame.Gzip = compressed.Bytes()
	wire, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(wire))+1 > remaining {
		return nil, typedindex.Capacity
	}
	return append(wire, '\n'), nil
}

type resultGzipBuffer struct {
	bytes.Buffer
	ctx   context.Context
	limit int64
}

func (b *resultGzipBuffer) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > b.limit-int64(b.Len()) {
		return 0, typedindex.Capacity
	}
	return b.Buffer.Write(p)
}

// DecodeResultWire unwraps only the bounded transport. It establishes no source,
// request, phase, plan, SCIP, sandbox completion or publication authority; every
// controller still passes the returned evidence through DecodeResult. remaining
// is the original physical worker allowance, after prior stdout/stderr spending.
func DecodeResultWire(ctx context.Context, raw []byte, remaining int64) ([]byte, error) {
	logical, _, err := decodeResultWire(ctx, raw, remaining)
	return logical, classify(err)
}

func decodeResultWire(ctx context.Context, raw []byte, remaining int64) ([]byte, bool, error) {
	if ctx == nil {
		return nil, false, typedindex.Invalid
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if remaining <= 0 || remaining > typedsandbox.OutputBytes || len(raw) == 0 || int64(len(raw)) > remaining {
		return nil, false, typedindex.Capacity
	}
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`{"schema":"`+resultGzipSchema+`",`)) {
		return raw, false, nil
	}
	if err := resultDimensions(ctx, raw); err != nil {
		return nil, true, err
	}
	var frame resultGzip
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&frame) != nil {
		return nil, true, typedindex.Invalid
	}
	canonical, err := json.Marshal(frame)
	if err != nil || len(raw) != len(canonical)+1 || raw[len(raw)-1] != '\n' || !bytes.Equal(raw[:len(raw)-1], canonical) || frame.Schema != resultGzipSchema || frame.DecodedBytes <= remaining || frame.DecodedBytes > typedsandbox.OutputBytes || len(frame.Gzip) == 0 {
		return nil, true, typedindex.Invalid
	}
	input := &resultGzipInput{Reader: bytes.NewReader(frame.Gzip), ctx: ctx}
	z, err := gzip.NewReader(input)
	if err != nil {
		if err = ctx.Err(); err != nil {
			return nil, true, err
		}
		return nil, true, typedindex.Invalid
	}
	defer func() { _ = z.Close() }()
	z.Multistream(false)
	logical := make([]byte, int(frame.DecodedBytes))
	for offset := 0; offset < len(logical); {
		if err = ctx.Err(); err != nil {
			return nil, true, err
		}
		n, readErr := z.Read(logical[offset:min(offset+resultWireChunk, len(logical))])
		offset += n
		if readErr != nil && readErr != io.EOF || n == 0 && readErr == nil || readErr == io.EOF && offset != len(logical) {
			if err = ctx.Err(); err != nil {
				return nil, true, err
			}
			return nil, true, typedindex.Invalid
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, true, err
	}
	var extra [1]byte
	n, err := z.Read(extra[:])
	if canceled := ctx.Err(); canceled != nil {
		return nil, true, canceled
	}
	if n != 0 {
		return nil, true, typedindex.Capacity
	}
	if err != io.EOF || input.Len() != 0 || len(logical) < 3 || logical[0] != '{' || logical[len(logical)-2] != '}' || logical[len(logical)-1] != '\n' {
		return nil, true, typedindex.Invalid
	}
	if err = ctx.Err(); err != nil {
		return nil, true, err
	}
	return logical, true, nil
}

// ByteReader prevents gzip from consuming a following stream or trailing bytes
// through buffered read-ahead. Both header and compressed reads check cancellation.
type resultGzipInput struct {
	*bytes.Reader
	ctx context.Context
}

func (r *resultGzipInput) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
func (r *resultGzipInput) ReadByte() (byte, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.ReadByte()
}

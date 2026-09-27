package typedsandbox

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
)

func (c *client) attach(ctx context.Context, id string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "http://docker"+apiVersion+"/containers/"+id+"/attach?stream=1&stdout=1&stderr=1", nil)
	if err != nil {
		return nil, ErrRefused
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, ErrRefused
	}
	if response.StatusCode != 101 && response.StatusCode != 200 {
		_ = response.Body.Close()
		return nil, ErrRefused
	}
	return response.Body, nil
}

type wireResult struct {
	stdout, stderr []byte
	err            error
	payloadBytes   int64
}

func readWire(reader io.Reader) wireResult { return readWireLimit(reader, maxWireBytes) }
func readWireLimit(reader io.Reader, limit int64) wireResult {
	if limit < 0 || limit > maxWireBytes {
		return wireResult{err: ErrExecution}
	}
	var stdout, stderr bytes.Buffer
	var header [8]byte
	for {
		_, err := io.ReadFull(reader, header[:])
		if err == io.EOF {
			return wireResult{stdout: stdout.Bytes(), stderr: stderr.Bytes(), payloadBytes: int64(stdout.Len() + stderr.Len())}
		}
		if err != nil || header[0] != 1 && header[0] != 2 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return wireResult{err: ErrExecution}
		}
		length := uint64(binary.BigEndian.Uint32(header[4:]))
		if length == 0 || length > uint64(limit-int64(stdout.Len())-int64(stderr.Len())) {
			return wireResult{err: ErrExecution}
		}
		writer := &stdout
		if header[0] == 2 {
			writer = &stderr
		}
		if _, err = io.CopyN(writer, reader, int64(length)); err != nil {
			return wireResult{err: ErrExecution}
		}
	}
}

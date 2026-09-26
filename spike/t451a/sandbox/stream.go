package sandbox

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
}

func readWire(reader io.Reader) wireResult {
	var stdout, stderr bytes.Buffer
	var header [8]byte
	for {
		_, err := io.ReadFull(reader, header[:])
		if err == io.EOF {
			return wireResult{stdout.Bytes(), stderr.Bytes(), nil}
		}
		if err != nil || header[0] != 1 && header[0] != 2 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return wireResult{err: ErrExecution}
		}
		length := uint64(binary.BigEndian.Uint32(header[4:]))
		if length == 0 || length > uint64(maxWireBytes-stdout.Len()-stderr.Len()) {
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

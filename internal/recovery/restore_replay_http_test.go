package recovery

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRestoreReplayImportResponse(t *testing.T) {
	const insert = `{"result":[],"status":"OK","time":"2.14825ms","type":null}`
	const commit = `{"result":null,"status":"OK","time":"4.239833ms","type":null}`
	const failed = `{"result":"native failure","status":"ERR","time":"0ns","type":null,"kind":"Thrown"}`
	for _, test := range []struct {
		name, body string
		definition bool
		status     int
		wantOK     bool
	}{
		{"insert", "[" + insert + "," + commit + "]", false, 200, true},
		{"definition", "[" + commit + "," + commit + "]", true, 200, true},
		{"HTTP failure", "[" + insert + "," + commit + "]", false, 500, false},
		{"write failed", "[" + failed + "," + commit + "]", false, 200, false},
		{"commit failed", "[" + insert + "," + failed + "]", false, 200, false},
		{"both failed", "[" + failed + "," + failed + "]", false, 200, false},
		{"missing commit", "[" + insert + "]", false, 200, false},
		{"extra result", "[" + insert + "," + commit + "," + commit + "]", false, 200, false},
		{"truncated", "[" + insert + ",", false, 200, false},
		{"trailing", "[" + insert + "," + commit + "] []", false, 200, false},
		{"wrong commit result", "[" + insert + "," + insert + "]", false, 200, false},
		{"wrong write result", "[" + commit + "," + commit + "]", false, 200, false},
		{"unknown status", "[" + strings.ReplaceAll(insert, "OK", "UNKNOWN") + "," + commit + "]", false, 200, false},
		{"unknown field", "[" + strings.ReplaceAll(insert, "\"type\":null", "\"type\":null,\"unknown\":1") + "," + commit + "]", false, 200, false},
		{"duplicate status", "[" + strings.ReplaceAll(insert, "\"status\":\"OK\"", "\"status\":\"ERR\",\"status\":\"OK\"") + "," + commit + "]", false, 200, false},
		{"duplicate result", "[" + strings.ReplaceAll(insert, "\"result\":[]", "\"result\":[],\"result\":[]") + "," + commit + "]", false, 200, false},
		{"missing type", "[" + strings.ReplaceAll(insert, ",\"type\":null", "") + "," + commit + "]", false, 200, false},
		{"oversize", strings.Repeat(" ", maxCommandOutput+1), false, 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &restoreReplayResponseBody{Reader: strings.NewReader(test.body)}
			err := readRestoreReplayImportResponse(&http.Response{StatusCode: test.status, Body: body}, test.definition)
			if test.wantOK != (err == nil) || !body.closed {
				t.Fatalf("response error=%v body_closed=%t", err, body.closed)
			}
		})
	}
}

type restoreReplayResponseBody struct {
	io.Reader
	closed bool
}

func (body *restoreReplayResponseBody) Close() error { body.closed = true; return nil }

func TestRestoreReplayBootstrapResponse(t *testing.T) {
	const ok = `{"result":null,"status":"OK","time":"0ns","type":null}`
	const failed = `{"result":"failed","status":"ERR","time":"0ns","type":null}`
	for _, test := range []struct {
		body   string
		wantOK bool
	}{
		{"[" + ok + "," + ok + "," + ok + "]", true},
		{"[" + failed + "," + ok + "," + ok + "]", false},
		{"[" + ok + "," + failed + "," + ok + "]", false},
		{"[" + ok + "," + ok + "," + failed + "]", false},
		{"[" + ok + "," + ok + "]", false},
		{"[" + ok + "," + ok + "," + ok + "," + ok + "]", false},
	} {
		body := &restoreReplayResponseBody{Reader: strings.NewReader(test.body)}
		err := readRestoreReplayResponse(&http.Response{StatusCode: http.StatusOK, Body: body}, true, true)
		if (err == nil) != test.wantOK || !body.closed {
			t.Fatalf("bootstrap response error=%v body_closed=%t", err, body.closed)
		}
	}
}

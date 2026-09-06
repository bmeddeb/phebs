package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// executeRestoreReplay is the ordinary offline restore path for the proven
// native export subset. There is no native CLI fallback after this boundary.
// Its census is not a durable attempted-work meter: parent-owned attempt ACKs
// remain a separate prerequisite for phase-wide hard-death evidence.
func executeRestoreReplay(ctx context.Context, prepared *preparedRestoreReplay, target, endpoint string, database DatabaseIdentity) (resultErr error) {
	if prepared == nil {
		return errors.New("native replay preparation is required")
	}
	if prepared.terminal != nil {
		return prepared.terminal
	}
	defer func() {
		if resultErr != nil {
			prepared.terminal = resultErr
		}
	}()
	if !restoreReplayNonblockingAvailable {
		return errors.New("protected native replay is unavailable on this platform")
	}
	if database != (DatabaseIdentity{Namespace: "phebs", Database: "phebs"}) {
		return errors.New("native replay database differs from the fixed archive identity")
	}
	address, err := url.Parse(cliEndpoint(endpoint))
	if err != nil || address.Scheme != "http" || address.User != nil || address.Path != "" ||
		address.RawQuery != "" || address.Fragment != "" || address.Port() == "" {
		return errors.New("native import endpoint is not the local runtime endpoint")
	}
	if ip := net.ParseIP(address.Hostname()); ip == nil || !ip.IsLoopback() {
		return errors.New("native import endpoint must be a loopback IP")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(target, ".database-replay-")
	if err != nil {
		return fmt.Errorf("create private import spool directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.Remove(directory)) }()
	transport := &http.Transport{MaxConnsPerHost: 1, MaxIdleConnsPerHost: 1, DisableCompression: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	address.Path = "/sql"
	if err := bootstrapRestoreReplay(ctx, client, address.String()); err != nil {
		return err
	}
	address.Path = "/import"
	for {
		file, unit, err := prepared.spoolNext(ctx, directory)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("prepare native import unit: %w", err)
		}
		err = submitRestoreReplayUnit(ctx, client, address.String(), database, file, unit)
		if err := errors.Join(err, file.Close()); err != nil {
			return fmt.Errorf("native import unit %d: %w", prepared.seen.Units, err)
		}
	}
}

// Both source-owned metadata definitions are real writes, each in its own
// explicit transaction with one submitted definition. They are not setup-
// exempt work. No application tables or records are created by this bootstrap.
func bootstrapRestoreReplay(ctx context.Context, client *http.Client, endpoint string) error {
	for _, kind := range [...]string{"NAMESPACE", "DATABASE"} {
		database := DatabaseIdentity{}
		if kind == "DATABASE" {
			database.Namespace = "phebs"
		}
		body := "BEGIN;\nDEFINE " + kind + " IF NOT EXISTS phebs;\nCOMMIT;"
		if err := submitRestoreReplayRequest(ctx, client, endpoint, database, strings.NewReader(body), int64(len(body)), true, true); err != nil {
			return fmt.Errorf("native import %s bootstrap: %w", kind, err)
		}
	}
	return nil
}

func submitRestoreReplayUnit(ctx context.Context, client *http.Client, endpoint string, database DatabaseIdentity, file *os.File, unit restoreReplayUnit) error {
	prefix, suffix := "OPTION IMPORT; BEGIN;\n", "\nCOMMIT;"
	if !unit.Definition {
		prefix += "INSERT ["
		suffix = "];" + suffix
	}
	body := io.MultiReader(
		strings.NewReader(prefix), io.NewSectionReader(file, unit.Span.Start, unit.Span.End-unit.Span.Start), strings.NewReader(suffix),
	)
	size := int64(len(prefix)+len(suffix)) + unit.Span.End - unit.Span.Start
	return submitRestoreReplayRequest(ctx, client, endpoint, database, body, size, unit.Definition, false)
}

func submitRestoreReplayRequest(ctx context.Context, client *http.Client, endpoint string, database DatabaseIdentity, source io.Reader, size int64, definition, bootstrap bool) error {
	body := &restoreReplayRequestBody{reader: contextReader{ctx: ctx, reader: source}, done: make(chan struct{})}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return fmt.Errorf("create native import request: %w", err)
	}
	request.ContentLength = size
	request.SetBasicAuth("root", "root")
	if database.Namespace != "" {
		request.Header.Set("Surreal-NS", database.Namespace)
	}
	if database.Database != "" {
		request.Header.Set("Surreal-DB", database.Database)
	}
	request.Header.Set("Accept", "application/json")
	// POST has no GetBody or idempotency header. No application replay or
	// redirect is permitted after an ambiguous HTTP failure.
	response, err := client.Do(request)
	if err == nil {
		err = readRestoreReplayResponse(response, definition, bootstrap)
	}
	// Transport owns Close even on error. Join its current body read before
	// the caller closes the readonly spool, including early HTTP refusals.
	<-body.done
	if err != nil {
		return fmt.Errorf("submit native import unit: %w", err)
	}
	return nil
}

type restoreReplayRequestBody struct {
	mu     sync.Mutex
	reader io.Reader
	closed bool
	done   chan struct{}
}

func (body *restoreReplayRequestBody) Read(raw []byte) (int, error) {
	body.mu.Lock()
	defer body.mu.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	return body.reader.Read(raw)
}

func (body *restoreReplayRequestBody) Close() error {
	body.mu.Lock()
	defer body.mu.Unlock()
	if !body.closed {
		body.closed = true
		close(body.done)
	}
	return nil
}

// readRestoreReplayImportResponse validates the pinned native /import result,
// not just HTTP status: an aborted transaction also returns HTTP 200. One
// source-owned write statement and its COMMIT must both finish successfully.
// OPTION IMPORT and BEGIN do not add entries to the observed native result.
func readRestoreReplayImportResponse(response *http.Response, definition bool) error {
	return readRestoreReplayResponse(response, definition, false)
}

func readRestoreReplayResponse(response *http.Response, definition, bootstrap bool) error {
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, maxCommandOutput+1))
	if err := errors.Join(readErr, response.Body.Close()); err != nil {
		return fmt.Errorf("read native import response: %w", err)
	}
	if len(raw) > maxCommandOutput {
		return errors.New("native import response exceeds its limit")
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("native import HTTP status %d", response.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return errors.New("native import result array is invalid")
	}
	statements := 2
	if bootstrap {
		// Unlike /import, the pinned /sql route returns a BEGIN result too.
		statements = 3
	}
	for index := range statements {
		want := "null"
		if index == 0 && !definition {
			want = "[]"
		}
		if err := readRestoreReplayStatementResult(decoder, want); err != nil {
			return fmt.Errorf("native import statement %d: %w", index, err)
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return errors.New("native transaction has an unexpected result count")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("native import response has trailing content")
	}
	return nil
}

// Only the four observed success fields are accepted, once each. This fixed
// two/three-result grammar rejects duplicate keys, unknown/error metadata, omitted
// nulls, and extra results without constructing an array of native results.
func readRestoreReplayStatementResult(decoder *json.Decoder, want string) error {
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return errors.New("missing statement result")
	}
	var seen uint8
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return errors.New("invalid result field")
		}
		var bit uint8
		switch key {
		case "status":
			bit = 1
		case "time":
			bit = 2
		case "type":
			bit = 4
		case "result":
			bit = 8
		default:
			return errors.New("unknown or failed native result metadata")
		}
		if seen&bit != 0 {
			return errors.New("duplicate native result field")
		}
		seen |= bit
		if bit <= 2 {
			var value string
			if err := decoder.Decode(&value); err != nil {
				return errors.New("invalid native result text")
			}
			if bit == 1 && value != "OK" {
				return errors.New("native statement did not succeed")
			}
			if bit == 2 {
				if elapsed, err := time.ParseDuration(value); err != nil || elapsed < 0 {
					return errors.New("native statement time is invalid")
				}
			}
		} else {
			var value json.RawMessage
			if err := decoder.Decode(&value); err != nil ||
				(bit == 4 && string(bytes.TrimSpace(value)) != "null") ||
				(bit == 8 && string(bytes.TrimSpace(value)) != want) {
				return errors.New("native write or COMMIT result is unknown")
			}
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || seen != 15 {
		return errors.New("native result fields are incomplete")
	}
	return nil
}

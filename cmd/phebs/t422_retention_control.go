package main

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sync"
	"time"

	"github.com/bmeddeb/phebs/internal/auth"
	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/internal/focusedindex"
	"github.com/bmeddeb/phebs/internal/lifecycle"
	"github.com/bmeddeb/phebs/internal/readaccounting"
	"github.com/bmeddeb/phebs/internal/repositoryindex"
	"github.com/bmeddeb/phebs/internal/search"
	"github.com/sourcegraph/zoekt/query"
)

const (
	t422RetentionPinPath    = "/api/t422/retention/pin"
	t422RetentionReadPath   = "/api/t422/retention/current-prior"
	t422RetentionProbePath  = "structural/cells/b000/c00000/f000.go"
	t422RetentionBodyBytes  = 16 << 10
	t422RetentionFinalBytes = 1 << 20
)

var errT422RetentionControl = errors.New("T42.2 current-prior retention control refused")

// One epoch-one control carries only the actual warm F identity and one real
// pin. The pin is acquired in phase four before B is authored, not relabeled
// from warm work. Native readers exist only within the one request's lifetime.
type t422RetentionControl struct {
	ctx           context.Context
	cancel        context.CancelFunc
	launch        *t422SemanticLaunch
	owner         lifecycle.SearchGenerationOwnerImpl
	pins          *focusedindex.SearchGenerationPins
	mu            sync.Mutex
	old           string
	files         uint64
	pin           *focusedindex.SearchGenerationLease
	step          uint8
	busy          bool
	closed        bool
	err           error
	pinnedAt      int64
	turns         uint64
	deleted       uint64
	maxDeleted    uint64
	sink          func([]byte) error
	stopContext   func() bool
	contextJoined chan struct{}
	closeOnce     sync.Once
}

type t422RetentionSweep struct {
	Attempt      uint64 `json:"attempt"`
	Scanned      int    `json:"scanned"`
	Deleted      int    `json:"deleted"`
	More         bool   `json:"more"`
	Completeness string `json:"completeness"`
	Failed       bool   `json:"failed"`
}

type t422RetentionObservation struct {
	Schema                      string             `json:"schema"`
	OldSearchGenerationSHA256   string             `json:"old_search_generation_sha256"`
	NewSearchGenerationSHA256   string             `json:"new_search_generation_sha256"`
	QuerySHA256                 string             `json:"query_sha256"`
	OldProjectionSHA256         string             `json:"old_projection_sha256"`
	NewProjectionSHA256         string             `json:"new_projection_sha256"`
	PostReleaseProjectionSHA256 string             `json:"post_release_projection_sha256"`
	OldRecords                  uint64             `json:"old_records"`
	NewRecords                  uint64             `json:"new_records"`
	PostReleaseRecords          uint64             `json:"post_release_records"`
	PinnedAtUnixNano            int64              `json:"pinned_at_unix_nano"`
	ReleasedAtUnixNano          int64              `json:"released_at_unix_nano"`
	Held                        t422RetentionSweep `json:"held"`
	Released                    t422RetentionSweep `json:"released"`
	OldReaderHeldThroughReprobe bool               `json:"old_reader_held_through_reprobe"`
}

func newT422RetentionControl(ctx context.Context, launch *t422SemanticLaunch, owner lifecycle.SearchGenerationOwnerImpl,
	pins *focusedindex.SearchGenerationPins,
) (*t422RetentionControl, error) {
	current, err := dispatchadmission.ProductionSemanticState()
	if ctx == nil || ctx.Err() != nil || launch == nil || launch.fail == nil || launch.request.ServerEpoch != 1 ||
		launch.initial.Phase != 2 || err != nil || !launch.matches(current) || pins == nil || owner.Pins != pins ||
		owner.Acquire == nil || !filepath.IsAbs(owner.IndexDir) || filepath.Clean(owner.IndexDir) != owner.IndexDir {
		return nil, errT422RetentionControl
	}
	lifetime, cancel := context.WithCancel(ctx)
	control := &t422RetentionControl{ctx: lifetime, cancel: cancel, launch: launch, owner: owner, pins: pins,
		sink: t4013ExactReportSink("exact retention turn: "), contextJoined: make(chan struct{})}
	control.stopContext = context.AfterFunc(lifetime, func() { control.releasePin(); close(control.contextJoined) })
	return control, nil
}

func (control *t422RetentionControl) releasePin() {
	control.mu.Lock()
	pin := control.pin
	control.pin = nil
	control.mu.Unlock()
	pin.Release()
}

// Main calls Close after stopping background owners. Cancellation also releases
// the pin; a failed request closes its own readers before its report tail.
func (control *t422RetentionControl) Close() {
	if control == nil {
		return
	}
	control.closeOnce.Do(func() {
		control.mu.Lock()
		control.closed = true
		control.mu.Unlock()
		control.cancel()
		if !control.stopContext() {
			<-control.contextJoined
		}
		control.releasePin()
	})
}

func (control *t422RetentionControl) stop() error {
	control.mu.Lock()
	first := control.err == nil
	control.err = errT422RetentionControl
	control.mu.Unlock()
	control.releasePin()
	if first {
		control.launch.fail(errT422RetentionControl)
	}
	return errT422RetentionControl
}

func (control *t422RetentionControl) current(ctx context.Context, phase uint32) bool {
	if ctx == nil || ctx.Err() != nil || control.ctx.Err() != nil {
		return false
	}
	control.mu.Lock()
	failed := control.err != nil || control.closed
	control.mu.Unlock()
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	current, err := dispatchadmission.ProductionSemanticState()
	return !failed && ok && err == nil && admitted.Phase == phase && admitted.OrdinaryOwnersDrained &&
		current.OrdinaryOwnersDrained && control.launch.sameRequest(admitted, current)
}

// This consumes only main's already-produced canonical F response. It performs
// no native read and captures no identity until the complete F report succeeds.
func (control *t422RetentionControl) finalTail(ctx context.Context, canonical []byte) (func(error), error) {
	admitted, ok := ctx.Value(t422SemanticRequestKey{}).(dispatchadmission.ProductionSemanticSnapshot)
	if !ok || admitted.Phase != 3 {
		return nil, nil
	}
	if !control.current(ctx, 3) || len(canonical) > t422RetentionFinalBytes {
		return nil, control.stop()
	}
	var value struct {
		Schema    string `json:"schema"`
		Authority struct {
			Search    string               `json:"search_generation_sha256"`
			Inventory t421FinalSetIdentity `json:"search_inventory"`
			Current   bool                 `json:"current"`
		} `json:"authority"`
	}
	if json.Unmarshal(canonical, &value) != nil || value.Schema != t421FinalAuthoritySchema || !value.Authority.Current ||
		!t422RetentionDigest(value.Authority.Search) || value.Authority.Inventory.Records == 0 ||
		value.Authority.Inventory.Records > repositoryindex.MaxSourceMembers*repositoryindex.MaxRecordsPerMember {
		return nil, control.stop()
	}
	control.mu.Lock()
	valid := !control.busy && control.step == 0 && control.err == nil && !control.closed
	if valid {
		control.busy = true
	}
	control.mu.Unlock()
	if !valid {
		return nil, control.stop()
	}
	return func(reportErr error) {
		if reportErr != nil || !control.current(ctx, 3) {
			_ = control.stop()
			return
		}
		control.mu.Lock()
		valid := control.busy && control.step == 0 && control.err == nil && !control.closed
		if valid {
			control.old, control.files = value.Authority.Search, value.Authority.Inventory.Records
			control.step, control.busy = 1, false
		}
		control.mu.Unlock()
		if !valid {
			_ = control.stop()
		}
	}, nil
}

func t422RetentionDigest(value string) bool {
	if len(value) != 71 || value[:7] != "sha256:" {
		return false
	}
	for _, ch := range value[7:] {
		if ch < '0' || ch > '9' && ch < 'a' || ch > 'f' {
			return false
		}
	}
	return true
}

func t422RetentionRequest(request *http.Request, command bool) bool {
	if request == nil || request.URL == nil || request.URL.Path != request.URL.EscapedPath() ||
		request.URL.RawQuery != "" || request.URL.ForceQuery || request.ContentLength != 0 || len(request.TransferEncoding) != 0 {
		return false
	}
	if command {
		return request.Method == http.MethodPost && request.URL.Path == t422RetentionPinPath
	}
	return request.Method == http.MethodGet && request.URL.Path == t422RetentionReadPath
}

func (control *t422RetentionControl) begin(ctx context.Context, step uint8) error {
	if !control.current(ctx, 4) {
		return control.stop()
	}
	control.mu.Lock()
	valid := !control.busy && control.step == step && control.err == nil && !control.closed
	if valid {
		control.busy = true
	}
	control.mu.Unlock()
	if !valid {
		return control.stop()
	}
	return nil
}

func (control *t422RetentionControl) complete(ctx context.Context, step uint8, resultErr error) {
	if resultErr != nil || !control.current(ctx, 4) {
		_ = control.stop()
		return
	}
	control.mu.Lock()
	valid := control.busy && control.step == step && control.err == nil && !control.closed
	if valid {
		control.step++
		control.busy = false
	}
	control.mu.Unlock()
	if !valid {
		_ = control.stop()
	}
}

func (control *t422RetentionControl) command(writer http.ResponseWriter, request *http.Request) {
	principal, ok := auth.PrincipalFromContext(request.Context())
	if !t422RetentionRequest(request, true) || !ok || !t421ExactReadLegacyPrincipal(principal) ||
		len(request.Header.Values(t421ExactReadActivationHeader)) != 0 || len(request.Header.Values(t421ExactReadOrdinalHeader)) != 0 ||
		control.begin(request.Context(), 1) != nil {
		_ = control.stop()
		http.Error(writer, "retention control refused", http.StatusConflict)
		return
	}
	completed := false
	defer func() {
		if !completed {
			_ = control.stop()
		}
	}()
	if err := control.acquirePin(); err != nil {
		_ = control.stop()
		http.Error(writer, "retention control refused", http.StatusConflict)
		return
	}
	const body = "{\"status\":\"complete\"}"
	writer.Header().Set("Content-Type", "application/json")
	n, err := writer.Write([]byte(body))
	if err != nil || n != len(body) {
		return
	}
	control.complete(request.Context(), 1, nil)
	completed = true
}

func (control *t422RetentionControl) acquirePin() error {
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.old == "" || control.pin != nil || control.err != nil || control.closed || control.ctx.Err() != nil {
		return errT422RetentionControl
	}
	pin, err := control.pins.Acquire(control.launch.request.Repository, control.old)
	if err != nil {
		return errT422RetentionControl
	}
	control.pin, control.pinnedAt = pin, time.Now().UnixNano()
	return nil
}

func (control *t422RetentionControl) limits() readaccounting.Counts {
	control.mu.Lock()
	defer control.mu.Unlock()
	return readaccounting.Counts{ControlFileReads: 41, MemberVisits: 2 * control.files}
}

func (control *t422RetentionControl) read(caller context.Context) ([]byte, func(error), error) {
	ctx, cancel := context.WithTimeout(caller, 4*time.Hour)
	joined := make(chan struct{})
	stop := context.AfterFunc(control.ctx, func() { cancel(); close(joined) })
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			if !stop() {
				<-joined
			}
		})
	}
	handedOff := false
	defer func() {
		if !handedOff {
			cleanup()
			_ = control.stop()
		}
	}()
	if err := control.begin(ctx, 2); err != nil {
		cleanup()
		return nil, nil, err
	}
	finish := func(err error) { defer cleanup(); control.complete(ctx, 2, err) }
	observation, err := control.observe(ctx)
	if err != nil {
		handedOff = true
		return nil, finish, control.stop()
	}
	raw, err := json.Marshal(observation)
	if err != nil || len(raw) > t422RetentionBodyBytes || !control.current(ctx, 4) {
		handedOff = true
		return nil, finish, control.stop()
	}
	handedOff = true
	return raw, finish, nil
}

// observe is the concrete native recipe, not an admission entry. Its caller
// owns the exact R ledger, semantic/request fence and terminal report tail.
func (control *t422RetentionControl) observe(ctx context.Context) (t422RetentionObservation, error) {
	control.mu.Lock()
	value := t422RetentionObservation{Schema: "t422-current-prior-observation-v1", OldSearchGenerationSHA256: control.old,
		PinnedAtUnixNano: control.pinnedAt}
	pinned := control.pin != nil
	control.mu.Unlock()
	defer control.releasePin()
	if !pinned || !control.pins.Pinned(control.launch.request.Repository, value.OldSearchGenerationSHA256) {
		return value, errT422RetentionControl
	}
	root, err := focusedindex.ReadSearchGenerationRootContext(ctx, control.owner.IndexDir, control.launch.request.Repository)
	if err != nil || root.Prior == nil || root.Prior.GenerationDigest != value.OldSearchGenerationSHA256 ||
		root.Current.GenerationDigest == value.OldSearchGenerationSHA256 {
		return value, errT422RetentionControl
	}
	// Root FileCount counts immutable artifact files, not corpus records. The
	// original exact ledger counts native source-member records during opens.
	value.NewSearchGenerationSHA256 = root.Current.GenerationDigest
	value.Held, err = control.sweep(ctx)
	if err != nil {
		return value, err
	}
	old, err := search.OpenExactGenerationReader(ctx, control.owner.IndexDir, root.Repository, root.Prior.GenerationDigest)
	if err != nil {
		return value, errT422RetentionControl
	}
	defer old.Close()
	oldBlob, err := t422RetentionQuery(ctx, old, root.Repository)
	if err != nil {
		return value, err
	}
	value.OldRecords = 1
	current, err := search.OpenExactGenerationReader(ctx, control.owner.IndexDir, root.Repository, root.Current.GenerationDigest)
	if err != nil {
		return value, errT422RetentionControl
	}
	defer current.Close()
	newBlob, err := t422RetentionQuery(ctx, current, root.Repository)
	if err != nil || oldBlob == newBlob {
		return value, errT422RetentionControl
	}
	value.NewRecords = 1
	control.releasePin()
	value.ReleasedAtUnixNano = time.Now().UnixNano()
	value.Released, err = control.sweep(ctx)
	if err != nil {
		return value, err
	}
	retainedBlob, err := t422RetentionQuery(ctx, old, root.Repository)
	if err != nil || retainedBlob != oldBlob {
		return value, errT422RetentionControl
	}
	value.PostReleaseRecords, value.OldReaderHeldThroughReprobe = 1, true
	value.QuerySHA256 = t422RetentionRecipe("t422-reader-query-v1", t421FinalSHA256([]byte(t422RetentionProbePath)), oldBlob, newBlob)
	value.OldProjectionSHA256 = t422RetentionRecipe("t422-reader-projection-v1", value.QuerySHA256, oldBlob)
	value.NewProjectionSHA256 = t422RetentionRecipe("t422-reader-projection-v1", value.QuerySHA256, newBlob)
	value.PostReleaseProjectionSHA256 = t422RetentionRecipe("t422-reader-projection-v1", value.QuerySHA256, retainedBlob)
	return value, nil
}

func (control *t422RetentionControl) sweep(ctx context.Context) (value t422RetentionSweep, retErr error) {
	defer func() {
		if recover() != nil {
			retErr = control.stop()
		}
	}()
	if ctx.Err() != nil {
		return value, errT422RetentionControl
	}
	control.mu.Lock()
	if control.turns >= 2 || control.err != nil {
		control.mu.Unlock()
		return value, errT422RetentionControl
	}
	control.turns++
	value.Attempt = control.turns
	control.mu.Unlock()
	result := control.owner.Sweep(ctx, time.Now(), "", lifecycle.DefaultLimits())
	value.Scanned, value.Deleted, value.More = result.Scanned, result.Deleted, result.More
	value.Completeness, value.Failed = string(result.Completeness), result.Err != nil
	control.mu.Lock()
	if result.Deleted > 0 {
		control.deleted += uint64(result.Deleted)
		control.maxDeleted = max(control.maxDeleted, uint64(result.Deleted))
	}
	control.mu.Unlock()
	raw, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Epoch  uint64 `json:"epoch"`
		Phase  uint32 `json:"phase"`
		t422RetentionSweep
	}{"t422-retention-turn-v1", 1, 4, value})
	if err != nil || len(raw) > 1<<10 || control.sink(raw) != nil || ctx.Err() != nil ||
		result.Err != nil || result.Completeness != lifecycle.Exact || result.More || result.Scanned != 0 || result.Deleted != 0 {
		return value, errT422RetentionControl
	}
	return value, nil
}

func t422RetentionQuery(ctx context.Context, reader *search.ExactGenerationReader, repository string) (string, error) {
	filename, _ := syntax.Parse("\\A"+regexp.QuoteMeta(t422RetentionProbePath)+"\\z", syntax.Perl)
	content, _ := syntax.Parse("(?s)\\A.*\\z", syntax.Perl)
	result, err := reader.Search(ctx, &query.And{Children: []query.Q{
		&query.Regexp{Regexp: filename, FileName: true, CaseSensitive: true},
		&query.Regexp{Regexp: content, Content: true, CaseSensitive: true},
	}}, search.Options{MaxMatches: 2})
	if err != nil || result == nil || result.Crashes != 0 || result.FilesSkipped != 0 || result.ShardsSkipped != 0 ||
		len(result.Files) != 1 || result.Files[0].Repository != repository || result.Files[0].FileName != t422RetentionProbePath {
		return "", errT422RetentionControl
	}
	var body []byte
	for _, chunk := range result.Files[0].ChunkMatches {
		if chunk.FileName {
			continue
		}
		if body != nil || chunk.ContentStart.ByteOffset != 0 || len(chunk.Content) == 0 || len(chunk.Content) > 1<<20 ||
			len(chunk.Ranges) != 1 || chunk.Ranges[0].Start.ByteOffset != 0 || int(chunk.Ranges[0].End.ByteOffset) != len(chunk.Content) {
			return "", errT422RetentionControl
		}
		body = chunk.Content
	}
	if body == nil {
		return "", errT422RetentionControl
	}
	// Git object identity, not a security digest; projections use SHA-256.
	digest := sha1.New()
	_, _ = fmt.Fprintf(digest, "blob %d\x00", len(body))
	_, _ = digest.Write(body)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func t422RetentionRecipe(domain string, values ...string) string {
	digest := sha256.New()
	for _, value := range append([]string{domain}, values...) {
		_, _ = fmt.Fprintf(digest, "%d:%s", len(value), value)
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

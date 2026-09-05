package dispatchadmission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	ProductionEnvironment = "PHEBS_T422_DISPATCH"
	ProductionSelector    = "parent-bound-v1"
	ProgramPhebs          = "phebs"
	ProgramCorpusAuthor   = "t422-author"
	// These are bootstrap construction ceilings, not frozen ceremony limits.
	MaximumProductionBootstrapBytes = 64 << 10
	ProductionBootstrapTimeout      = 10 * time.Second
	productionBootstrapHeaderBytes  = 72
)

// ProductionToolBinding carries private parent-owned recipe locations, never
// public evidence or caller-supplied verified identity. The admitted parent
// checks its opaque resources before each permission; the child does not hash
// them again or claim that a path and environment alone establish provenance.
type ProductionToolBinding struct {
	Role        string
	Path        string
	Environment []string
}

// ProductionBootstrap binds both inherited endpoints to one producer lifetime.
// Its complete canonical digest is acknowledged before ordinary DA01/PC01 use.
// The caller supplies already-derived bounds; this value issues no freeze or
// private tool/configuration admission and must come from the owning launcher.
type ProductionBootstrap struct {
	Program     string
	InputSHA256 [32]byte
	Producer    Producer
	Phase       uint32
	Limits      Limits
	Control     PhaseControlConfig
	Tools       []ProductionToolBinding
}

func (record ProductionBootstrap) validate() error {
	sites, roles := ProductionSites(), []string{"git", "surreal", "zoekt-git-index"}
	minimumRoles := 4
	switch record.Program {
	case ProgramPhebs:
		if record.InputSHA256 != ([32]byte{}) {
			return ErrProductionBootstrap
		}
	case ProgramCorpusAuthor:
		if record.Control.OwnerControl {
			return ErrProductionBootstrap
		}
		if record.InputSHA256 == ([32]byte{}) {
			return ErrProductionBootstrap
		}
		sites, roles, minimumRoles = AuthorSites(), []string{"git"}, 1
	default:
		return ErrProductionBootstrap
	}
	l := record.Limits
	if record.Producer.ID == 0 || record.Producer.Binding == ([32]byte{}) || record.Phase == 0 ||
		l.Producers < 1 || l.Producers > 32 || l.Sites < len(sites) || l.Sites > 512 ||
		l.Roles < minimumRoles || l.Roles > 16 || l.Phases < 1 || l.Phases > 32 ||
		l.ActivePerProducer < 1 || l.ActivePerProducer > 128 || l.Attempts == 0 || l.Attempts > 1_000_000_000 ||
		l.WireBytes < 2*FrameBytes || l.WireBytes > 1<<40 || l.AckTimeout <= 0 || l.AckTimeout > 30*time.Second ||
		!slices.Equal(record.Producer.Sites, sites) || len(record.Tools) != len(roles) ||
		record.Control.InitialPhase != record.Phase || record.Control.MaximumPhases > l.Phases ||
		record.Control.MaximumWireBytes > 1<<40 || record.Control.Timeout > time.Hour {
		return ErrProductionBootstrap
	}
	if _, err := record.Control.validate(); err != nil {
		return ErrProductionBootstrap
	}
	textBytes := len(record.Program)
	for index, role := range roles {
		tool := record.Tools[index]
		if tool.Role != role || !validProductionPath(tool.Path) || !validProductionEnvironment(tool.Environment, role != "surreal") {
			return ErrProductionBootstrap
		}
		textBytes += len(tool.Role) + len(tool.Path)
		for _, entry := range tool.Environment {
			textBytes += len(entry)
		}
		// Bound caller-owned text before JSON's escaping/materialization. The
		// separate encoded-byte check includes its structural/escaping overhead.
		if textBytes > MaximumProductionBootstrapBytes {
			return ErrProductionBootstrap
		}
	}
	return nil
}

func validProductionPath(path string) bool {
	return len(path) > 1 && len(path) <= 4096 && filepath.IsAbs(path) && filepath.Clean(path) == path &&
		!strings.ContainsAny(path, "\x00\r\n")
}

func validProductionEnvironment(environment []string, git bool) bool {
	if len(environment) < 8 || len(environment) > 64 {
		return false
	}
	seen := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		_, duplicate := seen[key]
		if !ok || len(entry) > 8192 || strings.ContainsAny(entry, "\x00\r\n") || duplicate {
			return false
		}
		seen[key] = value
		switch key {
		case "HOME", "TMPDIR", "TMP", "TEMP", "PATH", "GIT_EXEC_PATH":
			if !validProductionPath(value) {
				return false
			}
		case "LANG", "LC_ALL":
			if value != "C" {
				return false
			}
		case "TZ":
			if value != "UTC" {
				return false
			}
		case "GOENV", "GOWORK", "GOPROXY", "GOSUMDB", "GOTELEMETRY":
			if value != "off" {
				return false
			}
		case "GOTOOLCHAIN":
			if value != "local" {
				return false
			}
		case "GIT_CONFIG_NOSYSTEM", "GIT_ATTR_NOSYSTEM", "GIT_NO_REPLACE_OBJECTS", "GIT_NO_LAZY_FETCH":
			if value != "1" {
				return false
			}
		case "GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS":
			if value != "0" {
				return false
			}
		case "GIT_CONFIG_GLOBAL", "GIT_TEMPLATE_DIR":
			if value != os.DevNull {
				return false
			}
		case "GIT_ALLOW_PROTOCOL":
			if value != "file" {
				return false
			}
		case "GIT_CONFIG_COUNT":
			if value != "3" {
				return false
			}
		case "GIT_CONFIG_KEY_0", "GIT_CONFIG_KEY_1", "GIT_CONFIG_KEY_2":
			if value != map[string]string{"GIT_CONFIG_KEY_0": "core.fsmonitor", "GIT_CONFIG_KEY_1": "core.untrackedCache", "GIT_CONFIG_KEY_2": "core.hooksPath"}[key] {
				return false
			}
		case "GIT_CONFIG_VALUE_0", "GIT_CONFIG_VALUE_1":
			if value != "false" {
				return false
			}
		case "GIT_CONFIG_VALUE_2":
			if value != os.DevNull {
				return false
			}
		default:
			return false
		}
	}
	for _, key := range []string{"HOME", "TMPDIR", "TMP", "TEMP", "PATH", "LANG", "LC_ALL", "TZ"} {
		if _, present := seen[key]; !present {
			return false
		}
	}
	if git {
		for _, key := range []string{"GIT_EXEC_PATH", "GIT_CONFIG_NOSYSTEM", "GIT_CONFIG_GLOBAL", "GIT_ATTR_NOSYSTEM", "GIT_NO_REPLACE_OBJECTS", "GIT_NO_LAZY_FETCH", "GIT_TERMINAL_PROMPT", "GIT_OPTIONAL_LOCKS", "GIT_ALLOW_PROTOCOL", "GIT_TEMPLATE_DIR", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_KEY_1", "GIT_CONFIG_KEY_2", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_VALUE_1", "GIT_CONFIG_VALUE_2"} {
			if _, present := seen[key]; !present {
				return false
			}
		}
		if seen["PATH"] != seen["GIT_EXEC_PATH"] {
			return false
		}
	}
	return true
}

func bootstrapHeader(raw []byte, binding [32]byte) [productionBootstrapHeaderBytes]byte {
	var header [productionBootstrapHeaderBytes]byte
	copy(header[:4], "PB01")
	binary.BigEndian.PutUint32(header[4:8], uint32(len(raw)))
	copy(header[8:40], binding[:])
	digest := sha256.Sum256(raw)
	copy(header[40:], digest[:])
	return header
}

// SendProductionBootstrap borrows both parent endpoints on success so their owner
// can transfer them to ServeChecked/NewPhaseControl. Failure closes only those
// endpoints. One bounded record and the two digest ACKs are bootstrap traffic,
// not dispatches or DA01/PC01 frames.
// The caller must already own the verified child image, inputs and both sockets.
func SendProductionBootstrap(ctx context.Context, file, controlFile *os.File, record ProductionBootstrap) (retErr error) {
	defer func() {
		if retErr != nil {
			if file != nil {
				_ = file.Close()
			}
			if controlFile != nil {
				_ = controlFile.Close()
			}
		}
	}()
	if file == nil || controlFile == nil || file == controlFile || ctx == nil || ctx.Err() != nil ||
		record.validate() != nil || protectInheritance(file) != nil || protectInheritance(controlFile) != nil {
		return ErrProductionBootstrap
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > MaximumProductionBootstrapBytes {
		return ErrProductionBootstrap
	}
	connection, err := net.FileConn(file)
	if err != nil {
		return ErrProductionBootstrap
	}
	defer func() { _ = connection.Close() }()
	conn, ok := connection.(*net.UnixConn)
	if !ok {
		return ErrProductionBootstrap
	}
	controlConnection, err := net.FileConn(controlFile)
	if err != nil {
		return ErrProductionBootstrap
	}
	defer func() { _ = controlConnection.Close() }()
	control, ok := controlConnection.(*net.UnixConn)
	if !ok {
		return ErrProductionBootstrap
	}
	opCtx, cancel := context.WithTimeout(ctx, ProductionBootstrapTimeout)
	defer cancel()
	deadline, _ := opCtx.Deadline()
	if conn.SetDeadline(deadline) != nil || control.SetDeadline(deadline) != nil {
		return ErrProductionBootstrap
	}
	stop := context.AfterFunc(opCtx, func() { _ = conn.Close(); _ = control.Close() })
	defer stop()
	header := bootstrapHeader(raw, record.Producer.Binding)
	controlHeader := header
	copy(controlHeader[:4], "PBPC")
	// Write the distinctly tagged control challenge FIRST. If both inherited
	// descriptors alias one stream, its admission reader sees PBPC and refuses
	// instead of installing competing DA01/PC01 readers on the same endpoint.
	if n, err := control.Write(controlHeader[:]); err != nil || n != len(controlHeader) {
		return ErrProductionBootstrap
	}
	if n, err := conn.Write(header[:]); err != nil || n != len(header) {
		return ErrProductionBootstrap
	}
	if n, err := conn.Write(raw); err != nil || n != len(raw) {
		return ErrProductionBootstrap
	}
	var ack [productionBootstrapHeaderBytes]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil || ack != header || opCtx.Err() != nil {
		return ErrProductionBootstrap
	}
	if _, err := io.ReadFull(control, ack[:]); err != nil || ack != controlHeader || opCtx.Err() != nil {
		return ErrProductionBootstrap
	}
	return nil
}

// BootstrapProduction is called synchronously by early main, before any child
// or worker starts. Missing selector performs one lookup and creates no state.
// Exact mode consumes only FD3/FD4; both are adopted CLOEXEC before reading or
// acknowledging a record. An invalid/failed selected bootstrap never falls back.
// The private socket's trusted parent supplies authority; selector/record bytes
// alone cannot issue any private admission or prove verified input provenance.
func BootstrapProduction(ctx context.Context) (*ProductionLifetime, error) {
	return bootstrapSelectedProgram(ctx, ProgramPhebs, false)
}

// BootstrapAuthor requires the closed author program and inherited transport;
// absence is a refusal, never an ordinary or fixture authoring permission.
func BootstrapAuthor(ctx context.Context) (*ProductionLifetime, error) {
	return bootstrapSelectedProgram(ctx, ProgramCorpusAuthor, true)
}

func bootstrapSelectedProgram(ctx context.Context, program string, required bool) (*ProductionLifetime, error) {
	selector, selected := os.LookupEnv(ProductionEnvironment)
	if !selected {
		if required {
			return nil, ErrProductionBootstrap
		}
		return nil, nil
	}
	if selector != ProductionSelector || ctx == nil || ctx.Err() != nil || !productionBootstrapStarted.CompareAndSwap(false, true) {
		return nil, ErrProductionBootstrap
	}
	if !inheritedProductionSocket(3) || !inheritedProductionSocket(4) {
		return nil, ErrProductionBootstrap
	}
	return bootstrapProgram(ctx, os.NewFile(3, "production-admission"), os.NewFile(4, "production-phase"), program)
}

func bootstrapProduction(ctx context.Context, admissionFile, controlFile *os.File) (_ *ProductionLifetime, retErr error) {
	return bootstrapProgram(ctx, admissionFile, controlFile, ProgramPhebs)
}

func bootstrapProgram(ctx context.Context, admissionFile, controlFile *os.File, program string) (_ *ProductionLifetime, retErr error) {
	// Protect both original inherited handles before either can be handed to a
	// goroutine. No native launch occurs during this synchronous adoption.
	admission, admissionErr := adopt(admissionFile)
	control, controlErr := adopt(controlFile)
	if admissionErr != nil || controlErr != nil {
		if admission != nil {
			_ = admission.Close()
		}
		if control != nil {
			_ = control.Close()
		}
		return nil, ErrProductionBootstrap
	}
	defer func() { _ = admission.Close(); _ = control.Close() }()
	opCtx, cancel := context.WithTimeout(ctx, ProductionBootstrapTimeout)
	defer cancel()
	deadline, _ := opCtx.Deadline()
	if admission.SetDeadline(deadline) != nil || control.SetDeadline(deadline) != nil {
		return nil, ErrProductionBootstrap
	}
	stop := context.AfterFunc(opCtx, func() { _ = admission.Close(); _ = control.Close() })
	defer stop()
	var header [productionBootstrapHeaderBytes]byte
	if _, err := io.ReadFull(admission, header[:]); err != nil || string(header[:4]) != "PB01" {
		return nil, ErrProductionBootstrap
	}
	size := binary.BigEndian.Uint32(header[4:8])
	if size == 0 || size > MaximumProductionBootstrapBytes {
		return nil, ErrProductionBootstrap
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(admission, raw); err != nil {
		return nil, ErrProductionBootstrap
	}
	var record ProductionBootstrap
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil || record.validate() != nil || record.Program != program {
		return nil, ErrProductionBootstrap
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(raw, canonical) || bootstrapHeader(raw, record.Producer.Binding) != header || opCtx.Err() != nil {
		return nil, ErrProductionBootstrap
	}
	var controlHeader [productionBootstrapHeaderBytes]byte
	expectedControlHeader := header
	copy(expectedControlHeader[:4], "PBPC")
	if _, err := io.ReadFull(control, controlHeader[:]); err != nil || controlHeader != expectedControlHeader || opCtx.Err() != nil {
		return nil, ErrProductionBootstrap
	}
	// Stdlib File duplicates once during transfer to the existing constructors;
	// their adopted descriptors stay CLOEXEC. No second custody implementation.
	admissionFile, err = admission.File()
	if err != nil {
		return nil, ErrProductionBootstrap
	}
	client, err := NewClient(ctx, admissionFile, record.Producer, record.Phase, record.Limits)
	if err != nil {
		return nil, ErrProductionBootstrap
	}
	var lifetime *ProductionLifetime
	defer func() {
		if retErr != nil {
			_ = client.fail(ErrProductionBootstrap)
			_ = client.conn.Close()
			if lifetime != nil {
				<-lifetime.controlDone
			}
		}
	}()
	controlFile, err = control.File()
	if err != nil {
		return nil, ErrProductionBootstrap
	}
	done, err := StartPhaseControl(ctx, controlFile, client, record.Control)
	if err != nil {
		return nil, ErrProductionBootstrap
	}
	lifetime = &ProductionLifetime{program: program, inputSHA256: record.InputSHA256, client: client, controlDone: done, tools: make(map[string]ProductionToolBinding, len(record.Tools))}
	for _, tool := range record.Tools {
		tool.Environment = slices.Clone(tool.Environment)
		lifetime.tools[tool.Role] = tool
	}
	if n, err := admission.Write(header[:]); err != nil || n != len(header) || opCtx.Err() != nil || client.ctx.Err() != nil {
		return nil, ErrProductionBootstrap
	}
	if n, err := control.Write(controlHeader[:]); err != nil || n != len(controlHeader) || opCtx.Err() != nil || client.ctx.Err() != nil {
		return nil, ErrProductionBootstrap
	}
	if !productionRuntime.CompareAndSwap(nil, lifetime) {
		return nil, ErrProductionBootstrap
	}
	return lifetime, nil
}

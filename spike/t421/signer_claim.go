//go:build darwin || linux

package t421

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	executionSignerCeremonyClaimSchema = "t422-signer-ceremony-id-claim-v1"
	maxExecutionSignerClaimBytes       = 1 << 10
	maxExecutionSignerPathBytes        = 1023
	maxExecutionSignerNameBytes        = 255
)

type executionSignerCeremonyClaimV1 struct {
	Schema                string `json:"schema"`
	SignerNamespaceSHA256 string `json:"signer_namespace_sha256"`
	CeremonyID            string `json:"ceremony_id"`
}

type executionSignerRegistryNames struct {
	idSHA256               string
	claim                  string
	temporaryKey           string
	temporaryPublic        string
	privateKey             string
	generatedPublic        string
	allowlist              string
	candidate              string
	signatureStage         string
	signature              string
	sourceSignatureStage   string
	sourceSignature        string
	returnedSignatureStage string
	returnedSignature      string
}

func (names executionSignerRegistryNames) absentBeforeClaim() []string {
	return []string{
		names.temporaryKey, names.temporaryPublic, names.privateKey,
		names.generatedPublic, names.allowlist, names.candidate,
		names.signatureStage, names.signature, names.sourceSignatureStage,
		names.sourceSignature, names.returnedSignatureStage, names.returnedSignature,
	}
}

// executionSignerCeremonyClaimCustody is the first irreversible signer
// authority boundary. Close releases only its descriptor; the durable claim is
// deliberately retained so a post-claim failure cannot reuse the ID.
type executionSignerCeremonyClaimCustody struct {
	mu         sync.Mutex
	file       *os.File
	path       string
	name       string
	identity   executionSignerFileIdentity
	namespace  executionSignerNamespaceBinding
	idSHA256   string
	raw        []byte
	closed     bool
	keyUsed    bool
	ceremonyID string
	names      executionSignerRegistryNames
}

type executionSignerFileIdentity struct {
	device int64
	inode  uint64
	mode   uint32
	uid    uint32
	size   int64
	links  uint64
}

// claimExecutionSignerCeremony preflights every currently specified registry
// name and cap before the sole create-exclusive mutation. It creates no key,
// signature, candidate, socket, checkout binding, or operational authority.
func claimExecutionSignerCeremony(
	ctx context.Context,
	namespace executionSignerNamespaceBinding,
	ceremonyID string,
	operationalRoot string,
) (*executionSignerCeremonyClaimCustody, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrExecutionEpochOne
	}
	// The fixed auth.sock witness is intentionally first: no signer-root
	// mutation may precede discovery that the later Unix address cannot fit.
	if err := preflightExecutionAuthSocket(operationalRoot); err != nil {
		return nil, err
	}
	checked, err := namespace.recheck(ctx)
	if err != nil || !executionCeremonyID.MatchString(ceremonyID) || ceremonyID == "." || ceremonyID == ".." {
		return nil, ErrExecutionEpochOne
	}
	names := executionSignerNames(ceremonyID)
	root := checked.owner.path
	if err := preflightExecutionSignerNames(root, names); err != nil {
		return nil, err
	}
	claim := executionSignerCeremonyClaimV1{
		Schema:                executionSignerCeremonyClaimSchema,
		SignerNamespaceSHA256: checked.digest,
		CeremonyID:            ceremonyID,
	}
	raw, err := MarshalCanonical(claim)
	if err != nil || len(raw) == 0 || len(raw) > maxExecutionSignerClaimBytes {
		return nil, errors.New("T42.2 signer ceremony claim is not bounded canonical JSON")
	}
	return createExecutionSignerCeremonyClaim(ctx, checked, ceremonyID, names, raw)
}

func executionSignerNames(ceremonyID string) executionSignerRegistryNames {
	digest := sha256.Sum256([]byte(ceremonyID))
	id := hex.EncodeToString(digest[:])
	temporary := ".t422-keygen-" + id + ".key.tmp"
	return executionSignerRegistryNames{
		idSHA256:               id,
		claim:                  "ceremony-" + id + ".claim.json",
		temporaryKey:           temporary,
		temporaryPublic:        temporary + ".pub",
		privateKey:             "signer-" + id + ".key",
		generatedPublic:        "signer-" + id + ".key.pub",
		allowlist:              "allowed-signers-" + id + ".txt",
		candidate:              "execution-freeze-" + id + ".json",
		signatureStage:         "execution-freeze-" + id + ".sig.stage",
		signature:              "execution-freeze-" + id + ".sig",
		sourceSignatureStage:   "source-verification-" + id + ".sig.stage",
		sourceSignature:        "source-verification-" + id + ".sig",
		returnedSignatureStage: "returned-package-" + id + ".sig.stage",
		returnedSignature:      "returned-package-" + id + ".sig",
	}
}

func preflightExecutionAuthSocket(root string) error {
	if !validExecutionLauncherPath(root) || !utf8.ValidString(root) || strings.IndexByte(root, 0) >= 0 {
		return errors.New("T42.2 operational root cannot contain the authorization socket")
	}
	canonical, err := filepath.EvalSymlinks(root)
	info, statErr := os.Stat(root)
	if err != nil || statErr != nil || canonical != root || !info.IsDir() {
		return errors.New("T42.2 operational root is not an exact canonical directory")
	}
	path, err := executionSignerJoinedPath(root, "auth.sock", maxExecutionAuthSocketPathBytes)
	if err != nil || len(path) > maxExecutionAuthSocketPathBytes {
		return errors.New("T42.2 authorization socket path exceeds its native bound")
	}
	return nil
}

func preflightExecutionSignerNames(root string, names executionSignerRegistryNames) error {
	if !validExecutionHexSHA256(names.idSHA256) {
		return errors.New("T42.2 signer ceremony ID digest is invalid")
	}
	all := append([]string{names.claim}, names.absentBeforeClaim()...)
	// Public-derived names are not yet known; their maximum exact templates
	// establish the path cap before the ceremony claim mutates the registry.
	all = append(all,
		"canonical-public-"+strings.Repeat("f", 64)+".pub",
		"fingerprint-"+strings.Repeat("f", 64)+".claim.json",
	)
	seen := make(map[string]struct{}, len(all))
	for _, name := range all {
		if name == "" || len(name) > maxExecutionSignerNameBytes || !utf8.ValidString(name) || strings.IndexByte(name, 0) >= 0 || filepath.Base(name) != name {
			return errors.New("T42.2 signer registry basename is invalid")
		}
		if _, ok := seen[name]; ok {
			return errors.New("T42.2 signer registry basenames alias")
		}
		seen[name] = struct{}{}
		if _, err := executionSignerJoinedPath(root, name, maxExecutionSignerPathBytes); err != nil {
			return err
		}
	}
	return nil
}

func executionSignerJoinedPath(root, name string, maximum int) (string, error) {
	if !validExecutionLauncherPath(root) || filepath.Clean(root) != root || name == "" || filepath.Base(name) != name {
		return "", errors.New("T42.2 signer registry path is invalid")
	}
	joined := filepath.Join(root, name)
	if !filepath.IsAbs(joined) || filepath.Clean(joined) != joined || filepath.Dir(joined) != root ||
		!utf8.ValidString(joined) || strings.IndexByte(joined, 0) >= 0 || len(joined) > maximum {
		return "", errors.New("T42.2 signer registry path exceeds or escapes its held root")
	}
	return joined, nil
}

func (claim *executionSignerCeremonyClaimCustody) check(ctx context.Context) error {
	if claim == nil || ctx == nil || ctx.Err() != nil {
		return ErrExecutionEpochOne
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.closed || claim.file == nil || !claim.namespace.valid() {
		return ErrExecutionEpochOne
	}
	if err := checkExecutionSignerCeremonyClaim(ctx, claim); err != nil {
		return err
	}
	_, err := claim.namespace.recheck(ctx)
	return err
}

func (claim *executionSignerCeremonyClaimCustody) Close() error {
	if claim == nil {
		return nil
	}
	claim.mu.Lock()
	defer claim.mu.Unlock()
	if claim.closed {
		return nil
	}
	claim.closed = true
	if claim.file != nil {
		return claim.file.Close()
	}
	return nil
}

package t4013

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const maxExactControlEntries = 4096

type freezeEnvelope struct {
	Schema            string `json:"schema"`
	CeremonyID        string `json:"ceremony_id"`
	SourceCommit      string `json:"source_commit"`
	PlanDigest        string `json:"plan_digest"`
	SignerFingerprint string `json:"signer_fingerprint"`
	FrozenAt          string `json:"frozen_at"`
}

type transferEnvelope struct {
	Schema       string `json:"schema"`
	CeremonyID   string `json:"ceremony_id"`
	SourceCommit string `json:"source_commit"`
	PlanDigest   string `json:"plan_digest"`
	SealedAt     string `json:"sealed_at"`
}

// InspectPlanControl returns identity from one bounded stable plan read.
func InspectPlanControl(path string) (schema, planDigest string, err error) {
	raw, plan, err := ReadPlanControl(path)
	if err != nil {
		return "", "", err
	}
	return plan.Schema, PlanDigest(raw), nil
}

// ReadPlanControl reads and validates one bounded stable plan while retaining historical bytes.
func ReadPlanControl(path string) ([]byte, Plan, error) {
	identity, plan, err := readPlanIdentity(path)
	if err != nil {
		return nil, Plan{}, err
	}
	return identity.raw, plan, nil
}

// InspectCeremonyJSONValue returns one string from a canonical freeze or transfer envelope.
func InspectCeremonyJSONValue(path, key string) (string, error) {
	raw, err := readAtomicRegular(path, maxReturnedControlBytes)
	if err != nil {
		return "", err
	}
	var schema struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return "", errors.New("T40.13 ceremony control is not one JSON value")
	}
	var values map[string]string
	switch schema.Schema {
	case "t4013-freeze-envelope-v1":
		var value freezeEnvelope
		if err := decodeStrict(raw, &value); err != nil || requireCanonicalIndented(raw, value) != nil {
			return "", errors.New("T40.13 freeze envelope is not canonical")
		}
		values = map[string]string{
			"schema": value.Schema, "ceremony_id": value.CeremonyID,
			"source_commit": value.SourceCommit, "plan_digest": value.PlanDigest,
			"signer_fingerprint": value.SignerFingerprint, "frozen_at": value.FrozenAt,
		}
	case "t4013-source-free-transfer-v1":
		var value transferEnvelope
		if err := decodeStrict(raw, &value); err != nil || requireCanonicalIndented(raw, value) != nil {
			return "", errors.New("T40.13 transfer envelope is not canonical")
		}
		values = map[string]string{
			"schema": value.Schema, "ceremony_id": value.CeremonyID,
			"source_commit": value.SourceCommit, "plan_digest": value.PlanDigest,
			"sealed_at": value.SealedAt,
		}
	default:
		return "", errors.New("T40.13 ceremony control schema is invalid")
	}
	value, ok := values[key]
	if !ok {
		return "", errors.New("T40.13 ceremony control key is invalid")
	}
	return value, nil
}

// InspectChecksumInventory validates the fixed eight-file source-free checksum control.
func InspectChecksumInventory(path string) error {
	raw, err := readAtomicRegular(path, maxReturnedControlBytes)
	if err != nil {
		return err
	}
	want := []string{
		"allowed_signers", "freeze.json", "freeze.json.sig", "manifest.json",
		"observation.json", "plan.json", "results.json", "signer.pub",
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != len(want)+1 || lines[len(lines)-1] != "" {
		return errors.New("T40.13 checksum inventory is not one canonical value followed by EOF")
	}
	for index, name := range want {
		line := lines[index]
		if len(line) != sha256.Size*2+2+len(name) || line[sha256.Size*2:sha256.Size*2+2] != "  " ||
			line[sha256.Size*2+2:] != name {
			return errors.New("T40.13 checksum inventory is not canonical")
		}
		digest := line[:sha256.Size*2]
		decoded, decodeErr := hex.DecodeString(digest)
		if decodeErr != nil || hex.EncodeToString(decoded) != digest {
			return errors.New("T40.13 checksum inventory digest is not canonical")
		}
	}
	return nil
}

// InspectExactDirectory requires exactly the named entries within their count bound.
func InspectExactDirectory(path string, expected []string) error {
	if len(expected) > maxExactControlEntries {
		return errors.New("T40.13 exact directory expectation exceeds its entry bound")
	}
	want := slices.Clone(expected)
	for _, name := range want {
		if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
			return errors.New("T40.13 exact directory entry is invalid")
		}
	}
	slices.Sort(want)
	for index := 1; index < len(want); index++ {
		if want[index] == want[index-1] {
			return errors.New("T40.13 exact directory expectation contains a duplicate")
		}
	}
	entries, err := readDirectoryBounded(path, len(want))
	if err != nil {
		return err
	}
	got := make([]string, len(entries))
	for index, entry := range entries {
		got[index] = entry.Name()
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		return errors.New("T40.13 exact directory contains an unexpected or missing entry")
	}
	return nil
}

// InspectDirectoryPrefixAbsent bounds a sibling scan and refuses every matching entry.
func InspectDirectoryPrefixAbsent(path, forbiddenPrefix string, maximumEntries int) error {
	if maximumEntries < 0 || maximumEntries > maxExactControlEntries || forbiddenPrefix == "" ||
		filepath.Base(forbiddenPrefix) != forbiddenPrefix {
		return errors.New("T40.13 forbidden directory prefix is invalid")
	}
	entries, err := readDirectoryBounded(path, maximumEntries)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), forbiddenPrefix) {
			return fmt.Errorf("T40.13 forbidden directory entry remains: %s", entry.Name())
		}
	}
	return nil
}

// InspectExactFileDigest hashes one bounded stable regular control.
func InspectExactFileDigest(path string, maximumBytes int) (string, error) {
	raw, err := readAtomicRegular(path, maximumBytes)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func requireCanonicalIndented(raw []byte, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if !bytes.Equal(raw, encoded) {
		return errors.New("T40.13 control is not canonical")
	}
	return nil
}

func requireCanonicalCompact(raw []byte, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if !bytes.Equal(raw, encoded) {
		return errors.New("T40.13 control is not canonical")
	}
	return nil
}

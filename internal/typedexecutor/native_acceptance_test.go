package typedexecutor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

const acceptanceSchema = "phebs-typed-native-acceptance-v1"
const acceptanceRepo = "example.invalid/phebs-native-neutral"
const acceptanceMaxConfig = 16384
const acceptanceMaxReceipt = 128 << 10

var acceptanceID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var acceptanceCases = []string{"success", "cancel", "wall", "hard-death"}

// All paths are derived from ID and fixed names. Reviewed seed provenance is
// installation authority; a digest here does not manufacture source admission.
type nativeAcceptanceConfig struct {
	Schema           string            `json:"schema"`
	ID               string            `json:"id"`
	SourceCommit     string            `json:"source_commit"`
	Source           typedindex.Source `json:"source"`
	TestSHA256       string            `json:"test_sha256"`
	HelperSHA256     string            `json:"helper_sha256"`
	EngineSHA256     string            `json:"engine_sha256"`
	SeedSHA256       string            `json:"seed_sha256"`
	InventorySHA256  string            `json:"inventory_sha256"`
	ProfileSHA256    string            `json:"profile_sha256"`
	SelectionSHA256  string            `json:"selection_sha256"`
	ImageSHA256      string            `json:"image_sha256"`
	MkfsSHA256       string            `json:"mkfs_sha256"`
	UniverseSHA256   string            `json:"universe_sha256"`
	ProfileEpoch     int64             `json:"profile_epoch"`
	DeploymentSHA256 string            `json:"deployment_sha256"`
	Policy           typedindex.Policy `json:"policy"`
}

func acceptanceDigest(raw []byte) string {
	s := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(s[:])
}
func acceptanceHash(s string) bool {
	if len(s) != 71 || s[:7] != "sha256:" {
		return false
	}
	b, e := hex.DecodeString(s[7:])
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s[7:]
}
func acceptanceDecode(raw []byte, limit int, out any) error {
	if len(raw) == 0 || len(raw) > limit {
		return errors.New("acceptance control bound")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing control")
	}
	canonical, e := json.Marshal(out)
	if e != nil || !bytes.Equal(canonical, raw) {
		return errors.New("noncanonical or duplicate control")
	}
	return nil
}
func parseAcceptance(raw []byte) (nativeAcceptanceConfig, error) {
	var c nativeAcceptanceConfig
	if e := acceptanceDecode(raw, acceptanceMaxConfig, &c); e != nil {
		return c, e
	}
	if c.Schema != acceptanceSchema || !acceptanceID.MatchString(c.ID) || c.Source.Repository != acceptanceRepo || c.Source.Validate() != nil || c.ProfileEpoch < 1 || c.Policy != typedindex.MeasuredPolicy() {
		return c, errors.New("neutral acceptance contract")
	}
	b, e := hex.DecodeString(c.SourceCommit)
	if e != nil || len(b) != 20 || hex.EncodeToString(b) != c.SourceCommit {
		return c, errors.New("source commit")
	}
	for _, h := range []string{c.TestSHA256, c.HelperSHA256, c.EngineSHA256, c.SeedSHA256, c.InventorySHA256, c.ProfileSHA256, c.SelectionSHA256, c.ImageSHA256, c.MkfsSHA256, c.UniverseSHA256, c.DeploymentSHA256} {
		if !acceptanceHash(h) {
			return c, errors.New("unfilled identity")
		}
	}
	return c, nil
}
func acceptanceCaseValid(name string) bool {
	for _, n := range acceptanceCases {
		if n == name {
			return true
		}
	}
	return false
}
func acceptanceJSON(v any) []byte {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func acceptanceReceipt(path string, v any) error {
	raw := acceptanceJSON(v)
	if len(raw) > acceptanceMaxReceipt {
		return errors.New("receipt overflow")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if e = errors.Join(writeErr, syncErr, closeErr); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	return errors.Join(d.Sync(), d.Close())
}
func TestNativeAcceptanceConfig(t *testing.T) {
	h := acceptanceDigest([]byte("fixed"))
	c := nativeAcceptanceConfig{Schema: acceptanceSchema, ID: "neutral-1", SourceCommit: fmt.Sprintf("%040x", 1), Source: typedindex.Source{Repository: acceptanceRepo, Incarnation: "fixture", Generation: h, Commit: fmt.Sprintf("%040x", 2)}, TestSHA256: h, HelperSHA256: h, EngineSHA256: h, SeedSHA256: h, InventorySHA256: h, ProfileSHA256: h, SelectionSHA256: h, ImageSHA256: h, MkfsSHA256: h, UniverseSHA256: h, ProfileEpoch: 1, DeploymentSHA256: h, Policy: typedindex.MeasuredPolicy()}
	if _, e := parseAcceptance(acceptanceJSON(c)); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name   string
		change func(*nativeAcceptanceConfig)
	}{{"target", func(c *nativeAcceptanceConfig) { c.Source.Repository = "github.com/public/target" }}, {"path", func(c *nativeAcceptanceConfig) { c.ID = "../escape" }}, {"caps", func(c *nativeAcceptanceConfig) { c.Policy.WallSeconds = 900 }}, {"empty", func(c *nativeAcceptanceConfig) { c.HelperSHA256 = "" }}, {"epoch", func(c *nativeAcceptanceConfig) { c.ProfileEpoch = 0 }}} {
		t.Run(tc.name, func(t *testing.T) {
			v := c
			tc.change(&v)
			if _, e := parseAcceptance(acceptanceJSON(v)); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	for _, raw := range [][]byte{append(acceptanceJSON(c), '\n'), bytes.Replace(acceptanceJSON(c), []byte(`"schema":`), []byte(`"unknown":1,"schema":`), 1), bytes.Replace(acceptanceJSON(c), []byte(`"id":"neutral-1"`), []byte(`"id":"neutral-1","id":"neutral-1"`), 1)} {
		if _, e := parseAcceptance(raw); e == nil {
			t.Fatal("ambiguous config")
		}
	}
	if acceptanceCaseValid("target") || acceptanceCaseValid("") {
		t.Fatal("open case namespace")
	}
}
func TestNativeAcceptanceReceiptExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "receipt")
	if e := acceptanceReceipt(p, map[string]string{"state": "held"}); e != nil {
		t.Fatal(e)
	}
	if e := acceptanceReceipt(p, map[string]string{"state": "pass"}); e == nil {
		t.Fatal("overwritten receipt")
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != `{"state":"held"}` {
		t.Fatal(string(b), e)
	}
}

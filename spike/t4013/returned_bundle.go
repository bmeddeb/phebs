package t4013

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	maxReturnedPackageBytes         = 4 << 20
	maxReturnedControlBytes         = 4 << 10
	maxReturnedSignerBytes          = 1 << 10
	maxReturnedArchiveExpandedBytes = 1 << 20
	returnedSignerIdentity          = "phebs-ceremony"
)

type returnedBundleEntry struct {
	name    string
	maximum int64
}

var _returnedBundleEntries = []returnedBundleEntry{
	{name: "allowed_signers", maximum: maxReturnedSignerBytes},
	{name: "freeze.json", maximum: maxReturnedControlBytes},
	{name: "freeze.json.sig", maximum: maxReturnedControlBytes},
	{name: "manifest.json", maximum: maxReturnedControlBytes},
	{name: "observation.json", maximum: MaxObservationBytes},
	{name: "plan.json", maximum: MaxPlanBytes},
	{name: "results.json", maximum: MaxReceiptBytes},
	{name: "SHA256SUMS", maximum: maxReturnedControlBytes},
	{name: "SHA256SUMS.sig", maximum: maxReturnedControlBytes},
	{name: "signer.pub", maximum: maxReturnedSignerBytes},
}

// ExtractReturnedBundle validates every archive header and bound before
// creating the exact evidence inventory beneath outputRoot. Exactly one of
// expectedSignerFingerprint or expectedPackageDigest authenticates the package.
// A package digest is checked before archive inspection; a signer fingerprint
// is checked against the bounded in-memory public key before output.
func ExtractReturnedBundle(
	packagePath string,
	outputRoot string,
	expectedSignerFingerprint string,
	expectedPackageDigest string,
) error {
	if (expectedSignerFingerprint == "") == (expectedPackageDigest == "") {
		return errors.New("exactly one reviewed signer fingerprint or package digest is required")
	}
	if expectedSignerFingerprint != "" {
		if err := validateReturnedSignerFingerprint(expectedSignerFingerprint); err != nil {
			return err
		}
	}
	packageBytes, err := readReturnedPackage(packagePath)
	if err != nil {
		return err
	}
	if expectedPackageDigest != "" {
		if err := validateReturnedPackageDigest(expectedPackageDigest); err != nil {
			return err
		}
		actualDigest := sha256.Sum256(packageBytes)
		actual := "sha256:" + hex.EncodeToString(actualDigest[:])
		if actual != expectedPackageDigest {
			return errors.New("returned package digest differs from the reviewed digest")
		}
	}
	files, err := inspectReturnedArchive(packageBytes)
	if err != nil {
		return err
	}
	trustedAllowedSigners, err := authenticateReturnedSigner(
		files["signer.pub"],
		expectedSignerFingerprint,
	)
	if err != nil {
		return err
	}
	return writeReturnedEvidence(outputRoot, files, trustedAllowedSigners)
}

func readReturnedPackage(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("returned package path must be absolute")
	}
	raw, err := readAtomicRegular(path, maxReturnedPackageBytes)
	if err != nil {
		return nil, fmt.Errorf("read returned package: %w", err)
	}
	if len(raw) == 0 || len(raw) > maxReturnedPackageBytes {
		return nil, errors.New("returned package exceeds the fixed 4-MiB transfer bound")
	}
	return raw, nil
}

func validateReturnedPackageDigest(value string) error {
	const prefix = "sha256:"
	if len(value) != len(prefix)+sha256.Size*2 || value[:len(prefix)] != prefix {
		return errors.New("reviewed package digest must be canonical sha256")
	}
	decoded, err := hex.DecodeString(value[len(prefix):])
	if err != nil || len(decoded) != sha256.Size || prefix+hex.EncodeToString(decoded) != value {
		return errors.New("reviewed package digest must be canonical sha256")
	}
	return nil
}

func validateReturnedSignerFingerprint(value string) error {
	const prefix = "SHA256:"
	if len(value) != len(prefix)+43 || !strings.HasPrefix(value, prefix) {
		return errors.New("reviewed signer fingerprint must be canonical SHA256")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(value[len(prefix):])
	if err != nil || len(decoded) != sha256.Size ||
		prefix+base64.RawStdEncoding.EncodeToString(decoded) != value {
		return errors.New("reviewed signer fingerprint must be canonical SHA256")
	}
	return nil
}

func authenticateReturnedSigner(raw []byte, expectedFingerprint string) ([]byte, error) {
	publicKey, _, options, rest, err := ssh.ParseAuthorizedKey(raw)
	if err != nil || len(options) != 0 || len(rest) != 0 ||
		publicKey.Type() != ssh.KeyAlgoED25519 {
		return nil, errors.New("returned signer public key is not one canonical Ed25519 key")
	}
	if expectedFingerprint != "" && ssh.FingerprintSHA256(publicKey) != expectedFingerprint {
		return nil, errors.New("returned signer fingerprint differs from the reviewed fingerprint")
	}
	trusted := make([]byte, 0, len(returnedSignerIdentity)+1+len(raw))
	trusted = append(trusted, returnedSignerIdentity...)
	trusted = append(trusted, ' ')
	trusted = append(trusted, bytes.TrimSpace(ssh.MarshalAuthorizedKey(publicKey))...)
	trusted = append(trusted, '\n')
	return trusted, nil
}

func inspectReturnedArchive(packageBytes []byte) (map[string][]byte, error) {
	compressed := bytes.NewReader(packageBytes)
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, fmt.Errorf("open returned gzip stream: %w", err)
	}
	gzipReader.Multistream(false)
	expanded := &io.LimitedReader{R: gzipReader, N: maxReturnedArchiveExpandedBytes + 1}
	tarReader := tar.NewReader(expanded)
	wanted := make(map[string]int64, len(_returnedBundleEntries))
	for _, entry := range _returnedBundleEntries {
		wanted["evidence/"+entry.name] = entry.maximum
	}
	seen := make(map[string]bool, len(wanted)+1)
	files := make(map[string][]byte, len(wanted))
	var contentBytes int64
	for {
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read returned archive header: %w", nextErr)
		}
		if seen[header.Name] {
			return nil, fmt.Errorf("returned archive contains duplicate path %q", header.Name)
		}
		seen[header.Name] = true
		if header.Linkname != "" {
			return nil, fmt.Errorf("returned archive path %q has link metadata", header.Name)
		}
		if header.Name == "evidence/" {
			if header.Typeflag != tar.TypeDir || header.Size != 0 {
				return nil, errors.New("returned archive evidence root is not an empty directory header")
			}
			continue
		}
		maximum, ok := wanted[header.Name]
		if !ok {
			return nil, fmt.Errorf("returned archive contains unexpected path %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("returned archive path %q is not a regular file", header.Name)
		}
		if header.Size <= 0 || header.Size > maximum {
			return nil, fmt.Errorf("returned archive path %q exceeds its fixed size bound", header.Name)
		}
		contentBytes += header.Size
		if contentBytes > maxReturnedArchiveExpandedBytes {
			return nil, errors.New("returned archive exceeds its aggregate expanded-byte bound")
		}
		content, readErr := io.ReadAll(io.LimitReader(tarReader, header.Size+1))
		if readErr != nil {
			return nil, fmt.Errorf("read returned archive path %q: %w", header.Name, readErr)
		}
		if int64(len(content)) != header.Size {
			return nil, fmt.Errorf("returned archive path %q is truncated", header.Name)
		}
		files[header.Name[len("evidence/"):]] = content
	}
	tail, tailErr := io.ReadAll(expanded)
	if tailErr != nil {
		return nil, fmt.Errorf("read returned archive tail: %w", tailErr)
	}
	if maxReturnedArchiveExpandedBytes+1-expanded.N > maxReturnedArchiveExpandedBytes {
		return nil, errors.New("returned archive exceeds its aggregate expanded-byte bound")
	}
	for _, value := range tail {
		if value != 0 {
			return nil, errors.New("returned archive contains data after its end marker")
		}
	}
	if err := gzipReader.Close(); err != nil {
		return nil, fmt.Errorf("close returned gzip stream: %w", err)
	}
	if compressed.Len() != 0 {
		return nil, errors.New("returned package contains a trailing compressed stream")
	}
	if len(seen) != len(wanted)+1 || !seen["evidence/"] || len(files) != len(wanted) {
		return nil, errors.New("returned archive does not contain exactly one of every expected path")
	}
	return files, nil
}

func writeReturnedEvidence(
	outputRoot string,
	files map[string][]byte,
	trustedAllowedSigners []byte,
) (retErr error) {
	if !filepath.IsAbs(outputRoot) {
		return errors.New("returned extraction root must be absolute")
	}
	info, err := os.Lstat(outputRoot)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("returned extraction root must be an existing directory, not a symlink")
	}
	entries, err := readDirectoryBounded(outputRoot, 0)
	if err != nil {
		return fmt.Errorf("read returned extraction root: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("returned extraction root must be empty")
	}
	evidenceRoot := filepath.Join(outputRoot, "evidence")
	trustedPath := filepath.Join(outputRoot, "reviewed_allowed_signers")
	if err := os.Mkdir(evidenceRoot, 0o700); err != nil {
		return fmt.Errorf("create returned evidence root: %w", err)
	}
	defer func() {
		if retErr != nil {
			_ = os.RemoveAll(evidenceRoot)
			_ = os.Remove(trustedPath)
		}
	}()
	for _, entry := range _returnedBundleEntries {
		content, ok := files[entry.name]
		if !ok {
			return fmt.Errorf("returned evidence path %q is absent", entry.name)
		}
		path := filepath.Join(evidenceRoot, entry.name)
		file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			return fmt.Errorf("create returned evidence path %q: %w", entry.name, openErr)
		}
		written, writeErr := file.Write(content)
		closeErr := file.Close()
		if writeErr != nil {
			return fmt.Errorf("write returned evidence path %q: %w", entry.name, writeErr)
		}
		if written != len(content) {
			return fmt.Errorf("write returned evidence path %q: %w", entry.name, io.ErrShortWrite)
		}
		if closeErr != nil {
			return fmt.Errorf("close returned evidence path %q: %w", entry.name, closeErr)
		}
	}
	if err := os.WriteFile(trustedPath, trustedAllowedSigners, 0o600); err != nil {
		return fmt.Errorf("create reviewed signer allowlist: %w", err)
	}
	return nil
}

package t4013

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type exactFileIdentity struct {
	path        string
	raw         []byte
	maximum     int
	description string
}

func readPlanIdentity(path string) (exactFileIdentity, Plan, error) {
	raw, err := readBoundedFile(path, MaxPlanBytes)
	if err != nil {
		return exactFileIdentity{}, Plan{}, fmt.Errorf("read T40.13 plan: %w", err)
	}
	plan, err := DecodePlan(raw)
	if err != nil {
		return exactFileIdentity{}, Plan{}, err
	}
	identity := exactFileIdentity{
		path: path, raw: raw, maximum: MaxPlanBytes, description: "plan",
	}
	if planSchemaVersion(plan.Schema) >= 25 {
		identity.path, err = canonicalExistingAuthorityPath(path)
		if err != nil {
			return exactFileIdentity{}, Plan{}, errors.Join(
				err, errors.New("T40.13 V25 plan path is not canonical"),
			)
		}
	}
	return identity, plan, nil
}

func readPreparedIdentity(path, planDigest string) (exactFileIdentity, Prepared, error) {
	canonical, err := canonicalExistingAuthorityPath(path)
	if err != nil || canonical != filepath.Clean(path) {
		return exactFileIdentity{}, Prepared{}, errors.Join(
			err, errors.New("T40.13 V25 prepared path is not canonical"),
		)
	}
	raw, err := readAtomicRegular(canonical, MaxObservationBytes)
	if err != nil {
		return exactFileIdentity{}, Prepared{}, err
	}
	prepared, err := DecodePrepared(raw, planDigest)
	if err != nil {
		return exactFileIdentity{}, Prepared{}, err
	}
	return exactFileIdentity{
		path: canonical, raw: raw, maximum: MaxObservationBytes, description: "prepared custody",
	}, prepared, nil
}

func canonicalExistingAuthorityPath(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return "", errors.New("T40.13 authority path must be absolute and non-root")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(path)))
	if err != nil {
		return "", fmt.Errorf("resolve T40.13 authority directory: %w", err)
	}
	return filepath.Join(parent, filepath.Base(filepath.Clean(path))), nil
}

func (identity exactFileIdentity) revalidate() error {
	if identity.path == "" || identity.maximum <= 0 || identity.description == "" {
		return errors.New("T40.13 admission identity is incomplete")
	}
	raw, err := readAtomicRegular(identity.path, identity.maximum)
	if err != nil {
		return fmt.Errorf("revalidate T40.13 %s identity: %w", identity.description, err)
	}
	if !bytes.Equal(raw, identity.raw) {
		return fmt.Errorf("T40.13 %s identity changed before admission", identity.description)
	}
	return nil
}

func readBoundedFile(path string, maximum int) ([]byte, error) {
	if maximum <= 0 {
		return nil, errors.New("T40.13 admission read bound is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(raw) > maximum {
		return nil, errors.New("T40.13 admission input exceeds its byte bound")
	}
	return raw, nil
}

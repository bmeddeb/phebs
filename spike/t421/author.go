package t421

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t4110"
)

type PlanIdentity struct {
	Bytes  uint64
	SHA256 string
}

// Author builds and atomically links one create-only frozen plan.
func Author(ctx context.Context, destination, repositoryRoot, sourceCommit string) (PlanIdentity, error) {
	commit, err := t4110.VerifyCleanCommit(ctx, repositoryRoot)
	if err != nil {
		return PlanIdentity{}, err
	}
	if commit != sourceCommit {
		return PlanIdentity{}, errors.New("T42.1 source commit differs from exact clean HEAD")
	}
	plan, err := BuildPlan(sourceCommit)
	if err != nil {
		return PlanIdentity{}, err
	}
	raw, err := MarshalCanonical(plan)
	if err != nil {
		return PlanIdentity{}, err
	}
	if err := sealPlan(destination, raw); err != nil {
		return PlanIdentity{}, err
	}
	return PlanIdentity{Bytes: uint64(len(raw)), SHA256: SHA256(raw)}, nil
}

func sealPlan(destination string, raw []byte) error {
	directory, base := filepath.Dir(destination), filepath.Base(destination)
	if destination == "" || base == "." || base == string(filepath.Separator) {
		return errors.New("T42.1 plan destination is invalid")
	}
	temporary, err := os.CreateTemp(directory, "."+base+".tmp-")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryName, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("T42.1 plan destination already exists")
		}
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return errors.Join(
			fmt.Errorf("open T42.1 plan directory: %w", err),
			removeLinkedPlan(temporaryName, destination),
		)
	}
	if err := directoryHandle.Sync(); err != nil {
		return errors.Join(
			fmt.Errorf("sync T42.1 plan directory: %w", err),
			directoryHandle.Close(), removeLinkedPlan(temporaryName, destination),
		)
	}
	if err := directoryHandle.Close(); err != nil {
		return errors.Join(
			fmt.Errorf("close T42.1 plan directory: %w", err),
			removeLinkedPlan(temporaryName, destination),
		)
	}
	return nil
}

func removeLinkedPlan(temporary, destination string) error {
	temporaryInfo, err := os.Lstat(temporary)
	if err != nil {
		return fmt.Errorf("inspect temporary T42.1 plan: %w", err)
	}
	destinationInfo, err := os.Lstat(destination)
	if err != nil {
		return fmt.Errorf("inspect linked T42.1 plan: %w", err)
	}
	if !temporaryInfo.Mode().IsRegular() || !destinationInfo.Mode().IsRegular() ||
		!os.SameFile(temporaryInfo, destinationInfo) {
		return errors.New("T42.1 linked plan identity changed before cleanup")
	}
	return os.Remove(destination)
}

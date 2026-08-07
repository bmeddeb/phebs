package observationpublication

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/internal/pipelinerefusal"
	"github.com/bmeddeb/phebs/internal/sourceobservation"
	"github.com/bmeddeb/phebs/internal/sourcepartition"
)

type Stage struct {
	root       string
	repository string
	generation string
	partition  sourcepartition.Manifest
	directory  string
	priorRead  bool
	prior      string
	priorErr   error
}

func Begin(root string, partition sourcepartition.Manifest) (*Stage, error) {
	if !filepath.IsAbs(root) {
		return nil, invalid("root must be absolute")
	}
	if err := sourcepartition.ValidateManifest(partition); err != nil {
		return nil, err
	}
	if err := checkPublicationLimit(
		ErrLimit, pipelinerefusal.DimensionGenerationRecords,
		int64(partition.BlobCount), MaxGenerationRecords,
	); err != nil {
		return nil, err
	}
	generation, err := GenerationDigest(partition)
	if err != nil {
		return nil, err
	}
	repositoryRoot := repositoryDirectory(root, partition.Repository)
	if err := os.MkdirAll(repositoryRoot, 0o700); err != nil {
		return nil, err
	}
	entries, err := boundedDirectory(repositoryRoot, MaxRepositoryGenerations+2)
	if err != nil {
		if errors.Is(err, ErrLimit) {
			return nil, checkPublicationLimit(
				ErrLimit, pipelinerefusal.DimensionRepositoryEntries,
				MaxRepositoryGenerations+3, MaxRepositoryGenerations+2,
			)
		}
		return nil, err
	}
	generations := 0
	for _, entry := range entries {
		name := entry.Name()
		_, collecting := collectingStageGeneration(name)
		if entry.IsDir() && (len(name) == 64 && validLowerHex(name) || collecting) {
			generations++
		}
	}
	if generations >= MaxRepositoryGenerations {
		return nil, checkPublicationLimit(
			ErrLimit, pipelinerefusal.DimensionRepositoryGenerations,
			int64(generations+1), MaxRepositoryGenerations,
		)
	}
	marker := Marker{Schema: MarkerSchema, Repository: partition.Repository, GenerationDigest: generation}
	if err := writeExclusiveCanonical(markerPath(root, partition.Repository), marker); err != nil {
		return nil, err
	}
	directory := generationDirectory(root, partition.Repository, generation)
	if err := os.Mkdir(directory, 0o700); err != nil {
		_ = os.Remove(markerPath(root, partition.Repository))
		return nil, err
	}
	if err := os.Mkdir(filepath.Join(directory, "objects"), 0o700); err != nil {
		_ = os.RemoveAll(directory)
		_ = os.Remove(markerPath(root, partition.Repository))
		return nil, err
	}
	if err := syncDirectory(directory); err != nil {
		return nil, err
	}
	if err := syncDirectory(repositoryRoot); err != nil {
		return nil, err
	}
	return &Stage{root: root, repository: partition.Repository, generation: generation, partition: partition, directory: directory}, nil
}

func (stage *Stage) BuildPartition(
	ctx context.Context, plan *sourcepartition.Plan, repositoryDirectory string,
	ordinal int, metrics *Metrics,
) error {
	if stage == nil || plan == nil || !filepath.IsAbs(repositoryDirectory) {
		return invalid("partition build input")
	}
	if plan.Manifest().Digest != stage.partition.Digest || ordinal < 0 || ordinal >= len(stage.partition.Members) {
		return invalid("partition plan identity")
	}
	memberPath := filepath.Join(stage.directory, memberName(ordinal))
	if _, err := os.Lstat(memberPath); err == nil {
		return stage.validateBuiltPartition(ctx, ordinal)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(memberPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(memberPath)
		}
	}()
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("phebs-observation-member-v1\x00"))
	writer := bufio.NewWriterSize(io.MultiWriter(file, hasher), 64<<10)
	var contentBytes int64
	readStats, err := plan.ReadPartition(ctx, repositoryDirectory, ordinal, func(blob sourcepartition.BlobRecord, content []byte) error {
		if metrics != nil {
			metrics.ReadBlobs++
		}
		record, raw, parseErr := stage.observe(ctx, blob, content, metrics)
		if parseErr != nil {
			return parseErr
		}
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if int64(len(line)+1) > MaxMemberBytes-contentBytes {
			return checkPublicationLimit(
				ErrLimit, pipelinerefusal.DimensionObservationMemberBytes,
				contentBytes+int64(len(line)+1), MaxMemberBytes,
			)
		}
		if _, err := writer.Write(line); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		contentBytes += int64(len(line) + 1)
		_ = raw
		return nil
	})
	if err != nil {
		return err
	}
	if readStats.Blobs != stage.partition.Members[ordinal].BlobCount {
		return invalid("source partition totals")
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func (stage *Stage) validateBuiltPartition(ctx context.Context, ordinal int) error {
	member, records, err := readMember(ctx, stage.directory, ordinal, len(stage.partition.Members))
	if err != nil {
		return err
	}
	expected := stage.partition.Members[ordinal]
	placements := 0
	var declared int64
	for _, record := range records {
		placements += len(record.Placements)
		declared += record.DeclaredBytes
		if record.State == "observed" {
			if err := validateObservation(stage.directory, record); err != nil {
				return err
			}
		}
	}
	if member.RecordCount != expected.BlobCount || placements != expected.PlacementCount ||
		declared != expected.DeclaredBytes || records[0].ObjectID != expected.FirstObjectID ||
		records[len(records)-1].ObjectID != expected.LastObjectID {
		return invalid("existing partition member")
	}
	return nil
}

func (stage *Stage) observe(ctx context.Context, blob sourcepartition.BlobRecord, content []byte, metrics *Metrics) (Record, []byte, error) {
	contentSum := sha256.Sum256(content)
	contentDigest := "sha256:" + hex.EncodeToString(contentSum[:])
	record := Record{
		Schema: RecordSchema, ObjectID: blob.ObjectID, DeclaredBytes: blob.DeclaredBytes,
		Placements: blob.Placements, ContentDigest: contentDigest,
	}
	name := observationName(sourceobservation.PolicyDigest(), contentDigest)
	destination := filepath.Join(stage.directory, name)
	if raw, ok, err := stage.existingObservation(destination, contentDigest); err != nil {
		return Record{}, nil, err
	} else if ok {
		record.State = "observed"
		record.ObservationName = name
		record.ObservationDigest = digest("phebs-observation-bytes-v1", string(raw))
		record.ObservationOrigin = stage.existingObservationOrigin(name, destination)
		return record, raw, nil
	}
	if reused, err := stage.reuseObservation(name, destination, contentDigest); err != nil {
		return Record{}, nil, err
	} else if reused {
		raw, err := os.ReadFile(destination)
		if err != nil {
			return Record{}, nil, err
		}
		record.State = "observed"
		record.ObservationName = name
		record.ObservationDigest = digest("phebs-observation-bytes-v1", string(raw))
		record.ObservationOrigin = "reused"
		if metrics != nil {
			metrics.ReusedObservations++
		}
		return record, raw, nil
	}
	path := "source.go"
	observation, err := sourceobservation.Parse(ctx, sourceobservation.Input{Path: path, Content: string(content)})
	if err != nil {
		record.State = "unsupported"
		record.Reason = classifyParseError(err)
		if metrics != nil {
			metrics.UnsupportedBlobs++
		}
		return record, nil, nil
	}
	raw, err := sourceobservation.Canonical(observation)
	if err != nil {
		return Record{}, nil, err
	}
	if len(raw) > MaxObservationBytes {
		record.State = "unsupported"
		record.Reason = "observation_too_large"
		if metrics != nil {
			metrics.UnsupportedBlobs++
		}
		return record, nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return Record{}, nil, err
	}
	origin := "parsed"
	if err := writeExclusiveBytes(destination, raw); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return Record{}, nil, err
		}
		existing, ok, existingErr := stage.existingObservation(destination, contentDigest)
		if existingErr != nil || !ok || !bytes.Equal(existing, raw) {
			return Record{}, nil, errors.Join(existingErr, invalid("concurrent observation mismatch"))
		}
		raw = existing
		origin = stage.existingObservationOrigin(name, destination)
	}
	record.State = "observed"
	record.ObservationName = name
	record.ObservationDigest = digest("phebs-observation-bytes-v1", string(raw))
	record.ObservationOrigin = origin
	if metrics != nil {
		metrics.ParsedBlobs++
		metrics.WrittenBytes += int64(len(raw))
	}
	return record, raw, nil
}

func (stage *Stage) existingObservationOrigin(name, destination string) string {
	prior, err := stage.priorGeneration()
	if err != nil || prior == "" || prior == stage.generation {
		return "parsed"
	}
	priorInfo, err := os.Lstat(filepath.Join(
		generationDirectory(stage.root, stage.repository, prior), name,
	))
	if err != nil || !priorInfo.Mode().IsRegular() {
		return "parsed"
	}
	destinationInfo, err := os.Lstat(destination)
	if err == nil && destinationInfo.Mode().IsRegular() && os.SameFile(priorInfo, destinationInfo) {
		return "reused"
	}
	return "parsed"
}

func (stage *Stage) existingObservation(path, contentDigest string) ([]byte, bool, error) {
	raw, err := readBoundedRegular(path, MaxObservationBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var observation sourceobservation.Observation
	if err := decodeCanonical(raw, &observation); err != nil || sourceobservation.Validate(observation) != nil ||
		observation.ContentDigest != contentDigest || observation.PolicyDigest != sourceobservation.PolicyDigest() {
		return nil, false, invalid("existing observation is incompatible")
	}
	return raw, true, nil
}

func (stage *Stage) reuseObservation(name, destination, contentDigest string) (bool, error) {
	priorGeneration, err := stage.priorGeneration()
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if priorGeneration == "" || priorGeneration == stage.generation {
		return false, nil
	}
	prior := generationDirectory(stage.root, stage.repository, priorGeneration)
	source := filepath.Join(prior, name)
	raw, err := readBoundedRegular(source, MaxObservationBytes)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var observation sourceobservation.Observation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&observation); err != nil || sourceobservation.Validate(observation) != nil ||
		observation.ContentDigest != contentDigest || observation.PolicyDigest != sourceobservation.PolicyDigest() {
		return false, invalid("reused observation is incompatible")
	}
	canonical, err := sourceobservation.Canonical(observation)
	if err != nil || !bytes.Equal(canonical, raw) {
		return false, invalid("reused observation is noncanonical")
	}
	if err := os.Link(source, destination); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return false, err
		}
	}
	existing, ok, existingErr := stage.existingObservation(destination, contentDigest)
	if existingErr != nil || !ok || !bytes.Equal(existing, raw) {
		return false, errors.Join(existingErr, invalid("concurrent reused observation mismatch"))
	}
	return true, nil
}

func (stage *Stage) priorGeneration() (string, error) {
	if stage.priorRead {
		return stage.prior, stage.priorErr
	}
	stage.priorRead = true
	pointer, err := readPointer(stage.root, stage.repository)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		stage.priorErr = err
		return "", err
	}
	stage.prior = pointer.GenerationDigest
	return stage.prior, nil
}

func classifyParseError(err error) string {
	if errors.Is(err, sourceobservation.ErrLimit) {
		return "source_limit"
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "UTF-8"):
		return "invalid_utf8"
	case strings.Contains(message, "parse failed"):
		return "parse_error"
	default:
		return "unsupported_source"
	}
}

func writeExclusiveBytes(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	written, writeErr := file.Write(raw)
	if writeErr != nil || written != len(raw) {
		return errors.Join(writeErr, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func writeExclusiveCanonical(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeExclusiveBytes(path, raw)
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

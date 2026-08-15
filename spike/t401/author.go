package t401

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // Git SHA-1 object identity, not a security primitive.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type AuthorRequest struct {
	ModuleRoot    string
	Output        string
	Profile       Profile
	ConfirmFrozen bool
	FailAt        string
}

const authorStateSchema = "t401-author-state-v1"

type authorState struct {
	Schema          string `json:"schema"`
	Profile         string `json:"profile"`
	ProfileDigest   string `json:"profile_digest"`
	GitInitialized  bool   `json:"git_initialized"`
	NextRevision    uint64 `json:"next_revision"`
	GitVerified     bool   `json:"git_verified"`
	MetadataWritten bool   `json:"metadata_written"`
}

func Author(ctx context.Context, request AuthorRequest) (Receipt, error) {
	if ctx == nil {
		return Receipt{}, errors.New("T40.1 author requires a context")
	}
	if err := ValidateProfile(request.Profile); err != nil {
		return Receipt{}, err
	}
	if request.Profile.Scale == "frozen" && !request.ConfirmFrozen {
		return Receipt{}, errors.New("frozen T40.1 authoring requires explicit confirmation")
	}
	if !validFailurePoint(request.FailAt) {
		return Receipt{}, errors.New("unknown T40.1 author failure point")
	}
	if !filepath.IsAbs(request.Output) || filepath.Clean(request.Output) == string(filepath.Separator) {
		return Receipt{}, errors.New("T40.1 author output must be an absolute non-root path")
	}
	if err := refuseWorktreeOutput(request.ModuleRoot, request.Output); err != nil {
		return Receipt{}, err
	}
	if _, err := os.Lstat(request.Output); err == nil || !os.IsNotExist(err) {
		return Receipt{}, errors.New("T40.1 author output must not already exist")
	}
	digest, err := ProfileDigest(request.Profile)
	if err != nil {
		return Receipt{}, err
	}
	parent := filepath.Dir(request.Output)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Receipt{}, err
	}
	stage := request.Output + ".building"
	state, exists, err := openAuthorState(stage, request.Profile.Name, digest)
	if err != nil {
		return Receipt{}, err
	}
	if !exists {
		if err := inject(request.FailAt, "before_git_init"); err != nil {
			return Receipt{}, err
		}
		if err := os.Mkdir(stage, 0o755); err != nil {
			return Receipt{}, err
		}
		state = authorState{Schema: authorStateSchema, Profile: request.Profile.Name, ProfileDigest: digest}
		if err := saveAuthorState(stage, state); err != nil {
			return Receipt{}, err
		}
	}
	repository := filepath.Join(stage, "repository.git")
	if !state.GitInitialized {
		if _, err := runGit(ctx, "", nil, "init", "--bare", "--quiet", "--initial-branch=main", repository); err != nil {
			return Receipt{}, err
		}
		for _, setting := range [][2]string{
			{"core.autocrlf", "false"}, {"core.filemode", "false"},
			{"commit.gpgsign", "false"}, {"zoekt.name", RepositoryName},
		} {
			if _, err := runGit(ctx, repository, nil, "config", setting[0], setting[1]); err != nil {
				return Receipt{}, err
			}
		}
		state.GitInitialized = true
		if err := saveAuthorState(stage, state); err != nil {
			return Receipt{}, err
		}
		if err := inject(request.FailAt, "after_git_init"); err != nil {
			return Receipt{}, err
		}
	}
	state, err = reconcileAuthorState(ctx, stage, repository, state)
	if err != nil {
		return Receipt{}, err
	}
	for state.NextRevision < uint64(len(request.Profile.Transitions)) {
		index := int(state.NextRevision)
		transition := request.Profile.Transitions[index]
		point := map[int]string{0: "revision_a", 1: "revision_b", 2: "revision_a_return"}[index]
		if err := inject(request.FailAt, "before_"+point); err != nil {
			return Receipt{}, err
		}
		if err := importRevision(ctx, repository, request.Profile, transition, index); err != nil {
			return Receipt{}, err
		}
		state.NextRevision++
		if err := saveAuthorState(stage, state); err != nil {
			return Receipt{}, err
		}
		if err := inject(request.FailAt, "after_"+point); err != nil {
			return Receipt{}, err
		}
	}
	if !state.GitVerified {
		if err := inject(request.FailAt, "before_git_verify"); err != nil {
			return Receipt{}, err
		}
		if _, err := runGit(ctx, repository, nil, "fsck", "--strict", "--no-dangling"); err != nil {
			return Receipt{}, err
		}
		state.GitVerified = true
		if err := saveAuthorState(stage, state); err != nil {
			return Receipt{}, err
		}
	}
	oracle, err := DeriveOracle(request.Profile)
	if err != nil {
		return Receipt{}, err
	}
	revisions, err := readRevisionIdentities(ctx, repository, request.Profile, oracle)
	if err != nil {
		return Receipt{}, err
	}
	logical, allocated, err := directoryBytes(ctx, repository)
	if err != nil {
		return Receipt{}, err
	}
	manifest := Manifest{
		Schema: ManifestSchema, Profile: request.Profile.Name,
		ProfileDigest: oracle.ProfileDigest, Repository: RepositoryName,
		Projection: request.Profile.Scale == "projection", Aggregate: oracle.Aggregate,
		Revisions: revisions, FailurePoints: failurePointNames(),
		ByteMetrics: []ByteMetric{
			{Kind: "declared_repository_go", Measurement: "exact", Bytes: oracle.Aggregate.DeclaredRepositoryGoBytes},
			{Kind: "logical_source", Measurement: "exact", Bytes: oracle.Aggregate.LogicalSourceBytes},
		},
	}
	if err := ValidateManifest(manifest, request.Profile, oracle); err != nil {
		return Receipt{}, err
	}
	if !state.MetadataWritten {
		if err := inject(request.FailAt, "before_metadata"); err != nil {
			return Receipt{}, err
		}
	}
	profileBytes, err := MarshalCanonical(request.Profile)
	if err != nil {
		return Receipt{}, err
	}
	oracleBytes, err := MarshalCanonical(oracle)
	if err != nil {
		return Receipt{}, err
	}
	manifestBytes, err := MarshalCanonical(manifest)
	if err != nil {
		return Receipt{}, err
	}
	artifacts := []struct {
		name    string
		content []byte
	}{
		{name: "profile.json", content: profileBytes},
		{name: "oracle.json", content: oracleBytes},
		{name: "manifest.json", content: manifestBytes},
	}
	identities := make([]ArtifactIdentity, 0, len(artifacts))
	for _, artifact := range artifacts {
		if err := writeExact(stage, artifact.name, artifact.content); err != nil {
			return Receipt{}, err
		}
		identities = append(identities, ArtifactIdentity{
			Name: artifact.name, Bytes: uint64(len(artifact.content)), SHA256: SHA256(artifact.content),
		})
	}
	gitVersion, err := runGit(ctx, repository, nil, "--version")
	if err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{
		Schema: ReceiptSchema, Outcome: "authored", Profile: request.Profile.Name,
		ProfileDigest: oracle.ProfileDigest, Artifacts: identities, Revisions: revisions,
		Git: ToolIdentity{Name: "git", Version: strings.TrimSpace(gitVersion)}, Claims: request.Profile.Claims,
		HistoricalDiagnostic: FrozenHistoricalDiagnostic(),
		ByteMetrics: []ByteMetric{
			{Kind: "profile_canonical", Measurement: "exact", Bytes: uint64(len(profileBytes))},
			{Kind: "oracle_canonical", Measurement: "exact", Bytes: uint64(len(oracleBytes))},
			{Kind: "manifest_canonical", Measurement: "exact", Bytes: uint64(len(manifestBytes))},
			{Kind: "declared_repository_go", Measurement: "exact", Bytes: oracle.Aggregate.DeclaredRepositoryGoBytes},
			{Kind: "logical_source", Measurement: "exact", Bytes: oracle.Aggregate.LogicalSourceBytes},
			{Kind: "bare_git_logical", Measurement: "exact", Bytes: logical},
			{Kind: "bare_git_allocated", Measurement: "exact", Bytes: allocated},
		},
	}
	if err := ValidateReceipt(receipt, request.Profile, manifest); err != nil {
		return Receipt{}, err
	}
	receiptBytes, err := MarshalCanonical(receipt)
	if err != nil {
		return Receipt{}, err
	}
	if err := writeExact(stage, "receipt.json", receiptBytes); err != nil {
		return Receipt{}, err
	}
	state.MetadataWritten = true
	if err := saveAuthorState(stage, state); err != nil {
		return Receipt{}, err
	}
	if err := syncDirectory(stage); err != nil {
		return Receipt{}, err
	}
	if err := inject(request.FailAt, "before_publish"); err != nil {
		return Receipt{}, err
	}
	if _, err := runGit(ctx, repository, nil, "fsck", "--strict", "--no-dangling"); err != nil {
		return Receipt{}, err
	}
	if err := os.Rename(stage, request.Output); err != nil {
		return Receipt{}, err
	}
	if err := syncDirectory(parent); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func refuseWorktreeOutput(moduleRoot, output string) error {
	if !filepath.IsAbs(moduleRoot) || filepath.Clean(moduleRoot) == string(filepath.Separator) {
		return errors.New("T40.1 author module root must be an absolute non-root path")
	}
	resolvedRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		return errors.New("T40.1 author module root is unavailable")
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(output))
	if err != nil {
		return errors.New("T40.1 author output parent is unavailable")
	}
	candidate := filepath.Join(resolvedParent, filepath.Base(output))
	relative, err := filepath.Rel(resolvedRoot, candidate)
	if err != nil {
		return errors.New("T40.1 author output boundary cannot be established")
	}
	if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("T40.1 giant author output may not be inside the module worktree")
	}
	return nil
}

func openAuthorState(stage, profile, digest string) (authorState, bool, error) {
	info, err := os.Lstat(stage)
	if os.IsNotExist(err) {
		return authorState{}, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return authorState{}, false, errors.New("T40.1 author building target is not a regular directory")
	}
	checkpoint := filepath.Join(stage, "author-state.json")
	checkpointInfo, err := os.Lstat(checkpoint)
	if err != nil || !checkpointInfo.Mode().IsRegular() || checkpointInfo.Mode()&os.ModeSymlink != 0 {
		return authorState{}, false, errors.New("T40.1 author building checkpoint is unavailable")
	}
	data, err := os.ReadFile(checkpoint)
	if err != nil {
		return authorState{}, false, errors.New("T40.1 author building checkpoint is unavailable")
	}
	state, err := DecodeStrict[authorState](data)
	if err != nil || state.Schema != authorStateSchema || state.Profile != profile || state.ProfileDigest != digest ||
		state.NextRevision > 3 || state.GitVerified && state.NextRevision != 3 ||
		state.MetadataWritten && !state.GitVerified {
		return authorState{}, false, errors.New("T40.1 author building checkpoint is invalid")
	}
	return state, true, nil
}

func reconcileAuthorState(ctx context.Context, stage, repository string, state authorState) (authorState, error) {
	countOutput, err := runGit(ctx, repository, nil, "rev-list", "--count", "--all")
	if err != nil {
		return authorState{}, err
	}
	count, err := strconv.ParseUint(strings.TrimSpace(countOutput), 10, 64)
	if err != nil || count > 3 || count < state.NextRevision {
		return authorState{}, errors.New("T40.1 author repository differs from its checkpoint")
	}
	if count > state.NextRevision {
		state.NextRevision = count
		state.GitVerified = false
		state.MetadataWritten = false
		if err := saveAuthorState(stage, state); err != nil {
			return authorState{}, err
		}
	}
	return state, nil
}

func saveAuthorState(stage string, state authorState) error {
	content, err := MarshalCanonical(state)
	if err != nil {
		return err
	}
	temporary := filepath.Join(stage, "author-state.json.tmp")
	if info, lstatErr := os.Lstat(temporary); lstatErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("T40.1 author temporary checkpoint is a symlink")
	} else if lstatErr != nil && !os.IsNotExist(lstatErr) {
		return lstatErr
	}
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temporary, filepath.Join(stage, "author-state.json")); err != nil {
		return err
	}
	return syncDirectory(stage)
}

func importRevision(ctx context.Context, repository string, profile Profile, transition Transition, index int) (returnErr error) {
	parent := ""
	if index > 0 {
		var err error
		parent, err = runGit(ctx, repository, nil, "rev-parse", "refs/heads/main")
		if err != nil {
			return err
		}
		parent = strings.TrimSpace(parent)
		if !validObjectID(parent) {
			return errors.New("T40.1 parent revision is invalid")
		}
	}
	command := exec.CommandContext(ctx, "git", "fast-import", "--quiet", "--date-format=raw")
	command.Dir = repository
	command.Env = deterministicGitEnvironment(nil)
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	var stderr boundedBuffer
	stderr.maximum = 8 << 10
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()
	writer := bufio.NewWriterSize(stdin, 64<<10)
	if err := writeFastImportCommit(writer, profile, transition, index, parent); err != nil {
		return err
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := stdin.Close(); err != nil {
		return err
	}
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("T40.1 git fast-import failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	finished = true
	return nil
}

func writeFastImportCommit(writer *bufio.Writer, profile Profile, transition Transition, index int, parent string) error {
	if index == 0 && profile.Kind == "structural" {
		return writeStructuralBaseline(writer, profile, transition)
	}
	message := "fixture: T40.1 " + transition.Revision
	if _, err := fmt.Fprintln(writer, "commit refs/heads/main"); err != nil {
		return err
	}
	if err := writeCommitIdentity(writer, profile, message); err != nil {
		return err
	}
	if index == 0 {
		if _, err := fmt.Fprintln(writer, "deleteall"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(writer, "from "+parent); err != nil {
		return err
	}
	if index == 0 {
		if err := walkRevision(profile, transition.Revision, func(file fileContent) error {
			if _, err := fmt.Fprintf(writer, "M 100644 inline %s\n", file.Path); err != nil {
				return err
			}
			return writeFastImportData(writer, file.Content)
		}); err != nil {
			return err
		}
	} else {
		changed, err := firstEligibleFile(profile, transition.Revision)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "M 100644 inline %s\n", changed.Path); err != nil {
			return err
		}
		if err := writeFastImportData(writer, changed.Content); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(writer)
	return err
}

func writeStructuralBaseline(writer *bufio.Writer, profile Profile, transition Transition) error {
	marks := make([]int, 0, len(frozenControls)+int(profile.Shape.GoBlobReuseClasses))
	for _, control := range frozenControls {
		marks = append(marks, len(marks)+1)
		if err := writeMarkedBlob(writer, marks[len(marks)-1], control.Content); err != nil {
			return err
		}
	}
	for ordinal := uint64(0); ordinal < profile.Shape.GoBlobReuseClasses; ordinal++ {
		content, err := goContent(profile, "metadata_reused_go", ordinal, false)
		if err != nil {
			return err
		}
		marks = append(marks, len(marks)+1)
		if err := writeMarkedBlob(writer, marks[len(marks)-1], content); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(writer, "commit refs/heads/main"); err != nil {
		return err
	}
	if err := writeCommitIdentity(writer, profile, "fixture: T40.1 "+transition.Revision); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(writer, "deleteall"); err != nil {
		return err
	}
	for index, control := range frozenControls {
		if _, err := fmt.Fprintf(writer, "M 100644 :%d %s\n", marks[index], control.Path); err != nil {
			return err
		}
	}
	var goFiles uint64
	for cell := uint64(0); cell < profile.Shape.Cells; cell++ {
		for offset := uint64(0); offset < profile.Shape.EligibleGoPathsPerCell; offset++ {
			ordinal := cell*profile.Shape.EligibleGoPathsPerCell + offset
			goFiles++
			if err := CheckDimension("repository_lane_placements", goFiles, profile.Aggregate.RepositoryLanePlacements); err != nil {
				return err
			}
			path := fmt.Sprintf("structural/cells/b%03d/c%05d/f%03d.go", cell/100, cell, offset)
			mark := marks[len(frozenControls)+int(ordinal%profile.Shape.GoBlobReuseClasses)]
			if _, err := fmt.Fprintf(writer, "M 100644 :%d %s\n", mark, path); err != nil {
				return err
			}
		}
	}
	if goFiles != profile.Aggregate.EligibleGoFiles ||
		profile.Aggregate.PhysicalOwners != goFiles+uint64(len(frozenControls)) {
		return errors.New("T40.1 structural placement count differs from the frozen profile")
	}
	_, err := fmt.Fprintln(writer)
	return err
}

func writeMarkedBlob(writer *bufio.Writer, mark int, content []byte) error {
	if _, err := fmt.Fprintf(writer, "blob\nmark :%d\n", mark); err != nil {
		return err
	}
	return writeFastImportData(writer, content)
}

func writeCommitIdentity(writer *bufio.Writer, profile Profile, message string) error {
	identity := profile.Generator
	if _, err := fmt.Fprintf(
		writer, "author %s <%s> %d +0000\ncommitter %s <%s> %d +0000\n",
		identity.AuthorName, identity.AuthorEmail, identity.Timestamp,
		identity.AuthorName, identity.AuthorEmail, identity.Timestamp,
	); err != nil {
		return err
	}
	return writeFastImportData(writer, []byte(message))
}

func writeFastImportData(writer *bufio.Writer, content []byte) error {
	if _, err := fmt.Fprintf(writer, "data %d\n", len(content)); err != nil {
		return err
	}
	if _, err := writer.Write(content); err != nil {
		return err
	}
	return writer.WriteByte('\n')
}

func walkRevision(profile Profile, revision string, visitor func(fileContent) error) error {
	if err := ValidateProfile(profile); err != nil {
		return err
	}
	if revision != "a" && revision != "b" && revision != "a-return" {
		return errors.New("unknown T40.1 revision")
	}
	accumulator := profileAccumulator{profile: profile, revision: revision}
	for _, control := range frozenControls {
		if err := accumulator.visit(control, visitor); err != nil {
			return err
		}
	}
	switch profile.Kind {
	case "structural":
		for cell := uint64(0); cell < profile.Shape.Cells; cell++ {
			for offset := uint64(0); offset < profile.Shape.EligibleGoPathsPerCell; offset++ {
				ordinal := cell*profile.Shape.EligibleGoPathsPerCell + offset
				path := fmt.Sprintf(
					"structural/cells/b%03d/c%05d/f%03d.go", cell/100, cell, offset,
				)
				content, err := goContent(profile, "metadata_reused_go", ordinal, revision == "b" && ordinal == 0)
				if err != nil {
					return err
				}
				if err := accumulator.visit(fileContent{
					Path: path, Kind: "go", Family: "metadata_reused_go", Content: content,
				}, visitor); err != nil {
					return err
				}
			}
		}
	case "semantic":
		goFiles := profile.Shape.Cells * profile.Shape.EligibleGoPathsPerCell
		for ordinal := uint64(0); ordinal < goFiles; ordinal++ {
			family, err := familyAt(profile, "go", ordinal)
			if err != nil {
				return err
			}
			content, err := goContent(profile, family.Name, ordinal, revision == "b" && ordinal == 0)
			if err != nil {
				return err
			}
			if err := accumulator.visit(fileContent{
				Path: fmt.Sprintf("semantic/go/b%03d/f%06d.go", ordinal/1_024, ordinal),
				Kind: "go", Family: family.Name, Content: content,
			}, visitor); err != nil {
				return err
			}
		}
		var idlFiles uint64
		for _, family := range profile.Families {
			if family.Plane == "idl" {
				idlFiles += family.Inputs
			}
		}
		for ordinal := uint64(0); ordinal < idlFiles; ordinal++ {
			family, err := familyAt(profile, "idl", ordinal)
			if err != nil {
				return err
			}
			content, err := idlContent(profile, family, ordinal)
			if err != nil {
				return err
			}
			if err := accumulator.visit(fileContent{
				Path: fmt.Sprintf("semantic/idl/b%03d/f%05d%s", ordinal/1_024, ordinal, family.Suffix),
				Kind: "idl", Family: family.Name, Content: content,
			}, visitor); err != nil {
				return err
			}
		}
	default:
		return errors.New("unknown T40.1 profile kind")
	}
	return accumulator.finish()
}

type profileAccumulator struct {
	profile  Profile
	revision string
	previous string
	actual   Aggregate
}

func (accumulator *profileAccumulator) visit(file fileContent, visitor func(fileContent) error) error {
	if file.Path == "" || len(file.Content) == 0 ||
		accumulator.previous != "" && file.Path <= accumulator.previous {
		return errors.New("T40.1 author path order is invalid")
	}
	accumulator.previous = file.Path
	next := accumulator.actual
	next.RegularFiles++
	next.LogicalSourceBytes += uint64(len(file.Content))
	switch file.Kind {
	case "control":
		next.PhysicalOwners++
		next.ControlFiles++
		next.ControlBytes += uint64(len(file.Content))
	case "go":
		next.PhysicalOwners++
		next.EligibleGoFiles++
		next.RepositoryLanePlacements++
		next.DeclaredRepositoryGoBytes += uint64(len(file.Content))
		ordinal := next.EligibleGoFiles - 1
		if ordinal%accumulator.profile.Shape.EligibleGoPathsPerCell < accumulator.profile.Shape.CallerPathsPerCell {
			next.CallerLanePlacements++
		}
	case "idl":
		next.PhysicalOwners++
		next.IDLCandidates++
		next.UniqueIDLBlobs++
	default:
		return errors.New("T40.1 author file kind is invalid")
	}
	for _, dimension := range accumulator.profile.Dimensions {
		var observed uint64
		switch dimension.Name {
		case "physical_owners":
			observed = next.PhysicalOwners
		case "repository_lane_placements":
			observed = next.RepositoryLanePlacements
		case "caller_lane_placements":
			observed = next.CallerLanePlacements
		case "declared_repository_go_bytes":
			observed = next.DeclaredRepositoryGoBytes
		case "logical_source_bytes":
			observed = next.LogicalSourceBytes
		case "idl_candidates":
			observed = next.IDLCandidates
		case "control_files":
			observed = next.ControlFiles
		default:
			continue
		}
		if err := CheckDimension(dimension.Name, observed, dimension.Admitted); err != nil {
			return err
		}
	}
	if err := visitor(file); err != nil {
		return err
	}
	accumulator.actual = next
	return nil
}

func (accumulator *profileAccumulator) finish() error {
	want := accumulator.profile.Aggregate
	if accumulator.revision == "b" && accumulator.profile.Kind == "structural" {
		want.UniqueGoBlobs++
	}
	accumulator.actual.UniqueGoBlobs = want.UniqueGoBlobs
	accumulator.actual.ModeledDomainPartitions = want.ModeledDomainPartitions
	accumulator.actual.ModeledDomainMembers = want.ModeledDomainMembers
	accumulator.actual.DomainPartitionModel = want.DomainPartitionModel
	accumulator.actual.ObservationRecords = want.ObservationRecords
	accumulator.actual.ObservationGaps = want.ObservationGaps
	accumulator.actual.ExtractorFacts = want.ExtractorFacts
	accumulator.actual.ExtractorGapFacts = want.ExtractorGapFacts
	accumulator.actual.ExtractorStagingRows = want.ExtractorStagingRows
	accumulator.actual.CanonicalObservationBytes = want.CanonicalObservationBytes
	accumulator.actual.CanonicalExtractorBytes = want.CanonicalExtractorBytes
	accumulator.actual.CanonicalResultByteModel = want.CanonicalResultByteModel
	if accumulator.actual != want {
		return fmt.Errorf("T40.1 authored aggregate differs: got %+v want %+v", accumulator.actual, want)
	}
	return nil
}

func firstEligibleFile(profile Profile, revision string) (fileContent, error) {
	var result fileContent
	errStop := errors.New("T40.1 first file found")
	err := walkRevision(profile, revision, func(file fileContent) error {
		if file.Kind != "go" {
			return nil
		}
		result = file
		return errStop
	})
	if !errors.Is(err, errStop) {
		if err == nil {
			err = errors.New("T40.1 profile has no eligible Go file")
		}
		return fileContent{}, err
	}
	return result, nil
}

func familyAt(profile Profile, plane string, ordinal uint64) (TemplateFamily, error) {
	for _, family := range profile.Families {
		if family.Plane != plane {
			continue
		}
		if ordinal < family.Inputs {
			return family, nil
		}
		ordinal -= family.Inputs
	}
	return TemplateFamily{}, errors.New("T40.1 template family ordinal is out of range")
}

func goContent(profile Profile, family string, ordinal uint64, changed bool) ([]byte, error) {
	identity := ordinal
	if profile.Kind == "structural" {
		identity %= profile.Shape.GoBlobReuseClasses
	}
	change := "a"
	if changed {
		change = "b"
	}
	marker := fmt.Sprintf("%s:T401:%s:%s:%09d:%s", profile.Seed, profile.Kind, family, identity, change)
	var prefix string
	switch family {
	case "metadata_reused_go":
		prefix = fmt.Sprintf("package neutral\n\nconst T401Fixture = %q\n\n// padding ", marker)
	case "go_rpc_resolved":
		prefix = fmt.Sprintf("package neutral\nimport rpc \"example.invalid/rpc\"\nfunc t401(c rpc.Client) { c.Ping() }\n// %s padding ", marker)
	case "go_kafka_literal":
		prefix = fmt.Sprintf("package neutral\nimport \"github.com/IBM/sarama\"\nfunc t401(p sarama.SyncProducer) { p.SendMessage(&sarama.ProducerMessage{Topic: \"t401-events\"}) }\n// %s padding ", marker)
	case "go_dynamic_call":
		prefix = fmt.Sprintf("package neutral\nimport rpc \"example.invalid/rpc\"\nfunc t401() { dynamic().Ping() }\n// %s padding ", marker)
	case "go_dynamic_topic":
		prefix = fmt.Sprintf("package neutral\nimport \"github.com/IBM/sarama\"\nvar topic string\nfunc t401(p sarama.SyncProducer) { p.SendMessage(&sarama.ProducerMessage{Topic: topic}) }\n// %s padding ", marker)
	default:
		return nil, errors.New("T40.1 Go template family is unsupported")
	}
	return padded(prefix, profile.Shape.GoFileBytes)
}

// FrozenSemanticGoFixture returns one exact baseline-A Go input from the
// retained semantic author. It is intentionally narrow: readiness diagnostics
// can measure real extractor output without copying the frozen templates or
// materializing the complete Git repository.
func FrozenSemanticGoFixture(profile Profile, family string, ordinal uint64) (string, []byte, error) {
	if profile.Name != SemanticProfileName || profile.Kind != "semantic" || profile.Scale != "frozen" {
		return "", nil, errors.New("T40.1 semantic Go fixture requires the frozen semantic profile")
	}
	start := uint64(0)
	for _, candidate := range profile.Families {
		if candidate.Plane != "go" {
			continue
		}
		end, addErr := add(start, candidate.Inputs)
		if addErr != nil {
			return "", nil, addErr
		}
		if candidate.Name == family {
			if ordinal < start || ordinal >= end {
				return "", nil, errors.New("T40.1 semantic Go fixture ordinal is outside its family")
			}
			content, contentErr := goContent(profile, family, ordinal, false)
			if contentErr != nil {
				return "", nil, contentErr
			}
			return fmt.Sprintf("semantic/go/b%03d/f%06d.go", ordinal/1_024, ordinal), content, nil
		}
		start = end
	}
	return "", nil, errors.New("T40.1 semantic Go fixture family is unknown")
}

// FrozenStructuralGoFixture returns one exact baseline-A structural Go input
// without authoring the two-million-owner repository.
func FrozenStructuralGoFixture(profile Profile, ordinal uint64) (string, []byte, error) {
	if profile.Name != StructuralProfileName || profile.Kind != "structural" || profile.Scale != "frozen" {
		return "", nil, errors.New("T40.1 structural Go fixture requires the frozen structural profile")
	}
	inputs := profile.Shape.Cells * profile.Shape.EligibleGoPathsPerCell
	if ordinal >= inputs {
		return "", nil, errors.New("T40.1 structural Go fixture ordinal is outside its family")
	}
	content, err := goContent(profile, "metadata_reused_go", ordinal, false)
	if err != nil {
		return "", nil, err
	}
	cell := ordinal / profile.Shape.EligibleGoPathsPerCell
	offset := ordinal % profile.Shape.EligibleGoPathsPerCell
	return fmt.Sprintf(
		"structural/cells/b%03d/c%05d/f%03d.go", cell/100, cell, offset,
	), content, nil
}

// FrozenCallerControlFixture returns the exact committed go.mod selected by
// the caller candidate policy for either frozen profile.
func FrozenCallerControlFixture(profile Profile) (string, []byte, error) {
	if profile.Scale != "frozen" ||
		(profile.Kind != "structural" && profile.Kind != "semantic") {
		return "", nil, errors.New("T40.1 caller control fixture requires a frozen profile")
	}
	for _, control := range frozenControls {
		if control.Path == "go.mod" {
			return control.Path, slices.Clone(control.Content), nil
		}
	}
	return "", nil, errors.New("T40.1 frozen caller control fixture is absent")
}

func idlContent(profile Profile, family TemplateFamily, ordinal uint64) ([]byte, error) {
	var prefix string
	switch family.Name {
	case "proto_supported":
		prefix = fmt.Sprintf("syntax = \"proto3\";\npackage neutral.t401;\nmessage M%05d {}\n// %s ", ordinal, profile.Seed)
	case "proto_unresolved_type":
		prefix = fmt.Sprintf("syntax = \"proto3\";\npackage neutral.t401;\nmessage Gap%05d { Missing value = 1; }\n// %s ", ordinal, profile.Seed)
	case "thrift_supported":
		prefix = fmt.Sprintf("namespace go neutral.t401\nstruct S%05d {}\n// %s ", ordinal, profile.Seed)
	case "thrift_typedef_cycle":
		name := fmt.Sprintf("%05d", ordinal)
		prefix = fmt.Sprintf("namespace go neutral.t401\ntypedef B%s A%s\ntypedef A%s B%s\nstruct Gap%s { 1: A%s value }\n// %s ", name, name, name, name, name, name, profile.Seed)
	default:
		return nil, errors.New("T40.1 IDL template family is unsupported")
	}
	return padded(prefix, profile.Shape.IDLFileBytes)
}

func padded(prefix string, size uint64) ([]byte, error) {
	if size > 4<<20 || uint64(len(prefix))+1 > size {
		return nil, errors.New("T40.1 template does not fit its bounded file size")
	}
	content := bytes.Repeat([]byte{'x'}, int(size))
	copy(content, prefix)
	content[len(content)-1] = '\n'
	return content, nil
}

func readRevisionIdentities(ctx context.Context, repository string, profile Profile, oracle Oracle) ([]RevisionIdentity, error) {
	output, err := runGit(ctx, repository, nil, "rev-list", "--reverse", "refs/heads/main")
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(output)
	if len(commits) != len(oracle.Revisions) {
		return nil, errors.New("T40.1 repository has an unexpected commit count")
	}
	revisions := make([]RevisionIdentity, 0, len(commits))
	for index, commit := range commits {
		tree, err := runGit(ctx, repository, nil, "rev-parse", commit+"^{tree}")
		if err != nil {
			return nil, err
		}
		expected := oracle.Revisions[index]
		regularFiles, uniqueGoBlobs, err := inspectRevisionTree(ctx, repository, commit, profile, expected.Revision)
		if err != nil {
			return nil, err
		}
		if regularFiles != expected.RegularFiles || uniqueGoBlobs != expected.UniqueGoBlobs {
			return nil, errors.New("T40.1 authored tree aggregate differs from the independent oracle")
		}
		revisions = append(revisions, RevisionIdentity{
			Revision: expected.Revision, Commit: commit, Tree: strings.TrimSpace(tree),
			RegularFiles: regularFiles, UniqueGoBlobs: uniqueGoBlobs,
			ChangedFiles: expected.ChangedFiles,
		})
	}
	return revisions, nil
}

func inspectRevisionTree(
	ctx context.Context,
	repository, commit string,
	profile Profile,
	revision string,
) (uint64, uint64, error) {
	command := exec.CommandContext(ctx, "git", "ls-tree", "-r", "-l", "-z", commit)
	command.Dir = repository
	command.Env = deterministicGitEnvironment(nil)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return 0, 0, err
	}
	var stderr boundedBuffer
	stderr.maximum = 8 << 10
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return 0, 0, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Split(splitNUL)
	scanner.Buffer(make([]byte, 4<<10), 16<<10)
	structuralOIDs, err := structuralTemplateOIDs(profile)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return 0, 0, err
	}
	var count uint64
	for scanner.Scan() {
		fieldsAndPath := bytes.SplitN(scanner.Bytes(), []byte{'\t'}, 2)
		if len(fieldsAndPath) != 2 {
			_ = command.Process.Kill()
			_ = command.Wait()
			return 0, 0, errors.New("T40.1 Git tree record is malformed")
		}
		fields := strings.Fields(string(fieldsAndPath[0]))
		if len(fields) != 4 || fields[0] != "100644" || fields[1] != "blob" || !validObjectID(fields[2]) {
			_ = command.Process.Kill()
			_ = command.Wait()
			return 0, 0, errors.New("T40.1 Git tree record has an unsupported shape")
		}
		size, parseErr := strconv.ParseUint(fields[3], 10, 64)
		if parseErr != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return 0, 0, errors.New("T40.1 Git tree record size is invalid")
		}
		path, expectedSize, oid, expectationErr := expectedTreeRecord(profile, revision, count, structuralOIDs)
		if expectationErr != nil || string(fieldsAndPath[1]) != path || size != expectedSize || fields[2] != oid {
			_ = command.Process.Kill()
			_ = command.Wait()
			if expectationErr != nil {
				return 0, 0, expectationErr
			}
			return 0, 0, fmt.Errorf("T40.1 Git tree differs at record %d", count)
		}
		count++
		if err := CheckDimension("physical_owners", count, profile.Aggregate.PhysicalOwners); err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return 0, 0, err
		}
	}
	scanErr := scanner.Err()
	waitErr := command.Wait()
	if scanErr != nil {
		return 0, 0, scanErr
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		return 0, 0, errors.New("T40.1 Git tree inventory failed")
	}
	if count != profile.Aggregate.RegularFiles {
		return 0, 0, errors.New("T40.1 Git tree file count differs from the profile")
	}
	unique := profile.Aggregate.UniqueGoBlobs
	if revision == "b" && profile.Kind == "structural" {
		unique++
	}
	return count, unique, nil
}

func structuralTemplateOIDs(profile Profile) ([]string, error) {
	if profile.Kind != "structural" {
		return nil, nil
	}
	result := make([]string, profile.Shape.GoBlobReuseClasses)
	for ordinal := range profile.Shape.GoBlobReuseClasses {
		content, err := goContent(profile, "metadata_reused_go", ordinal, false)
		if err != nil {
			return nil, err
		}
		result[ordinal] = gitBlobOID(content)
	}
	return result, nil
}

func expectedTreeRecord(
	profile Profile,
	revision string,
	index uint64,
	structuralOIDs []string,
) (string, uint64, string, error) {
	if index < uint64(len(frozenControls)) {
		file := frozenControls[index]
		return file.Path, uint64(len(file.Content)), gitBlobOID(file.Content), nil
	}
	ordinal := index - uint64(len(frozenControls))
	changed := revision == "b" && ordinal == 0
	if profile.Kind == "structural" {
		if ordinal >= profile.Aggregate.EligibleGoFiles {
			return "", 0, "", errors.New("T40.1 structural tree has excess records")
		}
		path := fmt.Sprintf(
			"structural/cells/b%03d/c%05d/f%03d.go",
			(ordinal/profile.Shape.EligibleGoPathsPerCell)/100,
			ordinal/profile.Shape.EligibleGoPathsPerCell,
			ordinal%profile.Shape.EligibleGoPathsPerCell,
		)
		oid := structuralOIDs[ordinal%profile.Shape.GoBlobReuseClasses]
		if changed {
			content, err := goContent(profile, "metadata_reused_go", ordinal, true)
			if err != nil {
				return "", 0, "", err
			}
			oid = gitBlobOID(content)
		}
		return path, profile.Shape.GoFileBytes, oid, nil
	}
	if ordinal < profile.Aggregate.EligibleGoFiles {
		family, err := familyAt(profile, "go", ordinal)
		if err != nil {
			return "", 0, "", err
		}
		content, err := goContent(profile, family.Name, ordinal, changed)
		if err != nil {
			return "", 0, "", err
		}
		return fmt.Sprintf("semantic/go/b%03d/f%06d.go", ordinal/1_024, ordinal),
			profile.Shape.GoFileBytes, gitBlobOID(content), nil
	}
	idlOrdinal := ordinal - profile.Aggregate.EligibleGoFiles
	if idlOrdinal >= profile.Aggregate.IDLCandidates {
		return "", 0, "", errors.New("T40.1 semantic tree has excess records")
	}
	family, err := familyAt(profile, "idl", idlOrdinal)
	if err != nil {
		return "", 0, "", err
	}
	content, err := idlContent(profile, family, idlOrdinal)
	if err != nil {
		return "", 0, "", err
	}
	return fmt.Sprintf("semantic/idl/b%03d/f%05d%s", idlOrdinal/1_024, idlOrdinal, family.Suffix),
		profile.Shape.IDLFileBytes, gitBlobOID(content), nil
}

func gitBlobOID(content []byte) string {
	hash := sha1.New() //nolint:gosec // Git SHA-1 object identity, not a security primitive.
	_, _ = fmt.Fprintf(hash, "blob %d\x00", len(content))
	_, _ = hash.Write(content)
	return hex.EncodeToString(hash.Sum(nil))
}

func splitNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, 0); index >= 0 {
		return index + 1, data[:index], nil
	}
	if atEOF && len(data) != 0 {
		return 0, nil, errors.New("T40.1 Git tree inventory lacks NUL termination")
	}
	return 0, nil, nil
}

func directoryBytes(ctx context.Context, root string) (uint64, uint64, error) {
	var logical uint64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("T40.1 authored repository contains a symlink")
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		logical += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	output, err := runCommand(ctx, "", deterministicBaseEnvironment(), "du", "-sk", root)
	if err != nil {
		return 0, 0, errors.New("measure T40.1 allocated repository bytes")
	}
	fields := strings.Fields(output)
	if len(fields) < 1 {
		return 0, 0, errors.New("allocated byte measurement is empty")
	}
	kibibytes, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, 0, errors.New("allocated byte measurement is invalid")
	}
	allocated, err := multiply(kibibytes, 1_024)
	if err != nil {
		return 0, 0, err
	}
	return logical, allocated, nil
}

func writeExclusive(root, relative string, content []byte) error {
	target := filepath.Join(root, filepath.FromSlash(relative))
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err = file.Write(content); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func writeExact(root, relative string, content []byte) error {
	target := filepath.Join(root, filepath.FromSlash(relative))
	existing, err := os.ReadFile(target)
	if err == nil {
		if !bytes.Equal(existing, content) {
			return errors.New("T40.1 resumed artifact differs from the checkpoint")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return writeExclusive(root, relative, content)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func inject(selected, point string) error {
	if selected == point {
		return fmt.Errorf("%w: %s", ErrInjected, point)
	}
	return nil
}

func runGit(ctx context.Context, directory string, extraEnvironment []string, arguments ...string) (string, error) {
	return runCommand(ctx, directory, deterministicGitEnvironment(extraEnvironment), "git", arguments...)
}

func runCommand(ctx context.Context, directory string, environment []string, name string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = environment
	var stdout boundedBuffer
	stdout.maximum = 1 << 20
	var stderr boundedBuffer
	stderr.maximum = 8 << 10
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s command failed: %w", name, err)
	}
	return stdout.String(), nil
}

func deterministicGitEnvironment(extra []string) []string {
	environment := deterministicBaseEnvironment()
	environment = append(environment,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1",
	)
	return append(environment, extra...)
}

func deterministicBaseEnvironment() []string {
	allowed := []string{"PATH=", "TMPDIR=", "TEMP=", "TMP="}
	var environment []string
	for _, value := range os.Environ() {
		for _, prefix := range allowed {
			if strings.HasPrefix(value, prefix) {
				environment = append(environment, value)
				break
			}
		}
	}
	return append(environment, "LC_ALL=C", "LANG=C", "TZ=UTC")
}

type boundedBuffer struct {
	buffer  bytes.Buffer
	maximum int
	overrun bool
}

func (buffer *boundedBuffer) Write(content []byte) (int, error) {
	written := len(content)
	if buffer.maximum <= 0 {
		buffer.overrun = buffer.overrun || written > 0
		return written, nil
	}
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining > 0 {
		_, _ = buffer.buffer.Write(content[:min(remaining, len(content))])
	}
	buffer.overrun = buffer.overrun || len(content) > remaining
	return written, nil
}

func (buffer *boundedBuffer) String() string { return buffer.buffer.String() }

var _ io.Writer = (*boundedBuffer)(nil)

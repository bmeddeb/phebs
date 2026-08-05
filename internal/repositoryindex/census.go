package repositoryindex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/bmeddeb/phebs/internal/gitobj"
	"github.com/bmeddeb/phebs/internal/repopath"
	"github.com/bmeddeb/phebs/internal/store"
)

type treeRecord struct {
	mode, kind, objectID, path string
	declaredBytes              int64
}

type treeStream struct {
	ordinal     int
	commit      string
	command     *exec.Cmd
	reader      *bufio.Reader
	stderr      gitobj.StderrBuffer
	current     *treeRecord
	previous    string
	done        bool
	waited      bool
	revisionSum hash.Hash
	counts      RevisionMember
}

// BuildSourceGeneration performs one bounded, k-way streamed census over the
// exact revision set. At most eight ls-tree children and one record per child
// are live; shared path/object bytes become one owner record with many revision
// ordinals instead of being copied once per service or revision.
func BuildSourceGeneration(
	ctx context.Context,
	repositoryDir, stageDir, repository string,
	revisions []store.IndexedRevision,
) (SourceManifest, error) {
	if err := validateRevisions(revisions); err != nil {
		return SourceManifest{}, err
	}
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		return SourceManifest{}, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	streams := make([]*treeStream, 0, len(revisions))
	for ordinal, revision := range revisions {
		stream, err := startTreeStream(
			childCtx, repositoryDir, ordinal, revision,
		)
		if err != nil {
			cancel()
			_ = waitTreeStreams(ctx, streams)
			return SourceManifest{}, err
		}
		streams = append(streams, stream)
	}
	failed := true
	defer func() {
		if failed {
			cancel()
			_ = waitTreeStreams(ctx, streams)
		}
	}()
	for _, stream := range streams {
		if err := stream.advance(); err != nil {
			return SourceManifest{}, err
		}
	}

	writer := sourceWriter{
		directory: stageDir, repository: repository,
		revisions: revisions,
	}
	manifest := SourceManifest{
		Schema: SourceManifestSchema, Repository: repository,
		CensusPolicy: CensusPolicy,
		Revisions:    slices.Clone(revisions),
	}
	for {
		if err := ctx.Err(); err != nil {
			return SourceManifest{}, err
		}
		path := nextPath(streams)
		if path == "" {
			break
		}
		owners := make(map[string]SourceRecord, len(streams))
		for _, stream := range streams {
			if stream.current == nil || stream.current.path != path {
				continue
			}
			current := *stream.current
			if manifest.PlacementCount >= MaxPlacements {
				return SourceManifest{}, invalidf(
					"source placements exceed %d", MaxPlacements,
				)
			}
			manifest.PlacementCount++
			stream.counts.PlacementCount++
			switch current.kind {
			case "regular":
				stream.counts.RegularCount++
			case "symlink":
				stream.counts.SymlinkCount++
			case "gitlink":
				stream.counts.GitlinkCount++
			}
			hashPlacement(stream.revisionSum, current)
			record := SourceRecord{
				Schema: SourceMemberSchema, Path: current.path,
				Mode: current.mode, Kind: current.kind,
				ObjectID:      current.objectID,
				DeclaredBytes: current.declaredBytes,
			}
			key := recordKey(record)
			owner := owners[key]
			if owner.Schema == "" {
				owner = record
			}
			owner.Revisions = append(owner.Revisions, stream.ordinal)
			owners[key] = owner
			if err := stream.advance(); err != nil {
				return SourceManifest{}, err
			}
		}
		keys := make([]string, 0, len(owners))
		for key := range owners {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if manifest.OwnerCount >= MaxOwners {
				return SourceManifest{}, invalidf(
					"physical owners exceed %d", MaxOwners,
				)
			}
			record := owners[key]
			manifest.OwnerCount++
			switch record.Kind {
			case "regular":
				manifest.RegularOwnerCount++
				if record.DeclaredBytes > math.MaxInt64-manifest.RegularDeclaredBytes {
					return SourceManifest{}, invalidf("regular source bytes overflow")
				}
				manifest.RegularDeclaredBytes += record.DeclaredBytes
			case "symlink":
				manifest.SymlinkOwnerCount++
			case "gitlink":
				manifest.GitlinkOwnerCount++
			}
			if err := writer.add(record); err != nil {
				return SourceManifest{}, err
			}
		}
	}
	if err := waitTreeStreams(ctx, streams); err != nil {
		return SourceManifest{}, err
	}
	members, encodedBytes, err := writer.finish()
	if err != nil {
		return SourceManifest{}, err
	}
	manifest.Members = members
	manifest.EncodedMemberBytes = encodedBytes
	for ordinal, stream := range streams {
		revision := revisions[ordinal]
		stream.counts.Ordinal = ordinal
		stream.counts.Selector = revision.Selector
		stream.counts.Branch = revision.Branch
		stream.counts.Commit = revision.Commit
		stream.counts.Digest = "sha256:" +
			hex.EncodeToString(stream.revisionSum.Sum(nil))
		manifest.RevisionMembers = append(
			manifest.RevisionMembers, stream.counts,
		)
	}
	manifest.Digest, err = SourceManifestDigest(manifest)
	if err != nil {
		return SourceManifest{}, err
	}
	if err := ValidateSourceManifest(manifest); err != nil {
		return SourceManifest{}, err
	}
	if err := writeControl(
		filepath.Join(stageDir, SourceManifestName(repository)), manifest,
	); err != nil {
		return SourceManifest{}, err
	}
	if err := syncDirectory(stageDir); err != nil {
		return SourceManifest{}, err
	}
	failed = false
	return manifest, nil
}

func WriteSearchManifest(
	directory, repository string,
	revisions []store.IndexedRevision,
	source SourceManifest,
	physical PhysicalRoot,
) (SearchManifest, error) {
	if err := ValidateSourceManifest(source); err != nil {
		return SearchManifest{}, err
	}
	if source.Repository != repository ||
		!slices.Equal(source.Revisions, revisions) {
		return SearchManifest{}, invalidf("source generation identity mismatch")
	}
	manifest := SearchManifest{
		Schema: SearchManifestSchema, Repository: repository,
		Revisions:              slices.Clone(revisions),
		SourceManifestName:     SourceManifestName(repository),
		SourceGenerationDigest: source.Digest,
		TopologyPolicy:         DirectTopologyPolicy,
		PhysicalRoot:           physical,
	}
	var err error
	manifest.Digest, err = SearchManifestDigest(manifest)
	if err != nil {
		return SearchManifest{}, err
	}
	if err := ValidateSearchManifest(manifest); err != nil {
		return SearchManifest{}, err
	}
	if err := writeControl(
		filepath.Join(directory, SearchManifestName(repository)), manifest,
	); err != nil {
		return SearchManifest{}, err
	}
	return manifest, syncDirectory(directory)
}

func startTreeStream(
	ctx context.Context,
	repositoryDir string,
	ordinal int,
	revision store.IndexedRevision,
) (*treeStream, error) {
	args := []string{"ls-tree", "-r", "-l", "-z", "--full-tree", revision.Commit}
	stream := &treeStream{ordinal: ordinal, commit: revision.Commit}
	stream.command = gitobj.Command(ctx, repositoryDir, args...)
	stdout, err := stream.command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stream.command.Stderr = &stream.stderr
	if err := stream.command.Start(); err != nil {
		return nil, err
	}
	stream.reader = bufio.NewReaderSize(stdout, 64<<10)
	stream.revisionSum = sha256.New()
	_, _ = stream.revisionSum.Write(
		[]byte("phebs-repository-source-revision-v1\x00"),
	)
	return stream, nil
}

func (stream *treeStream) advance() error {
	if stream.done {
		stream.current = nil
		return nil
	}
	raw, done, err := readNULRecord(stream.reader, maxGitTreeRecordBytes)
	if err != nil {
		return invalidf("read revision %d census: %v", stream.ordinal, err)
	}
	if done {
		stream.done = true
		stream.current = nil
		return nil
	}
	record, err := parseTreeRecord(raw)
	if err != nil {
		return err
	}
	if stream.previous != "" && record.path <= stream.previous {
		return invalidf("revision %d paths are duplicated or unordered", stream.ordinal)
	}
	stream.previous = record.path
	stream.current = &record
	return nil
}

func waitTreeStreams(ctx context.Context, streams []*treeStream) error {
	var result error
	for _, stream := range streams {
		if stream.waited {
			continue
		}
		stream.waited = true
		if err := stream.command.Wait(); err != nil {
			args := []string{
				"ls-tree", "-r", "-l", "-z", "--full-tree",
				stream.commit,
			}
			result = errors.Join(result, gitobj.WrapError(
				ctx, args, err, stream.stderr.String(),
			))
		}
	}
	return result
}

func nextPath(streams []*treeStream) string {
	result := ""
	for _, stream := range streams {
		if stream.current != nil &&
			(result == "" || stream.current.path < result) {
			result = stream.current.path
		}
	}
	return result
}

func readNULRecord(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	return readDelimitedRecord(reader, 0, limit)
}

func readNewlineRecord(reader *bufio.Reader, limit int) ([]byte, bool, error) {
	return readDelimitedRecord(reader, '\n', limit)
}

func readDelimitedRecord(
	reader *bufio.Reader,
	delimiter byte,
	limit int,
) ([]byte, bool, error) {
	result := make([]byte, 0, 256)
	for {
		fragment, err := reader.ReadSlice(delimiter)
		if len(result)+len(fragment) > limit+1 {
			return nil, false, invalidf("delimited record exceeds %d bytes", limit)
		}
		result = append(result, fragment...)
		switch {
		case err == nil:
			return result[:len(result)-1], false, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(result) == 0:
			return nil, true, nil
		case errors.Is(err, io.EOF):
			return nil, false, invalidf("input ended inside a delimited record")
		default:
			return nil, false, err
		}
	}
}

func parseTreeRecord(raw []byte) (treeRecord, error) {
	header, rawPath, found := bytes.Cut(raw, []byte{'\t'})
	fields := bytes.Fields(header)
	if !found || len(rawPath) == 0 || len(fields) != 4 {
		return treeRecord{}, invalidf("Git tree emitted a malformed record")
	}
	mode, objectType, objectID :=
		string(fields[0]), string(fields[1]), string(fields[2])
	if !gitobj.IsObjectID(objectID) {
		return treeRecord{}, invalidf("Git tree emitted a malformed object ID")
	}
	filePath := string(rawPath)
	if err := repopath.Validate(filePath); err != nil {
		return treeRecord{}, invalidf("Git tree emitted a non-canonical path")
	}
	record := treeRecord{
		mode: mode, objectID: objectID, path: filePath,
	}
	switch {
	case objectType == "blob" && (mode == "100644" || mode == "100755"):
		record.kind = "regular"
	case objectType == "blob" && mode == "120000":
		record.kind = "symlink"
	case objectType == "commit" && mode == "160000":
		record.kind = "gitlink"
	default:
		return treeRecord{}, invalidf(
			"unsupported Git tree mode/type %s/%s", mode, objectType,
		)
	}
	if string(fields[3]) != "-" {
		declared, err := strconv.ParseInt(string(fields[3]), 10, 64)
		if err != nil || declared < 0 {
			return treeRecord{}, invalidf("Git tree emitted an invalid size")
		}
		record.declaredBytes = declared
	}
	return record, nil
}

type sourceWriter struct {
	directory, repository string
	revisions             []store.IndexedRevision
	members               []SourceMember
	file                  *os.File
	hash                  hash.Hash
	recordCount           int
	contentBytes          int64
	firstKey, lastKey     string
	totalBytes            int64
}

func (writer *sourceWriter) add(record SourceRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(raw)+1 > maxSourceRecordBytes {
		return invalidf("source record exceeds %d bytes", maxSourceRecordBytes)
	}
	if writer.file == nil || writer.recordCount >= MaxRecordsPerMember ||
		writer.contentBytes+int64(len(raw)+1) > MaxMemberBytes {
		if err := writer.closeMember(); err != nil {
			return err
		}
		if len(writer.members) >= MaxSourceMembers {
			return invalidf("source member count exceeds %d", MaxSourceMembers)
		}
		name, err := sourceMemberName(
			writer.repository, writer.revisions, len(writer.members),
		)
		if err != nil {
			return err
		}
		writer.file, err = os.OpenFile(
			filepath.Join(writer.directory, name),
			os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600,
		)
		if err != nil {
			return err
		}
		writer.hash = sha256.New()
		writer.members = append(writer.members, SourceMember{
			Ordinal: len(writer.members), Name: name,
		})
	}
	line := append(raw, '\n')
	if writer.totalBytes > MaxGenerationBytes-int64(len(line)) {
		return invalidf("source generation exceeds %d bytes", MaxGenerationBytes)
	}
	if _, err := writer.file.Write(line); err != nil {
		return err
	}
	_, _ = writer.hash.Write(line)
	key := recordKey(record)
	if writer.recordCount == 0 {
		writer.firstKey = key
	}
	writer.lastKey = key
	writer.recordCount++
	writer.contentBytes += int64(len(line))
	writer.totalBytes += int64(len(line))
	return nil
}

func (writer *sourceWriter) closeMember() error {
	if writer.file == nil {
		return nil
	}
	if err := writer.file.Sync(); err != nil {
		_ = writer.file.Close()
		return err
	}
	if err := writer.file.Close(); err != nil {
		return err
	}
	member := &writer.members[len(writer.members)-1]
	member.RecordCount = writer.recordCount
	member.ContentBytes = writer.contentBytes
	member.Digest = "sha256:" + hex.EncodeToString(writer.hash.Sum(nil))
	member.FirstKey, member.LastKey = writer.firstKey, writer.lastKey
	writer.file = nil
	writer.hash = nil
	writer.recordCount = 0
	writer.contentBytes = 0
	writer.firstKey, writer.lastKey = "", ""
	return nil
}

func (writer *sourceWriter) finish() ([]SourceMember, int64, error) {
	if err := writer.closeMember(); err != nil {
		return nil, 0, err
	}
	if len(writer.members) == 0 {
		return []SourceMember{}, 0, nil
	}
	for index := range writer.members {
		writer.members[index].Count = len(writer.members)
	}
	return slices.Clone(writer.members), writer.totalBytes, nil
}

func revisionSetDigest(
	repository string,
	revisions []store.IndexedRevision,
) (string, error) {
	raw, err := json.Marshal(struct {
		Policy     string                  `json:"policy"`
		Repository string                  `json:"repository"`
		Revisions  []store.IndexedRevision `json:"revisions"`
	}{CensusPolicy, repository, revisions})
	if err != nil {
		return "", err
	}
	return digest("phebs-repository-source-revision-set-v1\x00", raw), nil
}

func hashPlacement(destination hash.Hash, record treeRecord) {
	for _, value := range []string{
		record.path, record.mode, record.kind, record.objectID,
		strconv.FormatInt(record.declaredBytes, 10),
	} {
		_, _ = io.WriteString(destination, strconv.Itoa(len(value)))
		_, _ = destination.Write([]byte{':'})
		_, _ = io.WriteString(destination, value)
	}
	_, _ = destination.Write([]byte{0})
}

func syncDirectory(directory string) error {
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	err = file.Sync()
	return errors.Join(err, file.Close())
}

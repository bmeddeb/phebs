package t421

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bmeddeb/phebs/spike/t401"
)

const (
	maxCorpusAuthorRecords = 2_031_604
	maxCorpusAuthorPath    = 4096
	maxCorpusAuthorBlob    = 256 << 20
	maxCorpusAuthorBlobs   = 2 << 30
)

// ErrExecutionCorpusAuthor contains no repository location, source or Git output.
var ErrExecutionCorpusAuthor = errors.New("execution corpus author unavailable or inconsistent")

// The only production recipe comes from the validated frozen plan and existing
// generators. Function fields are private streaming seams for tiny real-Git
// tests, not caller-provided source, identities or admission assertions.
type corpusAuthorSource struct {
	recipe      SourceRecipe
	profile     CombinedProfile
	revisions   []PhysicalRevision
	walkRecords func(context.Context, string, func(sourceTreeRecord) error) error
	walkBlobs   func(context.Context, string, func(string, []byte) error) error
}

func newCorpusAuthorSource(ctx context.Context, plan Plan) (*corpusAuthorSource, error) {
	if ctx == nil || ctx.Err() != nil || plan.Schema != PlanV3Schema ||
		plan.Profile.Physical.CombinedRegularFiles != maxCorpusAuthorRecords || len(plan.Revisions.Physical) != 3 {
		return nil, ErrExecutionCorpusAuthor
	}
	structural, err := frozenStructuralProfile()
	if err != nil {
		return nil, ErrExecutionCorpusAuthor
	}
	// Retain only the small addition descriptors, not the structural census or
	// addition contents. The cold writer regenerates addition bytes once.
	additions := make([]sourceTreeRecord, 0, 31_602)
	err = WalkCombinedAdditions(func(path string, content []byte) error {
		if ctx.Err() != nil || len(additions) >= 31_602 || !corpusAuthorPath(path) || len(content) > maxCorpusAuthorBlob {
			return ErrExecutionCorpusAuthor
		}
		if len(additions) != 0 && path <= additions[len(additions)-1].Path {
			return ErrExecutionCorpusAuthor
		}
		additions = append(additions, sourceTreeRecord{Path: path, Bytes: uint64(len(content)), BlobOID: gitSHA1ObjectID("blob", content)})
		return nil
	})
	if err != nil || len(additions) != 31_602 {
		return nil, ErrExecutionCorpusAuthor
	}
	source := &corpusAuthorSource{recipe: plan.Revisions.SourceRecipe, profile: plan.Profile, revisions: plan.Revisions.Physical}
	source.walkRecords = func(ctx context.Context, revision string, visit func(sourceTreeRecord) error) error {
		addition, ordinal := 0, uint64(0)
		err := t401.WalkFrozenTreeRecords(structural, revision, func(value t401.FrozenTreeRecord) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			record := sourceTreeRecord{Path: value.Path, Bytes: value.Bytes, BlobOID: value.BlobOID}
			if strings.HasSuffix(record.Path, ".go") {
				if ordinal != 0 {
					record.Path = strings.TrimSuffix(record.Path, ".go") + ".txt"
				}
				ordinal++
			}
			for addition < len(additions) && additions[addition].Path < record.Path {
				if err := visit(additions[addition]); err != nil {
					return err
				}
				addition++
			}
			if addition < len(additions) && additions[addition].Path == record.Path {
				return ErrExecutionCorpusAuthor
			}
			return visit(record)
		})
		if err != nil || ordinal != structural.Aggregate.EligibleGoFiles {
			return ErrExecutionCorpusAuthor
		}
		for ; addition < len(additions); addition++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := visit(additions[addition]); err != nil {
				return err
			}
		}
		return nil
	}
	source.walkBlobs = func(ctx context.Context, revision string, visit func(string, []byte) error) error {
		checked := func(path string, content []byte) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return visit(path, content)
		}
		if err := t401.WalkFrozenStructuralRevisionBlobs(structural, revision, checked); err != nil {
			return err
		}
		if revision == "a" {
			return WalkCombinedAdditions(checked)
		}
		return nil
	}
	return source, nil
}

func corpusAuthorPath(path string) bool {
	if path == "" || len(path) > maxCorpusAuthorPath || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, value := range path {
		if value < 0x21 || value > 0x7e || value == '\\' || value == '"' {
			return false
		}
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func (source *corpusAuthorSource) writeRevision(ctx context.Context, writer *bufio.Writer, index int, parent string) error {
	if ctx == nil || ctx.Err() != nil || index < 0 || index >= len(source.revisions) ||
		index == 0 && parent != "" || index > 0 && parent != source.revisions[index-1].ExpectedCommit {
		return ErrExecutionCorpusAuthor
	}
	revision := source.revisions[index]
	var total uint64
	var changed sourceTreeRecord
	blobs := 0
	if err := source.walkBlobs(ctx, revision.Name, func(path string, content []byte) error {
		if ctx.Err() != nil || !corpusAuthorPath(path) || len(content) > maxCorpusAuthorBlob ||
			uint64(len(content)) > maxCorpusAuthorBlobs-total || blobs >= 32_116 || index > 0 && blobs != 0 {
			return ErrExecutionCorpusAuthor
		}
		blobs++
		total += uint64(len(content))
		changed = sourceTreeRecord{Path: path, Bytes: uint64(len(content)), BlobOID: gitSHA1ObjectID("blob", content)}
		if _, err := fmt.Fprintf(writer, "blob\ndata %d\n", len(content)); err != nil {
			return err
		}
		if _, err := writer.Write(content); err != nil {
			return err
		}
		return writer.WriteByte('\n')
	}); err != nil || blobs == 0 {
		return ErrExecutionCorpusAuthor
	}
	recipe, message := source.recipe, revision.CommitMessage+"\n"
	if _, err := fmt.Fprintf(writer,
		"commit refs/heads/main\nauthor %s <%s> %d %s\ncommitter %s <%s> %d %s\ndata %d\n%s",
		recipe.AuthorName, recipe.AuthorEmail, recipe.Timestamp, recipe.Timezone,
		recipe.CommitterName, recipe.CommitterEmail, recipe.Timestamp, recipe.Timezone, len(message), message); err != nil {
		return err
	}
	if index > 0 {
		if _, err := fmt.Fprintf(writer, "from %s\n", parent); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(writer, "M 100644 %s %s\n", changed.BlobOID, changed.Path); err != nil {
			return err
		}
	} else {
		if _, err := writer.WriteString("deleteall\n"); err != nil {
			return err
		}
		var count uint64
		last := ""
		if err := source.walkRecords(ctx, revision.Name, func(record sourceTreeRecord) error {
			if ctx.Err() != nil || count >= source.profile.Physical.CombinedRegularFiles || count >= maxCorpusAuthorRecords ||
				!corpusAuthorPath(record.Path) || record.Path <= last || !validGitObjectID(record.BlobOID, "sha1") {
				return ErrExecutionCorpusAuthor
			}
			count++
			last = record.Path
			_, err := fmt.Fprintf(writer, "M 100644 %s %s\n", record.BlobOID, record.Path)
			return err
		}); err != nil || count != source.profile.Physical.CombinedRegularFiles {
			return ErrExecutionCorpusAuthor
		}
	}
	_, err := writer.WriteString("\ndone\n")
	return err
}

// verifyInventory consumes one exact ls-tree record at a time. Git does not
// report blob sizes in this frozen command; size is independently bound by the
// expected blob OID/bytes recipe, never inferred from a file on the host.
func (source *corpusAuthorSource) verifyInventory(ctx context.Context, input io.Reader, index int) (sourceTreeIdentity, error) {
	if ctx == nil || ctx.Err() != nil || index < 0 || index >= len(source.revisions) {
		return sourceTreeIdentity{}, ErrExecutionCorpusAuthor
	}
	reader := bufio.NewReaderSize(input, maxCorpusAuthorPath+54)
	accumulator := newTreeInventoryAccumulator(nil)
	accumulator.goOnly = true
	var count uint64
	err := source.walkRecords(ctx, source.revisions[index].Name, func(expected sourceTreeRecord) error {
		if ctx.Err() != nil || count >= maxCorpusAuthorRecords || count >= source.profile.Physical.CombinedRegularFiles ||
			!corpusAuthorPath(expected.Path) || expected.Bytes > maxCorpusAuthorBlob {
			return ErrExecutionCorpusAuthor
		}
		record, err := reader.ReadSlice(0)
		if err != nil || string(record) != "100644 blob "+expected.BlobOID+"\t"+expected.Path+"\x00" {
			return ErrExecutionCorpusAuthor
		}
		count++
		return accumulator.add(expected)
	})
	if err != nil || count != source.profile.Physical.CombinedRegularFiles || ctx.Err() != nil {
		return sourceTreeIdentity{}, ErrExecutionCorpusAuthor
	}
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		return sourceTreeIdentity{}, ErrExecutionCorpusAuthor
	}
	identity, err := accumulator.finish()
	physical := source.revisions[index]
	if err != nil || identity.TreeOID != physical.ExpectedTree || identity.Inventory != physical.ExpectedTreeInventory {
		return sourceTreeIdentity{}, ErrExecutionCorpusAuthor
	}
	return identity, nil
}

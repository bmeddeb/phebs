//go:build linux

package typedexecutor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmeddeb/phebs/internal/codenav"
	"github.com/bmeddeb/phebs/internal/store"
	"github.com/bmeddeb/phebs/internal/typedindex"
	"github.com/bmeddeb/phebs/internal/typedworkspace"
	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"
)

// Every query/open/final check re-reads real exact current authority. There is
// no mutable binding map, imported source authority or Git fallback here.
type acceptanceCorpusResolver struct {
	store *store.Surreal
	base  string
	conf  nativeAcceptanceConfig
}

func (r *acceptanceCorpusResolver) current(ctx context.Context) (typedworkspace.RoutedAuthority, codenav.RoutedBinding, error) {
	c, err := r.store.ReadTypedIndexCurrentCustody(ctx, r.conf.Source.Repository)
	if err != nil {
		return typedworkspace.RoutedAuthority{}, codenav.RoutedBinding{}, err
	}
	id := typedworkspace.OwnerIdentity{PlanningDigest: c.PlanningDigest, AttemptDigest: c.AttemptDigest, ChunkIdentity: c.ChunkIdentity, LeaseDigest: c.LeaseDigest, Request: c.Parent.Request()}
	a := typedworkspace.RoutedAuthority{Owner: id, ManifestDigest: c.Custody.ManifestDigest, DirectoryDevice: c.Custody.DirectoryDevice, DirectoryInode: c.Custody.DirectoryInode, PublicationReceiptDigest: c.Custody.PublicationReceiptDigest, Parent: c.Parent, Execution: c.Admission, RootDigest: c.Pointer.RootDigest}
	if a.Identity() == "" || c.Parent.Request().Source != r.conf.Source || c.Pointer.Epoch == 0 || c.Custody.Revision != 3 || c.Custody.PublicationRootDigest != c.Pointer.RootDigest {
		return typedworkspace.RoutedAuthority{}, codenav.RoutedBinding{}, codenav.ErrTypedIndexBinding
	}
	identity := acceptanceDigest(acceptanceJSON(struct {
		Custody string
		Epoch   uint64
	}{a.Identity(), c.Pointer.Epoch}))
	return a, codenav.RoutedBinding{Selected: true, Identity: identity, RootDigest: c.Pointer.RootDigest, Source: r.conf.Source}, nil
}
func (r *acceptanceCorpusResolver) ResolveRoutedIndex(ctx context.Context, repo, commit string) (codenav.RoutedBinding, error) {
	if repo != r.conf.Source.Repository || commit != r.conf.Source.Commit {
		return codenav.RoutedBinding{}, codenav.ErrTypedIndexBinding
	}
	_, b, err := r.current(ctx)
	return b, err
}
func (r *acceptanceCorpusResolver) OpenRoutedIndex(ctx context.Context, b codenav.RoutedBinding, cached codenav.RoutedMetadata) (codenav.RoutedReader, codenav.RoutedMetadata, error) {
	a, actual, err := r.current(ctx)
	if err != nil {
		return nil, nil, err
	}
	if actual != b {
		return nil, nil, codenav.ErrBindingChanged
	}
	var m *typedworkspace.RoutedMetadata
	if cached != nil {
		var ok bool
		m, ok = cached.(*typedworkspace.RoutedMetadata)
		if !ok {
			return nil, nil, codenav.ErrTypedIndexBinding
		}
	}
	p, err := typedworkspace.OpenRoutedPublication(ctx, r.base, a, m)
	if err != nil {
		return nil, nil, err
	}
	return p, p.Metadata(), nil
}

// Read-only original Git objects supply real product range conversion. The
// source tree must equal every frozen archive file, including executable modes.
// This private test mirror is removed after proof; it never enters worker input.
func acceptanceCorpusPrepareGit(ctx context.Context, c nativeAcceptanceConfig) (data string, err error) {
	raw, err := acceptanceRead(filepath.Join(c.root(), "source-git.tar"), acceptanceCorpusGitMax)
	if err != nil || acceptanceDigest(raw) != c.SourceGitSHA256 {
		return "", errors.New("source Git input identity")
	}
	files, err := acceptanceCorpusGitFiles(raw)
	if err != nil {
		return "", err
	}
	data = filepath.Join(c.caseRoot(*acceptanceCase), "source-data")
	if err = os.Mkdir(data, 0700); err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, os.RemoveAll(data))
		}
	}()
	repo := filepath.Join(data, "repos", c.Source.Repository+".git")
	if err = os.MkdirAll(filepath.Join(repo, "objects/pack"), 0700); err != nil {
		return data, err
	}
	if err = os.Mkdir(filepath.Join(repo, "refs"), 0700); err != nil {
		return data, err
	}
	for name, body := range files {
		f, e := os.OpenFile(filepath.Join(repo, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
		if e != nil {
			return data, e
		}
		_, e = f.Write(body)
		if e = errors.Join(e, f.Close()); e != nil {
			return data, e
		}
	}
	archive, err := acceptanceRead(filepath.Join(c.root(), "bundle/tools/corpus/remote-apis-sdks.tar.gz"), 249496)
	if err != nil {
		return data, err
	}
	expected, err := acceptanceCorpusFiles(ctx, archive)
	if err != nil {
		return data, err
	}
	if err = acceptanceCorpusVerifyGit(ctx, repo, expected); err != nil {
		return data, err
	}
	return data, nil
}

func acceptanceCorpusPublication(ctx context.Context, c nativeAcceptanceConfig, s *store.Surreal, p *typedworkspace.Publication, m typedindex.AttemptManifest) (_ *acceptanceCorpusResult, err error) {
	if !m.Complete || len(m.Members) == 0 || len(m.Generated) != 0 {
		return nil, errors.New("corpus publication incomplete or widened")
	}
	r := &acceptanceCorpusResult{Cohort: c.Cohort, Commit: c.Source.Commit, ArchiveSHA256: acceptanceCorpusArchive, OracleSHA256: acceptanceCorpusOracle, RootDigest: acceptanceDigest(acceptanceJSON(p.Root())), PlanDigest: p.Root().Binding.PlanDigest, MemberDigests: []string{}, Symbols: []acceptanceCorpusSymbolResult{}, CrossCohort: "pending", CostGate: "unavailable", CostMissing: []string{"sampled_child_lifetimes", "sampled_fd_counts", "private_cache_inventory"}}
	documents := map[string]*scip.Document{}
	for _, member := range m.Members {
		b, e := p.ReadMember(ctx, member.Name)
		if e != nil {
			return nil, e
		}
		var index scip.Index
		if proto.Unmarshal(b, &index) != nil {
			return nil, errors.New("corpus member SCIP")
		}
		r.MemberDigests = append(r.MemberDigests, acceptanceDigest(b))
		for _, d := range index.Documents {
			if d == nil || documents[d.RelativePath] != nil || !strings.HasPrefix(d.RelativePath, "go/") {
				return nil, errors.New("corpus member document")
			}
			documents[d.RelativePath] = d
			r.Documents++
			r.Occurrences += len(d.Occurrences)
			for _, occurrence := range d.Occurrences {
				if occurrence != nil && occurrence.Symbol != "" && occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0 {
					r.Definitions++
				}
			}
		}
	}
	slices.Sort(r.MemberDigests)
	want := map[string][3]int{"ordinary": {3, 535, 148}, "proto": {1, 1309, 241}, "fanout": {13, 13278, 2350}}[c.Cohort]
	if [3]int{r.Documents, r.Occurrences, r.Definitions} != want {
		return nil, errors.New("corpus retained counts differ from frozen gate")
	}
	data, err := acceptanceCorpusPrepareGit(ctx, c)
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanup := os.RemoveAll(data)
		r.SourceGitDrained = cleanup == nil
		err = errors.Join(err, cleanup)
	}()
	resolver := &acceptanceCorpusResolver{s, filepath.Join(c.caseRoot(*acceptanceCase), "workspace"), c}
	service := codenav.New(codenav.Options{DataDir: data, RoutedResolver: resolver})
	defer func() { err = errors.Join(err, service.Remove(c.Source.Repository)) }()
	for _, symbol := range acceptanceCorpusSymbols {
		points := symbol.References
		if c.Cohort == symbol.Definition.Cohort {
			points = []acceptanceOraclePoint{symbol.Definition}
		} else if c.Cohort != "fanout" {
			continue
		}
		proof := acceptanceCorpusSymbolResult{Name: symbol.Name}
		for _, point := range points {
			rawSymbol := acceptanceCorpusPointSymbol(documents[point.Path], point, c.Cohort != "fanout")
			if rawSymbol == "" || proof.SymbolSHA256 != "" && proof.SymbolSHA256 != acceptanceDigest([]byte(rawSymbol)) {
				return nil, errors.New("corpus frozen source point symbol")
			}
			proof.SymbolSHA256 = acceptanceDigest([]byte(rawSymbol))
			query := codenav.Query{Repo: c.Source.Repository, Revision: c.Source.Commit, Path: point.Path, Line: point.Range[0], Character: point.Range[1], Encoding: codenav.EncodingUTF8}
			definition, e := service.Definition(ctx, query)
			if e != nil || !definition.Available || definition.Symbol != rawSymbol {
				return nil, errors.Join(e, errors.New("corpus Definition oracle"))
			}
			hover, e := service.Hover(ctx, query)
			if e != nil || !hover.Available {
				return nil, errors.Join(e, errors.New("corpus Hover availability"))
			}
			refs, e := service.References(ctx, query)
			if e != nil || !refs.Available || refs.Symbol != rawSymbol || refs.Truncated {
				return nil, errors.Join(e, errors.New("corpus References oracle"))
			}
			if c.Cohort == "fanout" {
				if definition.Location != nil || hover.Hover != nil || !acceptanceCorpusReferences(c, symbol.References, refs.Locations) {
					return nil, errors.New("corpus scoped reference set or explicit cross-cohort gap")
				}
				proof.ReferencePoints++
			} else {
				if !acceptanceCorpusLocation(c, symbol.Definition, definition.Location) || hover.Hover == nil || hover.Hover.Symbol != rawSymbol || hover.Hover.Range != acceptanceCorpusRange(point) || hover.Hover.Encoding != codenav.EncodingUTF8 || hover.Hover.Signature != symbol.Signature || !strings.Contains(strings.Join(strings.Fields(strings.Join(hover.Hover.Documentation, " ")), " "), symbol.Documentation) {
					return nil, errors.New("corpus definition range/signature/documentation")
				}
				proof.DefinitionLocations++
				proof.HoverPayloads++
			}
			proof.QueryPoints++
		}
		r.Symbols = append(r.Symbols, proof)
	}
	r.NavigationVerified = true
	return r, nil
}

func acceptanceCorpusPointSymbol(d *scip.Document, p acceptanceOraclePoint, definition bool) string {
	if d == nil {
		return ""
	}
	match := ""
	for _, occurrence := range d.Occurrences {
		if occurrence == nil || occurrence.Symbol == "" || (occurrence.SymbolRoles&int32(scip.SymbolRole_Definition) != 0) != definition {
			continue
		}
		span, ok := occurrence.SourceRange()
		if ok && span.Start.Line == p.Range[0] && span.Start.Character == p.Range[1] && span.End.Line == p.Range[0] && span.End.Character == p.Range[2] {
			if match != "" {
				return ""
			}
			match = occurrence.Symbol
		}
	}
	return match
}
func acceptanceCorpusReferences(c nativeAcceptanceConfig, expected []acceptanceOraclePoint, actual []codenav.Location) bool {
	points := map[acceptanceOraclePoint]bool{}
	for _, location := range actual {
		if location.Path != "go/pkg/rexec/rexec.go" {
			continue
		}
		matched := false
		for _, point := range expected {
			if acceptanceCorpusLocation(c, point, &location) && !points[point] {
				points[point], matched = true, true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return len(points) == len(expected)
}

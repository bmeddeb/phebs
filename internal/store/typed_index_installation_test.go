package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/typedindex"
)

func TestTypedIndexInstallExpectedSource(t *testing.T) {
	s := newRunnerStore(t)
	f := newTypedFixture(t, s, "installed-source")
	source, err := s.GetTypedSource(t.Context(), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetTypedIndexIntent(t.Context(), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := typedindex.DecodeProfile(t.Context(), []byte(before.ProfileJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetRepoIndexed(t.Context(), f.repo, strings.Repeat("b", 40), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstallTypedProfileExpectedSource(t.Context(), source, profile, before.UniverseDigest, before.ProfileEpoch); !errors.Is(err, typedindex.Stale) {
		t.Fatal("old source installed", err)
	}
	after, err := s.GetTypedIndexIntent(t.Context(), f.repo)
	if err != nil || after != before {
		t.Fatal("stale install mutated intent", after, err)
	}
	source, err = s.GetTypedSource(t.Context(), f.repo)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.InstallTypedProfileExpectedSource(t.Context(), source, profile, before.UniverseDigest, before.ProfileEpoch)
	if err != nil || next.ProfileEpoch != before.ProfileEpoch+1 {
		t.Fatal("current source refused", next, err)
	}
}

package store

import (
	"strings"
	"testing"
)

// T19.8: the store validates boundary shape and consistency; it cannot
// recompute the Git tree, so recalculation authority stays with the walker.
func TestValidateGitlinkBoundaries(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	valid := CoverageManifest{
		InventoryPolicy: "gitlink-boundary-v1", GitlinkCount: 3,
		GitlinkDigest: digest, GitlinkSamplePaths: []string{"a/mod", "b/mod"},
		GitlinkSampleTruncated: true,
	}
	cases := []struct {
		name    string
		mutate  func(*CoverageManifest)
		wantErr string
	}{
		{"valid", func(*CoverageManifest) {}, ""},
		{"legacy empty is valid", func(m *CoverageManifest) {
			*m = CoverageManifest{}
		}, ""},
		{"zero boundaries with policy is valid", func(m *CoverageManifest) {
			m.GitlinkCount = 0
			m.GitlinkSamplePaths = nil
			m.GitlinkSampleTruncated = false
		}, ""},
		{"fields without policy", func(m *CoverageManifest) {
			m.InventoryPolicy = ""
		}, "require an inventory policy"},
		{"legacy with stray truncation flag", func(m *CoverageManifest) {
			*m = CoverageManifest{GitlinkSampleTruncated: true}
		}, "require an inventory policy"},
		{"unbounded policy token", func(m *CoverageManifest) {
			m.InventoryPolicy = strings.Repeat("p", maxInventoryPolicyLen+1)
		}, "bounded token"},
		{"negative count", func(m *CoverageManifest) {
			m.GitlinkCount = -1
		}, "negative or unbounded"},
		{"non-canonical digest", func(m *CoverageManifest) {
			m.GitlinkDigest = "sha256:XYZ"
		}, "canonical sha256"},
		{"missing digest with policy", func(m *CoverageManifest) {
			m.GitlinkDigest = ""
		}, "canonical sha256"},
		{"sample larger than count", func(m *CoverageManifest) {
			m.GitlinkCount = 1
		}, "exceeds its bounds or its count"},
		{"unsorted sample", func(m *CoverageManifest) {
			m.GitlinkSamplePaths = []string{"b/mod", "a/mod"}
		}, "sorted and unique"},
		{"duplicate sample", func(m *CoverageManifest) {
			m.GitlinkSamplePaths = []string{"a/mod", "a/mod"}
		}, "sorted and unique"},
		{"empty sample path", func(m *CoverageManifest) {
			m.GitlinkSamplePaths = []string{""}
		}, "valid bounded UTF-8"},
		{"sample byte bound", func(m *CoverageManifest) {
			m.GitlinkCount = 4096
			m.GitlinkSamplePaths = nil
			for i := 0; i < 2; i++ {
				m.GitlinkSamplePaths = append(m.GitlinkSamplePaths,
					string(rune('a'+i))+strings.Repeat("x", maxGitlinkSampleBytes/2))
			}
		}, "byte bound"},
		{"truncated without excess count", func(m *CoverageManifest) {
			m.GitlinkCount = 2
			m.GitlinkSampleTruncated = true
		}, "truncation is inconsistent"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := valid
			manifest.GitlinkSamplePaths = append([]string(nil), valid.GitlinkSamplePaths...)
			testCase.mutate(&manifest)
			err := validateGitlinkBoundaries(manifest)
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("error = %v, want %q", err, testCase.wantErr)
			}
		})
	}
}

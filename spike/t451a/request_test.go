package t451a

import "testing"

func TestRequestClosesExecutionInputs(t *testing.T) {
	base := Request{Schema: "phebs-t451a-request-v1", Profile: Profile, Mode: "plan", BundleSHA256: Digest(nil), HelperSHA256: Digest(nil), PlannerSHA256: Digest(nil), LauncherSHA256: Digest(nil), ImageID: Digest(nil)}
	for _, tc := range []struct {
		name   string
		change func(*Request)
		pass   bool
	}{
		{"neutral plan", func(*Request) {}, true},
		{"closed probe", func(r *Request) { r.Mode, r.Probe = "probe", "access" }, true},
		{"target execution", func(r *Request) { r.Mode = "target" }, false},
		{"alternate profile", func(r *Request) { r.Profile = "target-linux-v1" }, false},
		{"command probe", func(r *Request) { r.Mode, r.Probe = "probe", "sh -c true" }, false},
		{"missing pin", func(r *Request) { r.BundleSHA256 = "" }, false},
		{"mutable image tag", func(r *Request) { r.ImageID = "debian:bookworm-slim" }, false},
		{"repo launcher", func(r *Request) { r.LauncherSHA256 = Digest([]byte("other")) }, false},
		{"different planner", func(r *Request) { r.PlannerSHA256 = Digest([]byte("other")) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.change(&r)
			_, err := DecodeRequest(wire(t, r))
			if (err == nil) != tc.pass {
				t.Fatalf("admission mismatch: %v", err)
			}
		})
	}
}

func TestToolLayoutRequiresEveryExecutableLane(t *testing.T) {
	files := []BundleFile{
		{Path: "tools/bin/bazel", Executable: true},
		{Path: "tools/go/bin/go", Executable: true},
		{Path: "tools/bin/gopackagesdriver", Executable: true},
		{Path: "tools/cc-sysroot.zip", Bytes: 1},
	}
	if err := validateToolLayout(Bundle{Files: files}); err != nil {
		t.Fatal(err)
	}
	for missing := range files {
		subset := append([]BundleFile{}, files[:missing]...)
		subset = append(subset, files[missing+1:]...)
		if err := validateToolLayout(Bundle{Files: subset}); err == nil {
			t.Fatalf("missing %s admitted", files[missing].Path)
		}
	}
}

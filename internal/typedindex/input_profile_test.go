package typedindex

import (
	"errors"
	"testing"
)

func TestInputProfileAuthorityAndIsolation(t *testing.T) {
	for _, provider := range []string{ModuleProviderID, ImportProviderID} {
		t.Run(provider, func(t *testing.T) {
			base, _, _, _ := fixture(t)
			d := base.Definition()
			d.Schema, d.Provider = InputProfileSchema, provider
			d.Config, _ = InputConfig(provider)
			helper := Tool{Version: "worker", Digest: hash([]byte("worker"))}
			d.Tools = Tools{Planner: helper, Launcher: helper, Indexer: Tool{Version: "0.2.7", Digest: SCIPGoIndexerDigest}}
			if provider == ModuleProviderID {
				d.Tools.Go = Tool{Version: "1.25.0", Digest: hash([]byte("SDK"))}
			}
			p, err := DecodeProfile(t.Context(), wire(t, d))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.Commands(); !errors.Is(err, Unsupported) {
				t.Fatal("input profile obtained Bazel recipe", err)
			}
			_, a, _, _ := fixture(t)
			a.Profile = Epoch{Number: 1, Digest: p.Digest()}
			r := NewManagedRequest(a.Source, p, 1, a.UniverseDigest, Publish)
			if _, err = Admit(t.Context(), a, p, wire(t, r)); err != nil {
				t.Fatal(err)
			}
			r.Provider = ProviderID
			if _, err = Admit(t.Context(), a, p, wire(t, r)); err == nil {
				t.Fatal("provider substitution admitted")
			}
			for _, mode := range []string{"old-schema", "foreign-provider", "bazel-tools", "rc", "resource", "helper", "generated", "go-tool"} {
				t.Run(mode, func(t *testing.T) {
					bad := d
					switch mode {
					case "old-schema":
						bad.Schema = ProfileSchema
					case "foreign-provider":
						bad.Provider = "shell"
					case "bazel-tools":
						bad.Tools.Bazel = helper
					case "rc":
						bad.RCDigest = hash([]byte(ResolvedRC))
					case "resource":
						bad.Policy.WallSeconds++
					case "helper":
						bad.Tools.Planner.Digest = hash([]byte("other"))
					case "generated":
						bad.Config.GeneratedDocuments = "sealed"
					case "go-tool":
						if provider == ImportProviderID {
							bad.Tools.Go = helper
						} else {
							bad.Tools.Go.Version = "1.26.0"
						}
					}
					if _, err := DecodeProfile(t.Context(), wire(t, bad)); err == nil {
						t.Fatal("changed profile admitted", mode)
					}
				})
			}
		})
	}
}

func TestSelectionFileClosed(t *testing.T) {
	for _, provider := range ProviderOrder() {
		if name, err := SelectionFile(provider); err != nil || name == "" {
			t.Fatal(provider, name, err)
		}
	}
	for _, provider := range []string{"", "shell", ModuleProviderID + " ", "../../source"} {
		if _, err := SelectionFile(provider); !errors.Is(err, Unsupported) {
			t.Fatal(provider, err)
		}
	}
}

package typedsandbox

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectRecorded(t *testing.T) {
	for _, fault := range []string{"good", "pending", "missing", "orphan pending", "pending mismatch", "daemon", "daemon changed", "journal changed", "recipe", "stopped", "paused", "restarting", "dead", "oom", "pid", "absent", "canceled"} {
		t.Run(fault, func(t *testing.T) {
			_, o := fakeDaemon(t, "")
			o.Socket = filepath.Join(filepath.Dir(o.Socket), "inspection.sock")
			owner := journal{Schema: ownerSchema, Name: "phebs-typed-index-" + strings.Repeat("a", 32), ContainerID: testContainer, DaemonID: "owned-daemon", ImageID: o.ImageID, Socket: o.Socket, Inputs: o.Inputs, Controls: o.Controls, Control: o.Control, Allowance: o.Allowance, Scratch: o.scratch}
			put := func(name string, value journal) {
				t.Helper()
				raw, _ := json.Marshal(value)
				if err := os.WriteFile(name, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			main := owner
			if fault == "pending" {
				main.ContainerID = ""
			}
			if fault != "missing" && fault != "orphan pending" {
				put(journalPath(o), main)
			}
			if fault == "pending" || fault == "orphan pending" || fault == "pending mismatch" {
				pending := owner
				if fault == "pending mismatch" {
					pending.Name = "phebs-typed-index-" + strings.Repeat("b", 32)
				}
				put(journalPath(o)+".next", pending)
			}
			infoCalls, requests := 0, 0
			listener, err := net.Listen("unix", o.Socket)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "GET" {
					t.Errorf("mutation: %s", r.Method)
					w.WriteHeader(500)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/info") {
					infoCalls++
					id := owner.DaemonID
					if fault == "daemon" || fault == "daemon changed" && infoCalls == 2 {
						id = "other"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"ID": id})
					return
				}
				if r.URL.Path != apiVersion+"/containers/"+testContainer+"/json" {
					t.Errorf("unexpected GET: %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				if fault == "absent" {
					w.WriteHeader(404)
					return
				}
				recipe := recipe(o, owner.Name)
				got := inspection{ID: testContainer, Image: o.ImageID, Name: "/" + owner.Name, Config: wantWithoutHost(recipe), HostConfig: recipe.HostConfig, AppArmorProfile: "docker-default"}
				for _, m := range recipe.HostConfig.Mounts {
					got.Mounts = append(got.Mounts, struct {
						Type, Source, Destination string
						RW                        bool
					}{m.Type, m.Source, m.Target, !m.ReadOnly})
				}
				got.State.Running = true
				got.State.Pid = 123
				switch fault {
				case "recipe":
					got.HostConfig.Privileged = true
				case "stopped":
					got.State.Running = false
				case "paused":
					got.State.Paused = true
				case "restarting":
					got.State.Restarting = true
				case "dead":
					got.State.Dead = true
				case "oom":
					got.State.OOMKilled = true
				case "pid":
					got.State.Pid = 1
				case "journal changed":
					changed := owner
					changed.Name = "phebs-typed-index-" + strings.Repeat("c", 32)
					put(journalPath(o), changed)
				}
				_ = json.NewEncoder(w).Encode(got)
			}))
			server.Listener = listener
			server.Start()
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if fault == "canceled" {
				cancel()
			}
			got, err := inspectRecorded(ctx, RecoveryOptions{Socket: o.Socket, ImageID: o.ImageID, Inputs: o.Inputs, PlanningDigest: o.Control.PlanningDigest, AttemptDigest: o.Control.AttemptDigest})
			want := fault == "good" || fault == "pending"
			if (err == nil) != want {
				t.Fatalf("inspection %v: %+v", err, got)
			}
			if want && (got.ContainerID != testContainer || got.PID != 123 || got.Control != owner.Control || got.Scratch != *owner.Scratch || requests != 3) {
				t.Fatalf("observation: %+v requests=%d", got, requests)
			}
			if !want && got != (RecordedInspection{}) {
				t.Fatal("partial authority returned")
			}
		})
	}
}

func TestValidateRecordedMetadata(t *testing.T) {
	for _, fault := range []string{"absent", "absent unconfigured", "unconfigured", "main", "initial", "pending", "orphan pending", "invalid object", "attempt", "socket", "image", "pending mismatch", "request", "scratch"} {
		t.Run(fault, func(t *testing.T) {
			_, o := fakeDaemon(t, "")
			o.Socket = filepath.Join(filepath.Dir(o.Socket), "metadata-only.sock")
			name, err := HostScratchRootName(o.Control.PlanningDigest, o.Control.AttemptDigest)
			if err != nil {
				t.Fatal(err)
			}
			o.scratch.Source = HostScratchBase + "/" + name + "/scratch"
			requestDigest := o.Control.RequestDigest
			owner := journal{Schema: ownerSchema, Name: "phebs-typed-index-" + strings.Repeat("a", 32), ContainerID: testContainer, DaemonID: "owned-daemon", ImageID: o.ImageID, Socket: o.Socket, Inputs: o.Inputs, Controls: o.Controls, Control: o.Control, Allowance: o.Allowance, Scratch: o.scratch}
			expected := RecoveryOptions{Socket: o.Socket, ImageID: o.ImageID, Inputs: o.Inputs, PlanningDigest: o.Control.PlanningDigest, AttemptDigest: o.Control.AttemptDigest}
			put := func(name string, value journal) {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(name, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "request" {
				requestDigest = "sha256:" + strings.Repeat("f", 64)
			}
			if fault == "scratch" {
				owner.Scratch.Source = HostScratchBase + "/wrong/scratch"
			}
			main := owner
			if fault == "initial" || fault == "pending" {
				main.ContainerID = ""
			}
			if fault == "absent unconfigured" || fault == "unconfigured" {
				expected.Socket = ""
				expected.ImageID = ""
			}
			if fault == "attempt" {
				expected.AttemptDigest = "sha256:" + strings.Repeat("f", 64)
			}
			if fault == "socket" {
				expected.Socket += ".other"
			}
			if fault == "image" {
				expected.ImageID = "sha256:" + strings.Repeat("f", 64)
			}
			if fault != "absent" && fault != "absent unconfigured" && fault != "orphan pending" {
				put(journalPath(o), main)
			}
			if fault == "invalid object" {
				if err := os.WriteFile(journalPath(o), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "pending" || fault == "orphan pending" || fault == "pending mismatch" {
				pending := owner
				if fault == "pending mismatch" {
					pending.Name = "phebs-typed-index-" + strings.Repeat("b", 32)
				}
				put(journalPath(o)+".next", pending)
			}
			err = ValidateRecordedMetadata(t.Context(), expected, requestDigest)
			good := fault == "absent" || fault == "absent unconfigured" || fault == "main" || fault == "initial" || fault == "pending"
			if (err == nil) != good {
				t.Fatalf("metadata acceptance %s: %v", fault, err)
			}
			// No daemon is listening: every positive path above must stay metadata-only.
		})
	}
}

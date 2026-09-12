package t421

import (
	"os/exec"
	"reflect"
	"slices"
	"testing"
)

func TestExecutionCommandObservation(t *testing.T) {
	// Supplied absolute paths and environment exercise the actual builder and
	// normalizer only, not native tool/config custody or admission.
	path, directory := "/private/phebs", "/private/workspace"
	parent := []string{"HOME=/private/home"}
	epoch := ExecutionEpochConfig{Epoch: 4, ConfigPath: "/private/config4", BackupRoot: "/private/backup"}
	for _, test := range []struct {
		verb string
		args []string
	}{
		{"serve", []string{path, "serve", "-config", epoch.ConfigPath}},
		{"backup", []string{path, "backup", "-config", epoch.ConfigPath, "-output", "/private/backup/archive"}},
		{"restore", []string{path, "restore", "-config", epoch.ConfigPath, "-backup", "/private/backup/archive"}},
	} {
		t.Run(test.verb, func(t *testing.T) {
			command, err := executionPhebsCommand(path, directory, test.verb, epoch, parent)
			environment := parent
			if test.verb == "serve" {
				environment = executionServeEnvironment(parent)
			}
			if err != nil || !slices.Equal(command.Args, test.args) || command.Dir != directory ||
				!slices.Equal(command.Env, environment) || command.Process != nil {
				t.Fatal("actual command bytes changed or child started", err)
			}
			for _, mode := range []string{"valid", "executable", "argv0", "directory", "environment", "config", "extra", "spelling", "verb", "archive"} {
				if mode == "archive" && test.verb == "serve" {
					continue
				}
				t.Run(mode, func(t *testing.T) {
					// Copy only unstarted command fields used by observation.
					changed := &exec.Cmd{Path: command.Path, Dir: command.Dir,
						Args: slices.Clone(command.Args), Env: slices.Clone(command.Env)}
					switch mode {
					case "executable":
						changed.Path += "-other"
					case "argv0":
						changed.Args[0] += "-other"
					case "directory":
						changed.Dir += "-other"
					case "environment":
						changed.Env = append(changed.Env, "UNDECLARED=1")
					case "config":
						changed.Args[3] += "-other"
					case "extra":
						changed.Args = append(changed.Args, "-addr", "127.0.0.1:4000")
					case "spelling":
						changed.Args[2] = "--config"
					case "verb":
						changed.Args[1] = "unknown"
					case "archive":
						changed.Args[5] += "-other"
					}
					got, err := observeExecutionCommand(changed, path, directory, epoch, environment)
					if (err == nil) != (mode == "valid") {
						t.Fatal("actual command mutation accepted", err)
					}
					if err != nil {
						if !reflect.DeepEqual(got, ExecutionCommandProfile{}) {
							t.Fatal("refusal retained partial observation")
						}
						return
					}
					want := frozenExecutionCommands()
					index := slices.IndexFunc(want, func(value ExecutionCommandProfile) bool { return value.Name == test.verb })
					if index < 0 || !reflect.DeepEqual(got, want[index]) {
						t.Fatal("observed command differs from frozen recipe")
					}
					got.NormalizedArgv[0] = "changed"
					if changed.Args[1] != test.verb || command.Args[1] != test.verb {
						t.Fatal("observation aliases actual argv")
					}
				})
			}
		})
	}
	if _, err := executionPhebsCommand(path, directory, "unknown", epoch, parent); err == nil {
		t.Fatal("unknown command built")
	}
}

func TestExecutionCommandFiveConfigsAndLegacyRecipe(t *testing.T) {
	// Modeled custody paths, not protected-input or real launch evidence.
	var epochs [5]ExecutionEpochConfig
	for index := range epochs {
		epochs[index] = ExecutionEpochConfig{Epoch: uint64(index + 1), ConfigPath: "/private/config" + string(rune('1'+index)),
			BackupRoot: "/private/backup", Home: "/private/home", Temporary: "/private/tmp"}
	}
	actual, err := observeExecutionCommands("/private/phebs", "/private/workspace", epochs, []string{"HOME=/private/home"})
	if err != nil {
		t.Fatal(err)
	}
	// Compare actual normalized recipes with all three existing validators.
	// Their supplied admission fixtures remain compatibility tests, not issuers.
	for _, plan := range lifecyclePolicyPlans(t) {
		tools, host := executionFreezeTestTools(plan, executionFreezeTestCommits()), executionFreezeTestHost()
		admission := executionProfileTestAdmission(t, plan, tools, host)
		profile, err := expectedExecutionProfile(plan, tools, host, admission)
		digest, digestErr := canonicalSHA256(actual)
		if err != nil || digestErr != nil || !reflect.DeepEqual(actual, profile.Commands) || digest != admission.commandsSHA256 {
			t.Fatal("legacy command recipe changed", plan.Schema, err, digestErr)
		}
	}
	for _, mode := range []string{"last_config", "last_epoch", "last_home", "last_tmp", "archive_root"} {
		t.Run(mode, func(t *testing.T) {
			changed := epochs
			switch mode {
			case "last_config":
				changed[4].ConfigPath = ""
			case "last_epoch":
				changed[4].Epoch = 1
			case "last_home":
				changed[4].Home += "-other"
			case "last_tmp":
				changed[4].Temporary += "-other"
			case "archive_root":
				changed[3].BackupRoot = ""
			}
			if rows, err := observeExecutionCommands("/private/phebs", "/private/workspace", changed, []string{"HOME=/private/home"}); err == nil || rows != nil {
				t.Fatal("incomplete actual command set retained")
			}
		})
	}
}

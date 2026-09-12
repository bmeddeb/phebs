package t421

import (
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
)

// executionPhebsCommand is the actual closed command builder shared by epoch
// launches and prework inspection. It does not start a child or issue admission.
// Recovery borrows the existing parent environment; serve keeps its existing copy.
func executionPhebsCommand(path, directory, verb string, epoch ExecutionEpochConfig, parent []string) (*exec.Cmd, error) {
	var args []string
	switch verb {
	case "serve":
		args = []string{verb, "-config", epoch.ConfigPath}
	case "backup":
		args = []string{verb, "-config", epoch.ConfigPath, "-output", filepath.Join(epoch.BackupRoot, "archive")}
	case "restore":
		args = []string{verb, "-config", epoch.ConfigPath, "-backup", filepath.Join(epoch.BackupRoot, "archive")}
	default:
		return nil, ErrExecutionEpochOne
	}
	command := exec.Command(path, args...)
	command.Dir, command.Env = directory, parent
	if verb == "serve" {
		command.Env = executionServeEnvironment(parent)
	}
	return command, nil
}

// observeExecutionCommand normalizes actual builder output against held paths
// and the independently captured concrete environment. Flags remain observed
// argv facts, not fields inferred from parsed YAML or frozen profile arrays.
func observeExecutionCommand(command *exec.Cmd, path, directory string, epoch ExecutionEpochConfig, environment []string) (ExecutionCommandProfile, error) {
	if command == nil || !filepath.IsAbs(path) || !filepath.IsAbs(directory) ||
		!filepath.IsAbs(epoch.ConfigPath) || command.Path != path || command.Dir != directory ||
		len(command.Args) < 2 || command.Args[0] != path || !slices.Equal(command.Env, environment) {
		return ExecutionCommandProfile{}, ErrExecutionEpochOne
	}
	name, class, flag, size := command.Args[1], "recovery", "", 6
	switch name {
	case "serve":
		class, size = "server", 4
	case "backup":
		flag = "-output"
	case "restore":
		flag = "-backup"
	default:
		return ExecutionCommandProfile{}, ErrExecutionEpochOne
	}
	if len(command.Args) != size || command.Args[2] != "-config" || command.Args[3] != epoch.ConfigPath ||
		size == 6 && (!filepath.IsAbs(epoch.BackupRoot) || command.Args[4] != flag || command.Args[5] != filepath.Join(epoch.BackupRoot, "archive")) {
		return ExecutionCommandProfile{}, ErrExecutionEpochOne
	}
	argv := slices.Clone(command.Args[1:])
	argv[2] = "@config"
	if size == 6 {
		argv[4] = "@backup"
	}
	return ExecutionCommandProfile{Name: name, ToolRole: "phebs", EnvironmentClass: class, NormalizedArgv: argv}, nil
}

// The five protected configs share owned runtime roots but have distinct bytes.
// Check every concrete recipe before collapsing equivalent serve rows. No
// launch, filesystem read or additional custody check is performed here.
func observeExecutionCommands(path, directory string, epochs [5]ExecutionEpochConfig, parent []string) ([]ExecutionCommandProfile, error) {
	server := executionServeEnvironment(parent)
	rows := make([]ExecutionCommandProfile, 3)
	for index, epoch := range epochs {
		if epoch.Epoch != uint64(index+1) || epoch.Home != epochs[0].Home || epoch.Temporary != epochs[0].Temporary {
			return nil, ErrExecutionEpochOne
		}
		command, err := executionPhebsCommand(path, directory, "serve", epoch, parent)
		if err != nil {
			return nil, err
		}
		observed, err := observeExecutionCommand(command, path, directory, epoch, server)
		if err != nil || index > 0 && !reflect.DeepEqual(rows[2], observed) {
			return nil, ErrExecutionEpochOne
		}
		rows[2] = observed
	}
	for index, verb := range []string{"backup", "restore"} {
		command, err := executionPhebsCommand(path, directory, verb, epochs[3], parent)
		if err != nil {
			return nil, err
		}
		rows[index], err = observeExecutionCommand(command, path, directory, epochs[3], parent)
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}

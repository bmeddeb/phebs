package t4110

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

type goTestEvent struct {
	Action string `json:"Action"`
	Test   string `json:"Test"`
}

type vitestReport struct {
	TotalTests   int                `json:"numTotalTests"`
	PassedTests  int                `json:"numPassedTests"`
	FailedTests  int                `json:"numFailedTests"`
	PendingTests int                `json:"numPendingTests"`
	TodoTests    int                `json:"numTodoTests"`
	Success      bool               `json:"success"`
	Results      []vitestFileResult `json:"testResults"`
}

type vitestFileResult struct {
	Status     string                  `json:"status"`
	Assertions []vitestAssertionResult `json:"assertionResults"`
}

type vitestAssertionResult struct {
	FullName string `json:"fullName"`
	Status   string `json:"status"`
	Title    string `json:"title"`
}

type composedReferenceBinaries struct {
	author admittedExecutable
	phebs  admittedExecutable
}

func runComposedGates(
	ctx context.Context,
	exportedRoot string,
	tools composedToolchain,
) ([]ComposedGate, error) {
	if err := tools.verify(); err != nil {
		return nil, err
	}
	result := gatesWithNames()
	for gateIndex := range result {
		gate := &result[gateIndex]
		goTests := make(map[string][]string)
		vitestNames := make([]string, 0)
		for _, identity := range gate.Tests {
			switch {
			case strings.HasPrefix(identity, "go:"):
				packageName, testName, ok := strings.Cut(strings.TrimPrefix(identity, "go:"), "#")
				if !ok || packageName == "" || testName == "" {
					return nil, fmt.Errorf("invalid composed Go identity %q", identity)
				}
				goTests[packageName] = append(goTests[packageName], testName)
			case strings.HasPrefix(identity, "vitest:ServiceDirectoryPage#"):
				vitestNames = append(
					vitestNames,
					strings.TrimPrefix(identity, "vitest:ServiceDirectoryPage#"),
				)
			default:
				return nil, fmt.Errorf("unsupported composed test identity %q", identity)
			}
		}

		packages := make([]string, 0, len(goTests))
		for packageName := range goTests {
			packages = append(packages, packageName)
		}
		sort.Strings(packages)
		for _, packageName := range packages {
			if err := runExactGoTests(
				ctx, exportedRoot, tools, packageName, goTests[packageName],
			); err != nil {
				return nil, fmt.Errorf("composed gate %s: %w", gate.Name, err)
			}
		}
		if len(vitestNames) != 0 {
			if err := runExactVitest(ctx, exportedRoot, tools, vitestNames); err != nil {
				return nil, fmt.Errorf("composed gate %s: %w", gate.Name, err)
			}
		}
		gate.Outcome = StepPassed
	}
	if err := tools.verify(); err != nil {
		return nil, err
	}
	return result, nil
}

func prepareComposedTree(
	ctx context.Context,
	repositoryRoot string,
	tools composedToolchain,
) error {
	if err := prepareComposedEnvironment(repositoryRoot); err != nil {
		return fmt.Errorf("prepare closed composed environment: %w", err)
	}
	goVerify := exec.CommandContext(ctx, tools.goTool.path, "mod", "verify")
	goVerify.Dir = repositoryRoot
	goVerify.Env = composedEnvironment(tools, repositoryRoot, true)
	if output, err := goVerify.CombinedOutput(); err != nil {
		return errors.Join(
			fmt.Errorf("verify composed Go modules: %w", err),
			boundedCommandError(string(output)),
		)
	}
	npmInstall := exec.CommandContext(
		ctx,
		tools.npm.path,
		"ci",
		"--ignore-scripts",
		"--offline",
		"--no-audit",
		"--no-fund",
	)
	npmInstall.Dir = filepath.Join(repositoryRoot, "ui")
	npmInstall.Env = composedEnvironment(tools, repositoryRoot, false)
	if output, err := npmInstall.CombinedOutput(); err != nil {
		return errors.Join(
			fmt.Errorf("install exact composed UI dependencies: %w", err),
			boundedCommandError(string(output)),
		)
	}
	return tools.verify()
}

func buildComposedUI(
	ctx context.Context,
	repositoryRoot string,
	tools composedToolchain,
) error {
	command := exec.CommandContext(ctx, tools.npm.path, "run", "build")
	command.Dir = filepath.Join(repositoryRoot, "ui")
	command.Env = composedEnvironment(tools, repositoryRoot, false)
	if output, err := command.CombinedOutput(); err != nil {
		return errors.Join(
			fmt.Errorf("build exact composed UI: %w", err),
			boundedCommandError(string(output)),
		)
	}
	index := filepath.Join(repositoryRoot, "ui", "dist", "index.html")
	if info, err := os.Stat(index); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.Join(err, errors.New("exact composed UI build has no index"))
	}
	return tools.verify()
}

func buildComposedReferenceBinaries(
	ctx context.Context,
	repositoryRoot, commit string,
	tools composedToolchain,
) (composedReferenceBinaries, error) {
	if err := tools.verify(); err != nil {
		return composedReferenceBinaries{}, err
	}
	boundCommit, err := verifyCleanCommitWithGit(ctx, repositoryRoot, tools.git.path)
	if err != nil || boundCommit != commit {
		return composedReferenceBinaries{}, errors.Join(
			errors.New("exact reference-build tree is not clean HEAD"), err,
		)
	}
	outputRoot := filepath.Join(repositoryRoot, composedExecutionDir, "reference-binaries")
	if err := os.Mkdir(outputRoot, 0o700); err != nil {
		return composedReferenceBinaries{}, err
	}
	type buildTarget struct {
		name        string
		packagePath string
		verify      func(string, string) (string, error)
	}
	targets := []buildTarget{
		{
			name: "phebs", packagePath: "./cmd/phebs",
			verify: verifyPhebsBinaryCommit,
		},
		{
			name: "author", packagePath: "./spike/t4110/cmd/author",
			verify: verifyAuthorBinaryCommit,
		},
	}
	result := composedReferenceBinaries{}
	for _, target := range targets {
		output := filepath.Join(outputRoot, target.name)
		command := exec.CommandContext(
			ctx, tools.goTool.path,
			"build", "-trimpath", "-pgo=off", "-o", output, target.packagePath,
		)
		command.Dir = repositoryRoot
		command.Env = composedEnvironment(tools, repositoryRoot, true)
		if commandOutput, err := command.CombinedOutput(); err != nil {
			return composedReferenceBinaries{}, errors.Join(
				fmt.Errorf("build exact reference %s: %w", target.name, err),
				boundedCommandError(string(commandOutput)),
			)
		}
		verified, err := target.verify(output, commit)
		if err != nil {
			return composedReferenceBinaries{}, fmt.Errorf(
				"verify exact reference %s: %w", target.name, err,
			)
		}
		executable, err := admitExecutable(verified)
		if err != nil {
			return composedReferenceBinaries{}, fmt.Errorf(
				"admit exact reference %s: %w", target.name, err,
			)
		}
		if target.name == "phebs" {
			result.phebs = executable
		} else {
			result.author = executable
		}
	}
	boundCommit, err = verifyCleanCommitWithGit(ctx, repositoryRoot, tools.git.path)
	if err != nil || boundCommit != commit {
		return composedReferenceBinaries{}, errors.Join(
			errors.New("exact reference builds changed their clean HEAD"), err,
		)
	}
	if err := tools.verify(); err != nil {
		return composedReferenceBinaries{}, err
	}
	return result, nil
}

func runExactGoTests(
	ctx context.Context,
	repositoryRoot string,
	tools composedToolchain,
	packageName string,
	tests []string,
) error {
	if len(tests) == 0 {
		return errors.New("composed Go test set is empty")
	}
	ordered := slices.Clone(tests)
	sort.Strings(ordered)
	patterns := make([]string, len(ordered))
	for index, testName := range ordered {
		patterns[index] = regexp.QuoteMeta(testName)
	}
	command := exec.CommandContext(
		ctx,
		tools.goTool.path,
		"test",
		"-mod=readonly",
		"-json",
		"./"+filepath.ToSlash(packageName),
		"-run",
		"^("+strings.Join(patterns, "|")+")$",
		"-count=1",
	)
	command.Dir = repositoryRoot
	command.Env = composedEnvironment(tools, repositoryRoot, true)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	runErr := command.Run()
	passed, parseErr := passedGoTests(output.Bytes())
	if runErr != nil || parseErr != nil {
		return errors.Join(
			fmt.Errorf("go test package %s: %w", packageName, runErr),
			parseErr,
			boundedCommandError(output.String()),
		)
	}
	for _, testName := range ordered {
		if !passed[testName] {
			return fmt.Errorf("go test %s#%s did not report pass", packageName, testName)
		}
	}
	return nil
}

func passedGoTests(data []byte) (map[string]bool, error) {
	result := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	const maximumEventBytes = 1 << 20
	scanner.Buffer(make([]byte, 64<<10), maximumEventBytes)
	for scanner.Scan() {
		var event goTestEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("decode go test event: %w", err)
		}
		if event.Action == "pass" && event.Test != "" {
			result[event.Test] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan go test events: %w", err)
	}
	return result, nil
}

func runExactVitest(
	ctx context.Context,
	repositoryRoot string,
	tools composedToolchain,
	tests []string,
) error {
	command := exec.CommandContext(
		ctx,
		tools.npm.path,
		"test",
		"--",
		"--run",
		"--reporter=json",
		"src/pages/ServiceDirectoryPage.test.tsx",
	)
	command.Dir = filepath.Join(repositoryRoot, "ui")
	command.Env = composedEnvironment(tools, repositoryRoot, false)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return errors.Join(
			fmt.Errorf("run ServiceDirectoryPage vitest: %w", err),
			boundedCommandError(output.String()),
		)
	}
	passed, err := passedVitest(output.Bytes())
	if err != nil {
		return errors.Join(err, boundedCommandError(output.String()))
	}
	for _, testName := range tests {
		if passed[testName] != 1 {
			return fmt.Errorf("vitest %q did not report exactly one pass", testName)
		}
	}
	return nil
}

func passedVitest(data []byte) (map[string]int, error) {
	var report *vitestReport
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var candidate vitestReport
		if json.Unmarshal(line, &candidate) != nil {
			continue
		}
		if report != nil {
			return nil, errors.New("vitest emitted multiple JSON reports")
		}
		report = &candidate
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan vitest report: %w", err)
	}
	if report == nil || !report.Success || report.TotalTests < 1 ||
		report.PassedTests != report.TotalTests || report.FailedTests != 0 ||
		report.PendingTests != 0 || report.TodoTests != 0 || len(report.Results) != 1 ||
		report.Results[0].Status != "passed" {
		return nil, errors.New("vitest report is not an exact all-passed run")
	}
	passed := make(map[string]int, report.TotalTests)
	for _, assertion := range report.Results[0].Assertions {
		if assertion.Status != "passed" || assertion.Title == "" ||
			assertion.FullName != assertion.Title {
			return nil, errors.New("vitest assertion is not an exact top-level pass")
		}
		passed[assertion.Title]++
	}
	if len(report.Results[0].Assertions) != report.TotalTests {
		return nil, errors.New("vitest assertion inventory is incomplete")
	}
	return passed, nil
}

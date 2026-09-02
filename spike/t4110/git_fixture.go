package t4110

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bmeddeb/phebs/spike/t411"
)

func writeBareCorpus(
	ctx context.Context,
	directory string,
	gitBinary string,
	files []t411.FixtureFile,
) (string, error) {
	if len(files) == 0 {
		return "", errors.New("T41.10 corpus is empty")
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		return "", err
	}
	if _, err := runCommand(ctx, "", gitBinary, "init", "--bare", directory); err != nil {
		return "", fmt.Errorf("initialize neutral repository: %w", err)
	}
	command := exec.CommandContext(ctx, gitBinary, "-C", directory, "fast-import", "--quiet")
	command.Env = gitEnvironment()
	stdin, err := command.StdinPipe()
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start neutral repository import: %w", err)
	}
	writeErr := writeFastImport(stdin, files)
	closeErr := stdin.Close()
	waitErr := command.Wait()
	if writeErr != nil || closeErr != nil || waitErr != nil {
		return "", fmt.Errorf(
			"import neutral repository: %w",
			errors.Join(writeErr, closeErr, waitErr, boundedCommandError(output.String())),
		)
	}
	if _, err := runCommand(
		ctx,
		"",
		gitBinary,
		"-C",
		directory,
		"symbolic-ref",
		"HEAD",
		"refs/heads/main",
	); err != nil {
		return "", fmt.Errorf("select neutral repository HEAD: %w", err)
	}
	commit, err := runCommand(
		ctx, "", gitBinary, "-C", directory, "rev-parse", "refs/heads/main",
	)
	if err != nil {
		return "", fmt.Errorf("resolve neutral repository commit: %w", err)
	}
	commit = strings.TrimSpace(commit)
	if len(commit) != 40 {
		return "", errors.New("neutral repository commit is invalid")
	}
	return commit, nil
}

func writeFastImport(writer io.Writer, files []t411.FixtureFile) error {
	for index, file := range files {
		if !safeFastImportPath(file.Path) || len(file.Content) == 0 ||
			(index > 0 && files[index-1].Path >= file.Path) {
			return errors.New("T41.10 corpus file is invalid")
		}
	}
	for index, file := range files {
		if _, err := fmt.Fprintf(
			writer,
			"blob\nmark :%d\ndata %d\n",
			index+1,
			len(file.Content),
		); err != nil {
			return err
		}
		if _, err := writer.Write(file.Content); err != nil {
			return err
		}
		if _, err := io.WriteString(writer, "\n"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(
		writer,
		"commit refs/heads/main\nmark :%d\ncommitter Neutral Gate <gate@neutral.invalid> 0 +0000\ndata 0\n",
		len(files)+1,
	); err != nil {
		return err
	}
	for index, file := range files {
		if _, err := fmt.Fprintf(writer, "M 100644 :%d %s\n", index+1, file.Path); err != nil {
			return err
		}
	}
	_, err := io.WriteString(writer, "\ndone\n")
	return err
}

func safeFastImportPath(path string) bool {
	if !fs.ValidPath(path) {
		return false
	}
	for _, character := range path {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("/._-", character) {
			continue
		}
		return false
	}
	return true
}

func runCommand(ctx context.Context, directory, name string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	if name == "git" || filepath.Base(name) == "git" {
		command.Env = gitEnvironment()
	}
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		return "", errors.Join(err, boundedCommandError(output.String()))
	}
	return output.String(), nil
}

func gitEnvironment() []string {
	return []string{
		"HOME=",
		"TMPDIR=" + os.TempDir(),
		"PATH=/usr/bin:/bin",
		"LANG=C",
		"LC_ALL=C",
		"TZ=UTC",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=core.fsmonitor",
		"GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=core.untrackedcache",
		"GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=core.hookspath",
		"GIT_CONFIG_VALUE_2=" + os.DevNull,
	}
}

func verifyNoAmbientGitEnvironment() error {
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_") {
			return errors.New("T41.10 live author inherited a Git environment variable")
		}
	}
	return nil
}

func boundedCommandError(output string) error {
	const maximum = 8 << 10
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	if len(output) > maximum {
		output = output[len(output)-maximum:]
	}
	return errors.New(output)
}

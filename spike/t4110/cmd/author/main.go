package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bmeddeb/phebs/spike/t4013"
	t4110 "github.com/bmeddeb/phebs/spike/t4110"
)

const privateSessionEnvironment = "PHEBS_T4110_PRIVATE_SESSION"

func main() {
	var err error
	if os.Getenv(privateSessionEnvironment) == "1" {
		err = run()
	} else {
		err = runInPrivateSession()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runInPrivateSession() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	environment := make([]string, 0, len(os.Environ())+1)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, privateSessionEnvironment+"=") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, privateSessionEnvironment+"=1")
	return runPrivateSessionCommand(executable, os.Args[1:], environment)
}

func runPrivateSessionCommand(executable string, arguments, environment []string) error {
	ctx, stop := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP,
	)
	defer stop()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = environment
	output, err := t4013.RunCustodyCombinedOutput(command)
	if len(output) != 0 {
		_, _ = os.Stdout.Write(output)
	}
	return err
}

func run() error {
	if err := verifyPrivateSession(); err != nil {
		return err
	}
	var options t4110.LiveOptions
	var destination string
	flag.StringVar(&destination, "out", "", "new source-free receipt path (must not exist)")
	flag.StringVar(&options.RepositoryRoot, "repository-root", ".", "clean Phebs checkout")
	flag.StringVar(&options.PhebsBinary, "phebs-binary", "", "exact clean-HEAD Phebs binary")
	flag.StringVar(&options.PhebsVersion, "phebs-version", "", "expected Phebs version (default: binary version output)")
	flag.StringVar(&options.ZoektBinary, "zoekt-binary", "", "same-module zoekt-git-index binary")
	flag.StringVar(&options.BrowserBinary, "browser-binary", "", "explicit Playwright-compatible browser executable")
	flag.Parse()
	if destination == "" || options.PhebsBinary == "" || options.BrowserBinary == "" || flag.NArg() != 0 {
		return errors.New("-out, -phebs-binary, and -browser-binary are required; positional arguments are not accepted")
	}
	if err := clearAmbientGitEnvironment(); err != nil {
		return err
	}
	root, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return err
	}
	options.RepositoryRoot = root
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return errors.New("receipt destination already exists")
	} else if !os.IsNotExist(err) {
		return err
	}

	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP,
	)
	defer cancel()
	identity, err := t4110.RunAndAuthor(ctx, options, destination)
	if err != nil {
		return err
	}
	fmt.Printf("T41.10 source-free receipt: bytes=%d sha256=%s\n", identity.Bytes, identity.SHA256)
	return nil
}

func verifyPrivateSession() error {
	members, err := t4013.PrivateProcessSessionMembers(os.Getpid())
	if err != nil || members != 1 {
		return errors.Join(
			fmt.Errorf("T41.10 author process session has %d live member(s)", members),
			err,
		)
	}
	return nil
}

func clearAmbientGitEnvironment() error {
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GIT_") {
			if err := os.Unsetenv(name); err != nil {
				return fmt.Errorf("clear ambient Git environment: %w", err)
			}
		}
	}
	return nil
}

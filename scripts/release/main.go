package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/internal/releasebundle"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: go run ./scripts/release <bundle|verify> [flags]")
	}
	switch args[0] {
	case "bundle":
		return bundle(args[1:])
	case "verify":
		return verify(args[1:])
	default:
		return fmt.Errorf("unknown release command %q", args[0])
	}
}

func bundle(args []string) error {
	flags := flag.NewFlagSet("bundle", flag.ContinueOnError)
	var opts releasebundle.BuildOptions
	var phebs, zoekt, buf, license, readme string
	flags.StringVar(&opts.OutputDir, "output", "", "new release directory")
	flags.StringVar(&opts.Version, "version", "", "release SemVer")
	flags.StringVar(&opts.Commit, "commit", "", "source commit")
	flags.StringVar(&opts.GoVersion, "go-version", "", "Go toolchain version")
	flags.StringVar(&opts.GOOS, "goos", "", "target GOOS")
	flags.StringVar(&opts.GOARCH, "goarch", "", "target GOARCH")
	flags.StringVar(&phebs, "phebs", "phebs", "phebs executable")
	flags.StringVar(&zoekt, "zoekt", "bin/zoekt-git-index", "zoekt-git-index executable")
	flags.StringVar(&buf, "buf", "bin/buf", "Buf executable")
	flags.StringVar(&license, "license", "LICENSE", "license file")
	flags.StringVar(&readme, "readme", "README.md", "readme file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("bundle accepts flags only")
	}
	opts.Sources = []releasebundle.Source{
		{Path: "phebs", SourcePath: phebs, Executable: true},
		{Path: "bin/zoekt-git-index", SourcePath: zoekt, Executable: true},
		{Path: "bin/buf", SourcePath: buf, Executable: true},
		{Path: "LICENSE", SourcePath: license},
		{Path: "README.md", SourcePath: readme},
	}
	manifest, err := releasebundle.Build(opts)
	if err != nil {
		return err
	}
	fmt.Printf("assembled %s (%d files)\n", opts.OutputDir, len(manifest.Files))
	return nil
}

func verify(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	dir := flags.String("bundle", "", "release directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *dir == "" {
		return errors.New("usage: go run ./scripts/release verify -bundle DIR")
	}
	manifest, err := releasebundle.Verify(*dir)
	if err != nil {
		return err
	}
	fmt.Printf("verified %s %s %s/%s (%d files)\n",
		manifest.Version, manifest.Commit, manifest.GOOS, manifest.GOARCH, len(manifest.Files))
	return nil
}

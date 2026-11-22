package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

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
		return errors.New("usage: go run ./scripts/release <bundle|verify|package> [flags]")
	}
	switch args[0] {
	case "bundle":
		return bundle(args[1:])
	case "verify":
		return verify(args[1:])
	case "package":
		return packageRelease(args[1:])
	default:
		return fmt.Errorf("unknown release command %q", args[0])
	}
}

func bundle(args []string) error {
	flags := flag.NewFlagSet("bundle", flag.ContinueOnError)
	var opts releasebundle.BuildOptions
	var phebs, zoekt, focused, buf, license, readme, otelDemo string
	flags.StringVar(&opts.OutputDir, "output", "", "new release directory")
	flags.StringVar(&opts.Version, "version", "", "release SemVer")
	flags.StringVar(&opts.Commit, "commit", "", "source commit")
	flags.StringVar(&opts.GoVersion, "go-version", "", "Go toolchain version")
	flags.StringVar(&opts.GOOS, "goos", "", "target GOOS")
	flags.StringVar(&opts.GOARCH, "goarch", "", "target GOARCH")
	flags.StringVar(&phebs, "phebs", "phebs", "phebs executable")
	flags.StringVar(&zoekt, "zoekt", "bin/zoekt-git-index", "zoekt-git-index executable")
	flags.StringVar(&focused, "focused", "bin/phebs-focused-index", "focused-index executable")
	flags.StringVar(&buf, "buf", "bin/buf", "Buf executable")
	flags.StringVar(&license, "license", "LICENSE", "license file")
	flags.StringVar(&readme, "readme", "README.md", "readme file")
	flags.StringVar(&otelDemo, "otel-demo", "phebs-otel-demo.yaml", "OpenTelemetry microservices demo config")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("bundle accepts flags only")
	}
	opts.Sources = []releasebundle.Source{
		{Path: "phebs", SourcePath: phebs, Executable: true},
		{Path: "bin/zoekt-git-index", SourcePath: zoekt, Executable: true},
		{Path: "bin/phebs-focused-index", SourcePath: focused, Executable: true},
		{Path: "bin/buf", SourcePath: buf, Executable: true},
		{Path: "LICENSE", SourcePath: license},
		{Path: "README.md", SourcePath: readme},
		{Path: "phebs-otel-demo.yaml", SourcePath: otelDemo},
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

// packageRelease assembles the T48.6 default-on package: the verified bundle as
// runtime/ and one record per template signed by `runtime/phebs release-sign`,
// so every binding is derived and self-verified by the binary being released.
func packageRelease(args []string) error {
	flags := flag.NewFlagSet("package", flag.ContinueOnError)
	var opts releasebundle.PackageOptions
	var key, keyID string
	flags.StringVar(&opts.BundleDir, "bundle", "", "verified release directory")
	flags.StringVar(&opts.OutputDir, "output", "", "new package directory")
	flags.StringVar(&opts.TemplatesDir, "templates", "release/packs/templates", "PackRelease templates")
	flags.StringVar(&opts.ArtifactsDir, "artifacts", "release/packs/artifacts", "flat pack artifact directory")
	flags.StringVar(&key, "key", "", "PKCS#8 PEM ed25519 release key")
	flags.StringVar(&keyID, "key-id", "", "release key identifier in the in-tree trust anchor")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || opts.BundleDir == "" || opts.OutputDir == "" || key == "" || keyID == "" {
		return errors.New("usage: go run ./scripts/release package -bundle DIR -output DIR -key PEM -key-id ID [-templates DIR] [-artifacts DIR]")
	}
	opts.Sign = func(ctx context.Context, phebs, template, artifacts, out string) error {
		cmd := exec.CommandContext(ctx, phebs, "release-sign",
			"-template", template, "-key", key, "-key-id", keyID, "-artifacts", artifacts, "-out", out)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	if err := releasebundle.Package(context.Background(), opts); err != nil {
		return err
	}
	fmt.Printf("packaged %s\n", opts.OutputDir)
	return nil
}

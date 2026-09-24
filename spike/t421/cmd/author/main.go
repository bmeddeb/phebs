package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bmeddeb/phebs/spike/t421"
)

func main() {
	options, err := parseAuthorOptions(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		fail(err)
	}
	identity, err := options.author(context.Background(), options.destination, options.repositoryRoot, options.sourceCommit)
	if err != nil {
		fail(err)
	}
	fmt.Printf("T42.1 source-free plan: bytes=%d sha256=%s\n", identity.Bytes, identity.SHA256)
}

type authorOptions struct {
	destination, repositoryRoot, sourceCommit string
	author                                    func(context.Context, string, string, string) (t421.PlanIdentity, error)
}

// Schema selection is closed before invoking either genuine author. The
// historical invocation remains V2; later contracts must be selected explicitly.
func parseAuthorOptions(args []string, output io.Writer) (authorOptions, error) {
	var options authorOptions
	var schema string
	flags := flag.NewFlagSet("t421-author", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&options.destination, "out", "", "new source-free plan path (must not exist)")
	flags.StringVar(&options.repositoryRoot, "repository-root", ".", "exact clean Phebs checkout")
	flags.StringVar(&options.sourceCommit, "source-commit", "", "exact clean implementation commit")
	flags.StringVar(&schema, "schema", "v2", "plan schema: v2 (historical default), v3, v4, or v5")
	if err := flags.Parse(args); err != nil {
		return authorOptions{}, err
	}
	if options.destination == "" || options.sourceCommit == "" || flags.NArg() != 0 {
		return authorOptions{}, errors.New("-out and -source-commit are required; positional arguments are not accepted")
	}
	switch schema {
	case "v2":
		options.author = t421.Author
	case "v3":
		options.author = t421.AuthorV3
	case "v4":
		options.author = t421.AuthorV4
	case "v5":
		options.author = t421.AuthorV5
	default:
		return authorOptions{}, errors.New("-schema must be v2, v3, v4, or v5")
	}
	root, err := filepath.Abs(options.repositoryRoot)
	if err != nil {
		return authorOptions{}, err
	}
	options.repositoryRoot = root
	return options, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "t421-author:", err)
	os.Exit(1)
}

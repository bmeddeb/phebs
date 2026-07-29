package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/bmeddeb/phebs/internal/focusedindex"
)

func main() {
	var requestPath string
	var resultPath string
	flags := flag.NewFlagSet("phebs-focused-index", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&requestPath, "request", "", "strict focused-index request JSON")
	flags.StringVar(&resultPath, "result", "", "exclusive focused-index result JSON")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if requestPath == "" || resultPath == "" || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: phebs-focused-index -request PATH -result PATH")
		os.Exit(2)
	}
	request, err := focusedindex.ReadRequest(requestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read request: %v\n", err)
		os.Exit(1)
	}
	result, err := focusedindex.Build(context.Background(), request, focusedindex.BuildOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "build focused index: %v\n", err)
		os.Exit(1)
	}
	if err := focusedindex.WriteControlFile(resultPath, result); err != nil {
		fmt.Fprintf(os.Stderr, "write result: %v\n", err)
		os.Exit(1)
	}
}

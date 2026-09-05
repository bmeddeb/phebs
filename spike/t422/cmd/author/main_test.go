package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
)

func TestAuthorCLIChild(t *testing.T) {
	if os.Getenv("T422_AUTHOR_CLI_TEST_CHILD") != "1" {
		return
	}
	os.Args = []string{"t422-author"}
	if os.Getenv("T422_AUTHOR_CLI_TEST_ARGUMENT") == "1" {
		os.Args = append(os.Args, "--source=/private/not-authorized")
	}
	main()
}

func TestAuthorCLIRefusesUnboundInvocation(t *testing.T) {
	for _, name := range []string{"absent bootstrap", "missing endpoints", "unknown selector", "path argument"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, binary, "-test.run=^TestAuthorCLIChild$")
			command.Env = []string{"T422_AUTHOR_CLI_TEST_CHILD=1"}
			switch name {
			case "missing endpoints":
				command.Env = append(command.Env, dispatchadmission.ProductionEnvironment+"="+dispatchadmission.ProductionSelector)
			case "unknown selector":
				command.Env = append(command.Env, dispatchadmission.ProductionEnvironment+"=untrusted")
			case "path argument":
				command.Env = append(command.Env, "T422_AUTHOR_CLI_TEST_ARGUMENT=1")
			}
			var output, diagnostic bytes.Buffer
			command.Stdout, command.Stderr = &output, &diagnostic
			command.Stdin = bytes.NewBufferString("private untrusted source input")
			if err := command.Run(); err == nil || command.ProcessState == nil || output.Len() != 0 ||
				diagnostic.String() != "t422-author: authenticated author operation unavailable\n" {
				t.Fatalf("unbound CLI did not fail source-free: stdout=%q stderr=%q err=%v", output.String(), diagnostic.String(), err)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/bmeddeb/phebs/internal/dispatchadmission"
	"github.com/bmeddeb/phebs/spike/t421"
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

// This inherited process tests only the real fd0/fd1 transport. It does not
// bootstrap an author, accept a plan, or claim a successful corpus operation.
func TestAuthorSocketChild(t *testing.T) {
	mode := os.Getenv("T422_AUTHOR_SOCKET_CHILD")
	if mode == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	file := os.NewFile(3, "author-test-control")
	connection, err := net.FileConn(file)
	if err != nil || file.Close() != nil {
		os.Exit(10)
	}
	defer func() { _ = connection.Close() }()
	if connection.SetDeadline(time.Now().Add(5*time.Second)) != nil {
		os.Exit(11)
	}
	if mode == "cancel-no-eof" {
		readCtx, cancelRead := context.WithCancel(ctx)
		socket, closeSocket, err := openAuthorSocket(readCtx, os.Stdin)
		if err != nil || writeAuthorResponse(ctx, os.Stdout, []byte("ready\n")) != nil {
			os.Exit(12)
		}
		canceled := make(chan struct{})
		go func() {
			var token [1]byte
			_, _ = io.ReadFull(connection, token[:])
			cancelRead()
			close(canceled)
		}()
		// No EOF is sent. Cancellation must interrupt this actual socket read,
		// even if it arrives immediately before Read rather than while blocked.
		_, err = io.ReadAll(io.LimitReader(socket, t421.MaxExecutionCorpusAuthorRequestBytes+1))
		<-canceled
		if err == nil || readCtx.Err() == nil || closeSocket() != nil {
			os.Exit(13)
		}
	} else {
		raw, err := readAuthorRequest(ctx, os.Stdin)
		if mode == "oversized" {
			if !errors.Is(err, t421.ErrExecutionCorpusAuthor) {
				os.Exit(14)
			}
		} else if err != nil || len(raw) != t421.MaxExecutionCorpusAuthorRequestBytes {
			os.Exit(15)
		}
	}
	if _, err := os.Stdin.Stat(); err != nil {
		os.Exit(16)
	}
	response := bytes.Repeat([]byte{'r'}, t421.MaxExecutionCorpusAuthorResponseBytes)
	if writeAuthorResponse(ctx, os.Stdout, response) != nil {
		os.Exit(17)
	}
	if _, err := os.Stdout.Stat(); err != nil {
		os.Exit(18)
	}
	// Keep the process's borrowed stdout alive after closing our duplicate.
	// The parent must see terminal EOF only after releasing and joining us.
	var token [1]byte
	if _, err := io.ReadFull(connection, token[:]); err != nil {
		os.Exit(19)
	}
	if connection.Close() != nil {
		os.Exit(20)
	}
	os.Exit(0)
}

func TestAuthorInheritedSocketTransport(t *testing.T) {
	for _, mode := range []string{"bounded", "oversized", "cancel-no-eof"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			var sockets [3]*net.UnixConn
			var children [3]*os.File
			for index := range sockets {
				parent, child, err := dispatchadmission.NewPipe()
				if err != nil {
					t.Fatal(err)
				}
				children[index] = child
				t.Cleanup(func() { _ = child.Close() })
				connection, err := net.FileConn(parent)
				closeErr := parent.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("adopt test parent: %v / %v", err, closeErr)
				}
				t.Cleanup(func() { _ = connection.Close() })
				sockets[index] = connection.(*net.UnixConn)
				if connection.SetDeadline(time.Now().Add(5*time.Second)) != nil {
					t.Fatal("test parent deadline")
				}
			}
			command := exec.CommandContext(ctx, binary, "-test.run=^TestAuthorSocketChild$")
			command.Env = []string{"T422_AUTHOR_SOCKET_CHILD=" + mode}
			command.Stdin, command.Stdout = children[0], children[1]
			command.ExtraFiles = []*os.File{children[2]}
			command.Stderr = io.Discard
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			waited := make(chan error, 1)
			go func() { waited <- command.Wait() }() // Sole owned native join.
			joined := false
			t.Cleanup(func() {
				if !joined {
					_ = command.Process.Kill()
					<-waited
				}
			})
			for _, child := range children {
				if child.Close() != nil {
					t.Fatal("close inherited child copy")
				}
			}
			inputBytes := t421.MaxExecutionCorpusAuthorRequestBytes
			switch mode {
			case "oversized":
				inputBytes++
			case "cancel-no-eof":
				inputBytes = 1
			}
			if count, err := sockets[0].Write(bytes.Repeat([]byte{'q'}, inputBytes)); err != nil || count != inputBytes {
				t.Fatalf("send request: %d / %v", count, err)
			}
			if mode == "cancel-no-eof" {
				var ready [6]byte
				if _, err := io.ReadFull(sockets[1], ready[:]); err != nil || string(ready[:]) != "ready\n" {
					t.Fatalf("pollable read was not armed: %q / %v", ready, err)
				}
				if _, err := sockets[2].Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
			} else if sockets[0].CloseWrite() != nil {
				t.Fatal("send request EOF")
			}
			response := make([]byte, t421.MaxExecutionCorpusAuthorResponseBytes)
			if _, err := io.ReadFull(sockets[1], response); err != nil || !bytes.Equal(response, bytes.Repeat([]byte{'r'}, len(response))) {
				t.Fatalf("bounded inherited response: %v", err)
			}
			if sockets[1].SetReadDeadline(time.Now()) != nil {
				t.Fatal("probe borrowed stdout lifetime")
			}
			var sentinel [1]byte
			if _, err := sockets[1].Read(sentinel[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("borrowed stdout closed before native join: %v", err)
			}
			if _, err := sockets[2].Write([]byte{2}); err != nil {
				t.Fatal(err)
			}
			err = <-waited
			joined = true
			if err != nil || command.ProcessState == nil || !command.ProcessState.Success() {
				t.Fatalf("actual child join: %v", err)
			}
			if sockets[1].SetReadDeadline(time.Now().Add(time.Second)) != nil {
				t.Fatal("terminal EOF deadline")
			}
			if _, err := sockets[1].Read(sentinel[:]); !errors.Is(err, io.EOF) {
				t.Fatalf("joined child did not produce exact EOF: %v", err)
			}
		})
	}
}

func TestAuthorSocketRefusesWrongDescriptorsAndOversizedResponse(t *testing.T) {
	regular, err := os.CreateTemp(t.TempDir(), "not-a-socket")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = regular.Close() }()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	for _, file := range []*os.File{regular, reader, writer} {
		if _, err := readAuthorRequest(t.Context(), file); !errors.Is(err, t421.ErrExecutionCorpusAuthor) {
			t.Fatal("non-socket request admitted", err)
		}
		if err := writeAuthorResponse(t.Context(), file, []byte("response")); !errors.Is(err, t421.ErrExecutionCorpusAuthor) {
			t.Fatal("non-socket response admitted", err)
		}
		if _, err := file.Stat(); err != nil {
			t.Fatal("refusal closed borrowed descriptor", err)
		}
	}
	parent, child, err := dispatchadmission.NewPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Close(); _ = child.Close() }()
	if err := writeAuthorResponse(t.Context(), child, bytes.Repeat([]byte{'x'}, t421.MaxExecutionCorpusAuthorResponseBytes+1)); !errors.Is(err, t421.ErrExecutionCorpusAuthor) {
		t.Fatal("oversized response admitted", err)
	}
	if _, err := child.Stat(); err != nil {
		t.Fatal("oversized refusal closed borrowed stdout", err)
	}
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

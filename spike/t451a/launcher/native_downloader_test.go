package launcher

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var downloaderBazel = flag.String("t451b-downloader-bazel", "", "exact admitted Bazel ZIP for opt-in host Java downloader proof")
var downloaderJavaHome = flag.String("t451b-downloader-java-home", "", "existing host JDK; never installs or downloads tools")

func TestNativeDownloaderHostProof(t *testing.T) {
	if *downloaderBazel == "" && *downloaderJavaHome == "" {
		t.Skip("explicit Bazel and existing host JDK paths required")
	}
	if !filepath.IsAbs(*downloaderBazel) || !filepath.IsAbs(*downloaderJavaHome) {
		t.Fatal("both tool paths must be explicit and absolute")
	}
	const bazelSHA = "cab23c59d3d39c5e5382f12cd116b47445afdff9813516c18ae3ee8836b3037f"
	const jarSHA = "1595bc4dfc4483480c42e3f4a2bbec41220ffe7a8e0f606991321be19696212f"
	bazel := downloaderProofRead(t, *downloaderBazel, 64<<20)
	if len(bazel) != 63709962 || hash(bazel) != bazelSHA {
		t.Fatal("Bazel identity differs from the admitted native binary")
	}
	archive, err := zip.NewReader(bytes.NewReader(bazel), int64(len(bazel)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	jar := filepath.Join(dir, "A-server.jar")
	found := false
	for _, entry := range archive.File {
		if entry.Name != "A-server.jar" {
			continue
		}
		if found || entry.UncompressedSize64 != 116237984 {
			t.Fatal("unexpected embedded server jar count or size")
		}
		found = true
		reader, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 116237985))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || len(data) != 116237984 || hash(data) != jarSHA {
			t.Fatalf("embedded jar identity/read refused: %v / %v", readErr, closeErr)
		}
		if err := os.WriteFile(jar, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatal("missing embedded server jar")
	}
	config := filepath.Join(dir, "downloader.cfg")
	if err := os.WriteFile(config, []byte(NativeDownloaderConfig), 0600); err != nil {
		t.Fatal(err)
	}
	source := downloaderProofRead(t, filepath.Join("testdata", "native_downloader", "DownloaderProof.java"), 64<<10)
	javaSource := filepath.Join(dir, "DownloaderProof.java")
	if err := os.WriteFile(javaSource, source, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"java", "javac"} {
		path := filepath.Join(*downloaderJavaHome, "bin", tool)
		data := downloaderProofRead(t, path, 2<<20)
		t.Logf("host %s bytes=%d sha256=%s", tool, len(data), hash(data))
	}
	t.Logf("Bazel sha256=%s embedded_jar sha256=%s owned_java sha256=%s config sha256=%s", bazelSHA, jarSHA, hash(source), hash([]byte(NativeDownloaderConfig)))
	run := func(tool string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, filepath.Join(*downloaderJavaHome, "bin", tool), args...)
		command.Dir = dir
		command.Env = []string{"HOME=" + dir, "TMPDIR=" + dir, "LANG=C", "LC_ALL=C"}
		command.WaitDelay = time.Second
		budget := outputBudget{remaining: 64 << 10, cancel: cancel}
		output := outputWriter{budget: &budget}
		command.Stdout, command.Stderr = &output, &output
		if err := command.Run(); err != nil {
			t.Fatalf("host %s proof failed: %v\n%s", tool, err, output.data.String())
		}
		t.Logf("host %s:\n%s", tool, output.data.String())
		return output.data.String()
	}
	run("java", "-version")
	run("javac", "-J-Xmx256m", "-proc:none", "-cp", jar, "-d", dir, javaSource)
	result := run("java", "-Xmx256m", "-cp", dir+string(os.PathListSeparator)+jar, "com.google.devtools.build.lib.bazel.repository.downloader.DownloaderProof", config)
	if !strings.Contains(result, "NATIVE_DOWNLOADER_HOST_PROOF_PASS") {
		t.Fatal("host proof omitted success marker")
	}
}

func downloaderProofRead(t *testing.T, path string, limit int64) []byte {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		t.Fatalf("bounded proof input refused: %v", err)
	}
	return data
}

//go:build linux && phebs_t42_native

package t421

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This fixed pthread engine creates no thread after its final READY. It tests
// measurement and post-CONT registration without Go runtime creation racing
// natural process exit. Lost creation history on fatal group exit remains a
// strict production refusal; this fixture grants no general termination claim.
const linuxEpochEngineFixtureC = `
#define _POSIX_C_SOURCE 200809L
#include <pthread.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>

static pthread_mutex_t mu = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t cv = PTHREAD_COND_INITIALIZER;
static int ready, finish;
static void *worker(void *unused) {
    (void)unused;
    if (pthread_mutex_lock(&mu)) _exit(125);
    ready++;
    if (pthread_cond_broadcast(&cv)) _exit(125);
    while (!finish) if (pthread_cond_wait(&cv, &mu)) _exit(125);
    if (pthread_mutex_unlock(&mu)) _exit(125);
    return NULL;
}
static void await_ready(int count) {
    if (pthread_mutex_lock(&mu)) _exit(125);
    while (ready != count) if (pthread_cond_wait(&cv, &mu)) _exit(125);
    if (pthread_mutex_unlock(&mu)) _exit(125);
}
static void wait_signal(int expected) {
    sigset_t set;
    int actual = 0;
    if (sigemptyset(&set) || sigaddset(&set, expected) || sigwait(&set, &actual) || actual != expected) _exit(125);
}
int main(void) {
    sigset_t blocked;
    pthread_t threads[9];
    int count = 1;
    if (sigemptyset(&blocked) || sigaddset(&blocked, SIGCONT) || sigaddset(&blocked, SIGUSR2) || pthread_sigmask(SIG_BLOCK, &blocked, NULL)) return 125;
    if (pthread_create(&threads[0], NULL, worker, NULL)) return 125;
    await_ready(1);
    if (printf("TRACE_ENGINE_CHILD_READY {\"Mode\":\"pthread-engine\",\"PID\":%ld,\"Tasks\":2}\n", (long)getpid()) < 0 || fflush(stdout)) return 125;
    if (getenv("PHEBS_PRIVATE_EPOCH_ENGINE_BURST")) {
        wait_signal(SIGCONT);
        for (int i = 1; i < 9; i++) if (pthread_create(&threads[i], NULL, worker, NULL)) return 125;
        count = 9;
        await_ready(count);
        if (printf("TRACE_ENGINE_BURST_READY {\"Mode\":\"pthread-engine\",\"PID\":%ld,\"Tasks\":8}\n", (long)getpid()) < 0 || fflush(stdout)) return 125;
    }
    wait_signal(SIGUSR2);
    if (pthread_mutex_lock(&mu)) return 125;
    finish = 1;
    if (pthread_cond_broadcast(&cv) || pthread_mutex_unlock(&mu)) return 125;
    for (int i = 0; i < count; i++) if (pthread_join(threads[i], NULL)) return 125;
    return 17;
}
`

func linuxEpochEngineNativeFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "engine.c")
	image := filepath.Join(directory, "engine")
	if err := os.WriteFile(source, []byte(linuxEpochEngineFixtureC), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/cc", "-std=c11", "-O0", "-Wall", "-Wextra", "-Werror", "-pthread", source, "-o", image)
	command.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	output, err := command.CombinedOutput()
	if err != nil || command.ProcessState == nil || command.ProcessState.ExitCode() != 0 {
		t.Fatal("native fixture compiler original Wait", err, string(output))
	}
	raw, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("own pthread fixture source sha256:%x image sha256:%x compiler PID=%d actual Wait=0; no production admission", sha256.Sum256([]byte(linuxEpochEngineFixtureC)), sha256.Sum256(raw), command.Process.Pid)
	return image
}

//go:build linux

package t421

import (
	"errors"
	"io"
	"strconv"
	"strings"
)

// A prework refusal keeps the same ErrExecutionLauncher outcome and adds one
// fixed stage token naming where it first refused. The token is a constant
// from this package, never caller, path or tool text, so the one-line stderr
// report stays bounded and carries no authority. Success never writes it.
type preworkRefusal struct {
	stage string
	inner string // outer only: the validated stage relayed from the inner child
	exit  int    // outer only: the inner child's actual exit code, -1 if none
}

func (refusal *preworkRefusal) Error() string { return ErrExecutionLauncher.Error() }

func (refusal *preworkRefusal) Is(target error) bool { return target == ErrExecutionLauncher }

func preworkStage(stage string) error { return &preworkRefusal{stage: stage, exit: -1} }

// firstPreworkRefusal returns the earliest recorded stage in errors.Join order.
func firstPreworkRefusal(err error) (*preworkRefusal, bool) {
	var refusal *preworkRefusal
	if errors.As(err, &refusal) {
		return refusal, true
	}
	return nil, false
}

const (
	preworkRefusalPrefix   = "t422-prework-refusal: "
	maxPreworkRefusalStage = 40
	maxPreworkRefusalLine  = 160
)

func validPreworkRefusalToken(token string) bool {
	if token == "" || len(token) > maxPreworkRefusalStage {
		return false
	}
	for _, character := range token {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func preworkRefusalLine(mode string, err error) string {
	if mode != executionLinuxPreworkOuterMode && mode != executionLinuxPreworkInnerMode {
		mode = "unknown"
	}
	stage, inner, exit := "unrecorded", "", -1
	if refusal, ok := firstPreworkRefusal(err); ok && validPreworkRefusalToken(refusal.stage) {
		stage, inner, exit = refusal.stage, refusal.inner, refusal.exit
	}
	line := preworkRefusalPrefix + "mode=" + preworkModeToken(mode) + " stage=" + stage
	if validPreworkRefusalToken(inner) {
		line += " inner=" + inner
	}
	if exit >= 0 && exit <= 255 {
		line += " inner-exit=" + strconv.Itoa(exit)
	}
	return line + "\n"
}

func preworkModeToken(mode string) string {
	switch mode {
	case executionLinuxPreworkOuterMode:
		return "outer"
	case executionLinuxPreworkInnerMode:
		return "inner"
	default:
		return "unknown"
	}
}

func writePreworkRefusal(stderr io.Writer, mode string, err error) {
	if err == nil || stderr == nil {
		return
	}
	line := preworkRefusalLine(mode, err)
	if len(line) <= maxPreworkRefusalLine {
		_, _ = io.WriteString(stderr, line)
	}
}

// relayedPreworkStage extracts the inner child's own stage from its bounded,
// already-captured stderr. Anything malformed relays nothing.
func relayedPreworkStage(stderr string) string {
	const want = preworkRefusalPrefix + "mode=inner stage="
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(line, want) || len(line) > maxPreworkRefusalLine {
			continue
		}
		token := strings.TrimPrefix(line, want)
		if index := strings.IndexByte(token, ' '); index >= 0 {
			token = token[:index]
		}
		if validPreworkRefusalToken(token) {
			return token
		}
		return ""
	}
	return ""
}

package provider

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

const (
	hostCommandExecAttempts   = 10
	hostCommandExecRetryDelay = 20 * time.Millisecond
)

// runCommandWithExecutableBusyRetry retries only failures that happen before
// the process starts because its executable is still open for writing.
func runCommandWithExecutableBusyRetry(ctx context.Context, build func() *exec.Cmd) error {
	var err error
	for attempt := 1; attempt <= hostCommandExecAttempts; attempt++ {
		err = build().Run()
		if err == nil || !isExecutableBusyError(err) || attempt == hostCommandExecAttempts {
			return err
		}

		timer := time.NewTimer(hostCommandExecRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func combinedOutputWithExecutableBusyRetry(ctx context.Context, build func() *exec.Cmd) ([]byte, error) {
	var output bytes.Buffer
	err := runCommandWithExecutableBusyRetry(ctx, func() *exec.Cmd {
		output.Reset()
		cmd := build()
		cmd.Stdout = &output
		cmd.Stderr = &output
		return cmd
	})
	return append([]byte(nil), output.Bytes()...), err
}

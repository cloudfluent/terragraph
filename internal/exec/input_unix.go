//go:build !windows

package exec

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// prepareCommandInput keeps terminal reads in the CLI foreground group and stops the copy when the runtime group exits, even if stdin remains open.
func prepareCommandInput(ctx context.Context, cmd *exec.Cmd) (context.CancelFunc, bool) {
	input, ok := cmd.Stdin.(*os.File)
	if !ok {
		return func() {}, false
	}
	if _, err := unix.IoctlGetInt(int(input.Fd()), unix.TIOCGPGRP); err != nil {
		return func() {}, false
	}
	inputContext, stop := context.WithCancel(ctx)
	cmd.Stdin = &terminalReader{ctx: inputContext, file: input}
	return stop, true
}

type terminalReader struct {
	ctx  context.Context
	file *os.File
}

func (r *terminalReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if r.ctx.Err() != nil {
			return 0, io.EOF
		}
		fds := []unix.PollFd{{Fd: int32(r.file.Fd()), Events: unix.POLLIN}}
		ready, err := unix.Poll(fds, 50)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if ready > 0 {
			return r.file.Read(p)
		}
	}
}

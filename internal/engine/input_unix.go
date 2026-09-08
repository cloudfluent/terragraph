//go:build !windows

package engine

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"syscall"
)

// approvalInput bounds parent-owned approval reads while keeping the original file available for direct subprocess stdin inheritance.
func approvalInput(ctx context.Context, input io.Reader) io.Reader {
	file, ok := input.(*os.File)
	if !ok || ctx.Done() == nil {
		return input
	}
	return &interruptibleInput{ctx: ctx, file: file}
}

type interruptibleInput struct {
	ctx  context.Context
	file *os.File
}

func (r *interruptibleInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
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

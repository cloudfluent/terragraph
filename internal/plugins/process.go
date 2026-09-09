package plugins

import (
	"context"
	runtimeexec "github.com/cloudfluent/terragraph/internal/exec"
	"io"
	"os/exec"
	"strconv"
	"sync"

	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin/runner"
)

// processRunner owns descendants as well as the RPC server, including helpers left behind when the server exits normally.
type processRunner struct {
	cmd     *exec.Cmd
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	cleanup func()
	once    sync.Once
}

func newProcessRunner(_ hclog.Logger, cmd *exec.Cmd, _ string) (runner.Runner, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return &processRunner{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

func (p *processRunner) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cleanup, err := runtimeexec.StartManagedProcess(p.cmd)
	if err != nil {
		return err
	}
	p.cleanup = cleanup
	return nil
}

func (p *processRunner) stop() {
	p.once.Do(func() {
		if p.cleanup != nil {
			p.cleanup()
		}
	})
}
func (p *processRunner) Wait(context.Context) error { err := p.cmd.Wait(); p.stop(); return err }
func (p *processRunner) Kill(context.Context) error { p.stop(); return nil }
func (p *processRunner) Diagnose(context.Context) string {
	return "plugin process failed; verify its platform and protocol"
}
func (p *processRunner) Stdout() io.ReadCloser { return p.stdout }
func (p *processRunner) Stderr() io.ReadCloser { return p.stderr }
func (p *processRunner) Name() string          { return p.cmd.Path }
func (p *processRunner) ID() string {
	if p.cmd.Process == nil {
		return ""
	}
	return strconv.Itoa(p.cmd.Process.Pid)
}
func (p *processRunner) PluginToHost(network, address string) (string, string, error) {
	return network, address, nil
}
func (p *processRunner) HostToPlugin(network, address string) (string, string, error) {
	return network, address, nil
}

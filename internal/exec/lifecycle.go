package exec

import (
	"context"
	"errors"
	"os"
	osexec "os/exec"
	"strings"
)

// ErrNotStarted lets the journal distinguish admission rejection from an uncertain mutation.
var ErrNotStarted = errors.New("runtime was not started")

// RuntimeHook supplies per-command credentials and cancellation without giving the hook a mutable subprocess or command arguments.
type RuntimeHook func(context.Context, string) (context.Context, map[string]string, func(error) error, error)

func (r *Runner) execute(cmd *osexec.Cmd) (resultErr error) {
	ctx := r.Context
	if r.Hook != nil {
		operation := ""
		if len(cmd.Args) > 1 {
			operation = cmd.Args[1]
		}
		if operation == "state" && len(cmd.Args) > 2 {
			operation += " " + cmd.Args[2]
		}
		next, env, finish, err := r.Hook(ctx, operation)
		if err != nil {
			return errors.Join(ErrNotStarted, err)
		}
		ctx = next
		if len(env) > 0 && cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		for key, value := range env {
			filtered := cmd.Env[:0]
			for _, entry := range cmd.Env {
				name, _, _ := strings.Cut(entry, "=")
				if !strings.EqualFold(name, key) {
					filtered = append(filtered, entry)
				}
			}
			cmd.Env = append(filtered, key+"="+value)
		}
		if finish != nil {
			defer func() { resultErr = errors.Join(resultErr, finish(resultErr)) }()
		}
	}
	return runCommand(ctx, cmd)
}

// PlanDocument exposes read-only plan evidence; callers must authorize any delivery because native JSON can contain secret values.
func (r *Runner) PlanDocument(path string) ([]byte, error) { return r.planJSON(path) }

type credentialLifetimeKey struct{}

// WithCredentialLifetime opts credential-dependent Windows commands into cancellable child containment without changing native console defaults.
func WithCredentialLifetime(ctx context.Context) context.Context {
	return context.WithValue(ctx, credentialLifetimeKey{}, true)
}

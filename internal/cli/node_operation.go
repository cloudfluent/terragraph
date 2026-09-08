package cli

import (
	"fmt"
	"log/slog"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/spf13/cobra"
)

func newNodeOperationCmd(path *string, binaryOf func() exec.Binary, loggerOf func() *slog.Logger) *cobra.Command {
	var node string
	cmd := &cobra.Command{Use: "run --node <leaf> -- <native-command> [arguments]", Short: "Run a supported native operation in one node's runtime and backend context", RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.ArgsLenAtDash() != 0 {
			return fmt.Errorf("separate native command arguments with -- after --node")
		}
		e, close, err := engine.OpenNodeOperation(cmd.Context(), *path, binaryOf(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer close()
		wireEngine(cmd, e, loggerOf, *path)
		return e.RunNode(node, args)
	}}
	cmd.Flags().StringVar(&node, "node", "", "one exact expanded leaf; groups are not expanded")
	return cmd
}

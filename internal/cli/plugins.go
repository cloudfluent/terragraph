package cli

import (
	"fmt"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/plugins"
	"github.com/cloudfluent/terragraph/internal/runlock"
	"github.com/spf13/cobra"
)

func newPluginCmd(path *string) *cobra.Command {
	root := &cobra.Command{Use: "plugin", Short: "Install and inspect version-locked executable plugins"}
	var locked bool
	install := &cobra.Command{Use: "install ALIAS PACKAGE_DIRECTORY", Short: "Install a local release package and pin its checksum for this platform", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		configs, dir, err := blueprint.LoadPlugins(*path)
		if err != nil {
			return err
		}
		for _, config := range configs {
			if config.Name != args[0] {
				continue
			}
			lock, err := runlock.AcquireContext(cmd.Context(), dir, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer func() { _ = lock.Close() }()
			if err := plugins.Install(dir, config, args[1], locked); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "installed %s (%s)\n", config.Name, plugins.Platform())
			return err
		}
		return fmt.Errorf("plugin.%s: no declaration; add a plugin block to the blueprint", args[0])
	}}
	install.Flags().BoolVar(&locked, "locked", false, "require the package to match the existing lock without changing it")
	root.AddCommand(install)
	root.AddCommand(&cobra.Command{Use: "inspect PACKAGE_DIRECTORY", Short: "Read package metadata and checksums without executing plugin code", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := plugins.Inspect(args[0])
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\nprotocol: %d\nchecksum: %s\n", p.Descriptor.Name, p.Descriptor.Version, p.Descriptor.Protocol, p.Digest); err != nil {
			return err
		}
		for _, feature := range p.Descriptor.Features {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", feature.Name, feature.Kind, feature.Effect); err != nil {
				return err
			}
		}
		return nil
	}})
	root.AddCommand(&cobra.Command{Use: "list", Short: "Verify and list locked plugins for this platform without executing them", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		configs, dir, err := blueprint.LoadPlugins(*path)
		if err != nil {
			return err
		}
		for _, config := range configs {
			p, err := plugins.Resolve(dir, config)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %s\n", config.Name, config.Source, p.Descriptor.Version, plugins.Platform()); err != nil {
				return err
			}
		}
		return nil
	}})
	var stopped, acknowledge bool
	recoverCmd := &cobra.Command{Use: "recover EXECUTION_ID CALL_ID", Short: "Retry a recorded idempotent plugin effect or acknowledge its externally reviewed outcome", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		e, close, err := engine.OpenExecutionHistory(cmd.Context(), *path, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer close()
		if !acknowledge {
			configs, _, err := blueprint.LoadPlugins(*path)
			if err != nil {
				return err
			}
			e.Blueprint.Plugins = configs
		}
		record, err := e.RecoverPluginCall(args[0], args[1], stopped, acknowledge)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", record.ID, record.Status)
		return err
	}}
	recoverCmd.Flags().BoolVar(&stopped, "confirm-stopped", false, "confirm the previous executor has stopped")
	recoverCmd.Flags().BoolVar(&acknowledge, "acknowledge-external-state", false, "record that the external outcome was inspected and resolved without replaying it")
	root.AddCommand(recoverCmd)
	root.AddCommand(&cobra.Command{Use: "report EXECUTION_ID CALL_ID", Short: "Write a plugin-authored report to stdout; may contain sensitive plan evidence", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		e, close, err := engine.OpenExecutionHistory(cmd.Context(), *path, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
		defer close()
		return e.ReadPluginReport(args[0], args[1], cmd.OutOrStdout())
	}})
	return root
}

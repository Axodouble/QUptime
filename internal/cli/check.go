package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"git.cer.sh/axodouble/quptime/internal/config"
	"git.cer.sh/axodouble/quptime/internal/daemon"
	"git.cer.sh/axodouble/quptime/internal/transport"
)

func addCheckCmd(root *cobra.Command) {
	check := &cobra.Command{
		Use:   "check",
		Short: "Manage configured checks",
	}

	addHTTP := buildAddCheckCmd(config.CheckHTTP, "http", "<name> <url>",
		"Add an HTTP/HTTPS check",
		func(args []string, c *config.Check) error {
			c.Name = args[0]
			c.Target = args[1]
			return nil
		})
	addHTTP.Flags().Int("expect", 200, "HTTP status code that signals UP")
	addHTTP.Flags().String("body-match", "", "substring required in response body for UP")

	addTCP := buildAddCheckCmd(config.CheckTCP, "tcp", "<name> <host:port>",
		"Add a TCP-connect check",
		func(args []string, c *config.Check) error {
			c.Name = args[0]
			c.Target = args[1]
			return nil
		})

	addICMP := buildAddCheckCmd(config.CheckICMP, "icmp", "<name> <host>",
		"Add an ICMP ping check",
		func(args []string, c *config.Check) error {
			c.Name = args[0]
			c.Target = args[1]
			return nil
		})

	addParent := &cobra.Command{
		Use:   "add",
		Short: "Add a new check",
	}
	addParent.AddCommand(addHTTP, addTCP, addICMP)

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List configured checks and their current aggregate state",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			return runStatusPrintChecks(ctx, cmd)
		},
	}

	removeCmd := &cobra.Command{
		Use:   "remove <id-or-name>",
		Short: "Remove a configured check",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			body := daemon.MutateBody{Kind: transport.MutationRemoveCheck}
			payload, _ := json.Marshal(args[0])
			body.Payload = payload
			raw, err := callDaemon(ctx, daemon.CtrlMutate, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			_ = json.Unmarshal(raw, &res)
			fmt.Fprintf(cmd.OutOrStdout(), "removed check %s (cluster version now %d)\n", args[0], res.Version)
			return nil
		},
	}

	check.AddCommand(addParent, listCmd, removeCmd, buildCheckEditCmd())
	root.AddCommand(check)
}

// buildCheckEditCmd returns `qu check edit`, which updates fields of an
// existing check in place. Only flags that the operator actually passes
// modify the corresponding field — everything else is preserved from the
// existing record, including the ID. Identity match is by ID or Name.
func buildCheckEditCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "edit <id-or-name>",
		Short: "Update fields of an existing check",
		Long: `Update one or more fields of an existing check.

Identifies the target by ID or Name. Only flags you pass take effect;
all other fields are preserved from the existing record. HTTP-only flags
(--expect, --body-match) error out on non-HTTP checks.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			cluster, err := config.LoadClusterConfig()
			if err != nil {
				return err
			}
			snap := cluster.Snapshot()
			var existing *config.Check
			for i := range snap.Checks {
				if snap.Checks[i].ID == args[0] || snap.Checks[i].Name == args[0] {
					cp := snap.Checks[i]
					existing = &cp
					break
				}
			}
			if existing == nil {
				return fmt.Errorf("no check named %q", args[0])
			}

			f := cmd.Flags()
			if f.Changed("name") {
				v, _ := f.GetString("name")
				existing.Name = strings.TrimSpace(v)
			}
			if f.Changed("target") {
				v, _ := f.GetString("target")
				existing.Target = strings.TrimSpace(v)
			}
			if f.Changed("interval") {
				s, _ := f.GetString("interval")
				d, err := time.ParseDuration(s)
				if err != nil {
					return fmt.Errorf("--interval: %w", err)
				}
				existing.Interval = d
			}
			if f.Changed("timeout") {
				s, _ := f.GetString("timeout")
				d, err := time.ParseDuration(s)
				if err != nil {
					return fmt.Errorf("--timeout: %w", err)
				}
				existing.Timeout = d
			}
			if f.Changed("alerts") {
				csv, _ := f.GetString("alerts")
				existing.AlertIDs = nil
				for _, p := range strings.Split(csv, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						existing.AlertIDs = append(existing.AlertIDs, p)
					}
				}
			}
			if f.Changed("expect") {
				if existing.Type != config.CheckHTTP {
					return fmt.Errorf("--expect only applies to HTTP checks (this is %s)", existing.Type)
				}
				v, _ := f.GetInt("expect")
				existing.ExpectStatus = v
			}
			if f.Changed("body-match") {
				if existing.Type != config.CheckHTTP {
					return fmt.Errorf("--body-match only applies to HTTP checks (this is %s)", existing.Type)
				}
				v, _ := f.GetString("body-match")
				existing.BodyMatch = v
			}

			payload, err := json.Marshal(existing)
			if err != nil {
				return err
			}
			body := daemon.MutateBody{Kind: transport.MutationAddCheck, Payload: payload}
			raw, err := callDaemon(ctx, daemon.CtrlMutate, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			_ = json.Unmarshal(raw, &res)
			fmt.Fprintf(cmd.OutOrStdout(), "updated check %s (cluster version now %d)\n", existing.Name, res.Version)
			return nil
		},
	}
	cmd.Flags().String("name", "", "rename the check")
	cmd.Flags().String("target", "", "new probe target (URL, host:port, or host)")
	cmd.Flags().String("interval", "", "new probe interval (e.g. 30s, 1m)")
	cmd.Flags().String("timeout", "", "new per-probe timeout (e.g. 10s)")
	cmd.Flags().String("alerts", "", "replace alert list with this CSV of IDs/names (pass empty to clear)")
	cmd.Flags().Int("expect", 0, "expected HTTP status code (HTTP only)")
	cmd.Flags().String("body-match", "", "substring required in body (HTTP only)")
	return cmd
}

// buildAddCheckCmd produces the per-type "qu check add <type>" subcommand.
func buildAddCheckCmd(ctype config.CheckType, use, argSpec, short string,
	bind func(args []string, c *config.Check) error,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   use + " " + argSpec,
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			ch := config.Check{
				ID:   uuid.NewString(),
				Type: ctype,
			}
			if err := bind(args, &ch); err != nil {
				return err
			}
			intervalStr, _ := cmd.Flags().GetString("interval")
			timeoutStr, _ := cmd.Flags().GetString("timeout")
			alertsCSV, _ := cmd.Flags().GetString("alerts")
			if intervalStr != "" {
				d, err := time.ParseDuration(intervalStr)
				if err != nil {
					return fmt.Errorf("--interval: %w", err)
				}
				ch.Interval = d
			} else {
				ch.Interval = 30 * time.Second
			}
			if timeoutStr != "" {
				d, err := time.ParseDuration(timeoutStr)
				if err != nil {
					return fmt.Errorf("--timeout: %w", err)
				}
				ch.Timeout = d
			} else {
				ch.Timeout = 10 * time.Second
			}
			if alertsCSV != "" {
				for _, p := range strings.Split(alertsCSV, ",") {
					p = strings.TrimSpace(p)
					if p != "" {
						ch.AlertIDs = append(ch.AlertIDs, p)
					}
				}
			}
			if ctype == config.CheckHTTP {
				es, _ := cmd.Flags().GetInt("expect")
				bm, _ := cmd.Flags().GetString("body-match")
				ch.ExpectStatus = es
				ch.BodyMatch = bm
			}

			payload, err := json.Marshal(ch)
			if err != nil {
				return err
			}
			body := daemon.MutateBody{Kind: transport.MutationAddCheck, Payload: payload}
			raw, err := callDaemon(ctx, daemon.CtrlMutate, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			_ = json.Unmarshal(raw, &res)
			fmt.Fprintf(cmd.OutOrStdout(), "added check %s (%s) id=%s — cluster version %d\n",
				ch.Name, ch.Type, ch.ID, res.Version)
			return nil
		},
	}
	bindCheckFlags(cmd)
	return cmd
}

func bindCheckFlags(cmd *cobra.Command) {
	cmd.Flags().String("interval", "30s", "probe interval")
	cmd.Flags().String("timeout", "10s", "per-probe timeout")
	cmd.Flags().String("alerts", "", "comma-separated alert IDs/names to notify on transition")
}

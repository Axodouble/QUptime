package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/jasper/quptime/internal/config"
	"github.com/jasper/quptime/internal/daemon"
	"github.com/jasper/quptime/internal/transport"
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
	bindHTTPFlags(addHTTP)

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

	check.AddCommand(addParent, listCmd, removeCmd)
	root.AddCommand(check)
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

// bindHTTPFlags is a no-op kept to mirror the per-type flag bind sites
// so the caller can extend cleanly later.
func bindHTTPFlags(cmd *cobra.Command) {}

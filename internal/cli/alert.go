package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/jasper/quptime/internal/config"
	"github.com/jasper/quptime/internal/daemon"
	"github.com/jasper/quptime/internal/transport"
)

func addAlertCmd(root *cobra.Command) {
	alert := &cobra.Command{
		Use:   "alert",
		Short: "Manage notification channels",
	}

	addParent := &cobra.Command{
		Use:   "add",
		Short: "Add a new alert channel",
	}
	addParent.AddCommand(buildSMTPAddCmd(), buildDiscordAddCmd())

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List configured alerts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cluster, err := config.LoadClusterConfig()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tTYPE\tNAME")
			for _, a := range cluster.Alerts {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", a.ID, a.Type, a.Name)
			}
			return tw.Flush()
		},
	}

	removeCmd := &cobra.Command{
		Use:   "remove <id-or-name>",
		Short: "Remove an alert channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			payload, _ := json.Marshal(args[0])
			body := daemon.MutateBody{Kind: transport.MutationRemoveAlert, Payload: payload}
			raw, err := callDaemon(ctx, daemon.CtrlMutate, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			_ = json.Unmarshal(raw, &res)
			fmt.Fprintf(cmd.OutOrStdout(), "removed alert %s (cluster version now %d)\n", args[0], res.Version)
			return nil
		},
	}

	testCmd := &cobra.Command{
		Use:   "test <id-or-name>",
		Short: "Send a test notification through an alert channel",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			body := daemon.AlertTestBody{AlertID: args[0]}
			if _, err := callDaemon(ctx, daemon.CtrlAlertTest, body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "test alert sent via %s\n", args[0])
			return nil
		},
	}

	alert.AddCommand(addParent, listCmd, removeCmd, testCmd)
	root.AddCommand(alert)
}

func buildSMTPAddCmd() *cobra.Command {
	var host, user, password, from string
	var port int
	var to []string
	var startTLS bool

	cmd := &cobra.Command{
		Use:   "smtp <name>",
		Short: "Add an SMTP relay alert",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			a := config.Alert{
				ID:           uuid.NewString(),
				Name:         args[0],
				Type:         config.AlertSMTP,
				SMTPHost:     host,
				SMTPPort:     port,
				SMTPUser:     user,
				SMTPPassword: password,
				SMTPFrom:     from,
				SMTPTo:       to,
				SMTPStartTLS: startTLS,
			}
			payload, _ := json.Marshal(a)
			body := daemon.MutateBody{Kind: transport.MutationAddAlert, Payload: payload}
			raw, err := callDaemon(ctx, daemon.CtrlMutate, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			_ = json.Unmarshal(raw, &res)
			fmt.Fprintf(cmd.OutOrStdout(), "added smtp alert %s id=%s — cluster version %d\n",
				a.Name, a.ID, res.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "smtp server host")
	cmd.Flags().IntVar(&port, "port", 587, "smtp server port")
	cmd.Flags().StringVar(&user, "user", "", "smtp auth user (empty for anonymous)")
	cmd.Flags().StringVar(&password, "password", "", "smtp auth password")
	cmd.Flags().StringVar(&from, "from", "", "envelope From address")
	cmd.Flags().StringSliceVar(&to, "to", nil, "recipient address (repeat or comma-separate)")
	cmd.Flags().BoolVar(&startTLS, "starttls", true, "negotiate STARTTLS")
	_ = cmd.MarkFlagRequired("host")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func buildDiscordAddCmd() *cobra.Command {
	var webhook string
	cmd := &cobra.Command{
		Use:   "discord <name>",
		Short: "Add a Discord webhook alert",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			a := config.Alert{
				ID:             uuid.NewString(),
				Name:           args[0],
				Type:           config.AlertDiscord,
				DiscordWebhook: webhook,
			}
			payload, _ := json.Marshal(a)
			body := daemon.MutateBody{Kind: transport.MutationAddAlert, Payload: payload}
			raw, err := callDaemon(ctx, daemon.CtrlMutate, body)
			if err != nil {
				return err
			}
			var res daemon.MutateResult
			_ = json.Unmarshal(raw, &res)
			fmt.Fprintf(cmd.OutOrStdout(), "added discord alert %s id=%s — cluster version %d\n",
				a.Name, a.ID, res.Version)
			return nil
		},
	}
	cmd.Flags().StringVar(&webhook, "webhook", "", "discord webhook URL")
	_ = cmd.MarkFlagRequired("webhook")
	return cmd
}

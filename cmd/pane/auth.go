package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/michaelquigley/pane/internal/auth"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newAuthCommand()) }

func newAuthCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "manage pane's openai subscription login", Args: cobra.NoArgs}
	var method string
	login := &cobra.Command{Use: "login openai", Short: "sign in to openai for pane", Args: openaiArg, RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := auth.OpenGlobalStore()
		if err != nil {
			return err
		}
		manager := auth.NewManager(store)
		show := func(message string) { fmt.Fprintln(cmd.OutOrStdout(), message) }
		switch method {
		case "browser":
			err = manager.LoginBrowser(cmd.Context(), show)
		case "device":
			err = manager.LoginDevice(cmd.Context(), show)
		default:
			return fmt.Errorf("unknown login method '%s'", method)
		}
		if err != nil {
			return err
		}
		_, err = io.WriteString(cmd.OutOrStdout(), "signed in to openai for pane\n")
		return err
	}}
	login.Flags().StringVar(&method, "method", "browser", "login method: browser or device")
	status := &cobra.Command{Use: "status openai", Short: "show local openai login readiness", Args: openaiArg, RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := auth.OpenGlobalStore()
		if err != nil {
			return err
		}
		state, err := auth.NewManager(store).Status()
		if err != nil {
			return err
		}
		message := "openai: login required\n"
		if state.SignedIn {
			message = "openai: signed in\n"
		}
		if state.RefreshNeeded {
			message = "openai: signed in; token refresh needed before use\n"
		}
		_, err = io.WriteString(cmd.OutOrStdout(), message)
		return err
	}}
	logout := &cobra.Command{Use: "logout openai", Short: "remove pane's local openai login", Args: openaiArg, RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := auth.OpenGlobalStore()
		if err != nil {
			return err
		}
		if err := auth.NewManager(store).Logout(cmd.Context()); err != nil {
			return err
		}
		_, err = io.WriteString(cmd.OutOrStdout(), "removed pane's local openai login\n")
		return err
	}}
	command.AddCommand(login, status, logout)
	return command
}

func openaiArg(_ *cobra.Command, args []string) error {
	if len(args) != 1 || strings.ToLower(args[0]) != "openai" || args[0] != "openai" {
		return fmt.Errorf("expected provider 'openai'")
	}
	return nil
}

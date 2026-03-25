package main

import (
	"fmt"
	"runtime"

	"github.com/michaelquigley/pane/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "show version information",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("pane %s (%s/%s)\n", config.Version, runtime.GOOS, runtime.GOARCH)
		},
	})
}

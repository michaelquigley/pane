package main

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/api"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/ui"
	"github.com/spf13/cobra"
)

var verbose bool
var configPath string

var rootCmd = &cobra.Command{
	Use:   strings.TrimSuffix(filepath.Base(os.Args[0]), filepath.Ext(os.Args[0])),
	Short: "pane - a thin pane of glass between a human and an LLM",
	Run:   run,
}

func init() {
	dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/michaelquigley/"))
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose output")
	rootCmd.Flags().StringVar(&configPath, "config", "", "config file path")
	rootCmd.PersistentPreRun = func(_ *cobra.Command, _ []string) {
		if verbose {
			dl.Init(dl.DefaultOptions().SetTrimPrefix("github.com/michaelquigley/").SetLevel(slog.LevelDebug))
		}
	}
}

func run(_ *cobra.Command, _ []string) {
	cfg, err := config.Load(configPath)
	if err != nil {
		dl.Fatalf("loading config: %v", err)
	}

	dl.Debugf("config: endpoint=%s model=%s listen=%s", cfg.Endpoint, cfg.Model, cfg.Listen)

	llmClient := llm.NewClient(cfg.Endpoint, cfg.Model)
	a := api.NewAPI(cfg, llmClient)

	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	handler := ui.Middleware(mux)

	dl.Infof("listening on %s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, handler); err != nil {
		dl.Fatalf("server: %v", err)
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

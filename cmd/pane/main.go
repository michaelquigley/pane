package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/michaelquigley/pane/internal/api"
	"github.com/michaelquigley/pane/internal/config"
	"github.com/michaelquigley/pane/internal/llm"
	"github.com/michaelquigley/pane/internal/mcp"
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	mcpMgr := mcp.NewManager(cfg.MCP)
	mcpMgr.Start(ctx)

	llmClient := llm.NewClient(cfg.Endpoint, cfg.Model)
	a := api.NewAPI(cfg, llmClient, mcpMgr)

	mux := http.NewServeMux()
	a.RegisterRoutes(mux)

	handler := ui.Middleware(mux)

	server := &http.Server{
		Addr:    cfg.Listen,
		Handler: handler,
	}

	go func() {
		<-sigCh
		dl.Infof("shutting down...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			dl.Errorf("server shutdown: %v", err)
		}
	}()

	dl.Infof("listening on http://%s", cfg.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		dl.Fatalf("server: %v", err)
	}

	mcpMgr.Stop()
	dl.Infof("stopped")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

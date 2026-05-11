package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mvd-analyzer/qwanalytics/internal/democache"
	"github.com/mvd-analyzer/qwanalytics/internal/hubfetch"
)

const mcpServerName = "qw-mvd"

// runMCP starts a stdio MCP server. Two modes:
//
//   - local (no -api): import democache + view directly, parse demos
//     on first access, cache on disk under -cache-dir.
//   - proxy (-api URL): forward every tool call to a remote `qw-mvd
//     serve`. Implemented in Step 5.
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	var (
		apiURL    = fs.String("api", "", "if set, proxy every tool call to this `qw-mvd serve` URL instead of querying locally")
		cacheDir  = fs.String("cache-dir", democache.DefaultRoot(), "local mode: on-disk cache root")
		label     = fs.String("label", "", "non-secret request-source label forwarded as Authorization: Bearer <label>")
		timeoutS  = fs.Int("timeout", 60, "proxy mode: per-request HTTP timeout in seconds")
		_         = label
		_         = timeoutS
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	var backend MCPBackend
	if *apiURL != "" {
		backend = newProxyBackend(*apiURL, *label, time.Duration(*timeoutS)*time.Second)
	} else {
		cache := democache.New(*cacheDir, hubfetch.NewClient())
		backend = &localBackend{store: cache}
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("qw-mvd mcp starting", "mode", mcpMode(*apiURL), "cacheDir", *cacheDir)

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    mcpServerName,
		Title:   "QuakeWorld MVD analytics",
		Version: GitTag,
	}, nil)
	registerTools(srv, backend)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("mcp shutting down", "signal", sig.String())
		cancel()
	}()

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		// stdin EOF / ctx cancel = clean shutdown the way every MCP
		// client signals "I'm done." The SDK wraps EOF in its own
		// "server is closing" error string; match on that as well.
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
			strings.Contains(err.Error(), "server is closing") {
			return nil
		}
		return fmt.Errorf("mcp run: %w", err)
	}
	return nil
}

func mcpMode(apiURL string) string {
	if apiURL != "" {
		return "proxy"
	}
	return "local"
}

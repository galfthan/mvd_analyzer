// mvd-mcp is a stdio MCP server that forwards every tool call to a
// running mvd-api. The shim is intentionally minimal: it does not
// import any qwanalytics code, so the distributable binary is small
// (~5 MB) and stable against analytics-side changes.
//
// Usage:
//
//	mvd-mcp -api URL [-label TAG] [-timeout SECONDS]
//	mvd-mcp version
//
// Flags:
//
//	-api      required: base URL of a running mvd-api (e.g. https://qw-mvd.example.com)
//	-label    optional non-secret request-source tag, forwarded as Authorization: Bearer <label>
//	-timeout  per-request HTTP timeout in seconds (default 60)
//
// For local MCP, run mvd-api on localhost and point -api at it:
//
//	mvd-api -addr :8080 &
//	mvd-mcp -api http://localhost:8080
package main

import (
	"context"
	"encoding/json"
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
)

const serverName = "mvd-mcp"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{
			"hash":      GitHash,
			"tag":       GitTag,
			"buildDate": BuildDate,
		})
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "mvd-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("mvd-mcp", flag.ContinueOnError)
	apiURL := fs.String("api", "", "required: mvd-api base URL (e.g. http://localhost:8080)")
	label := fs.String("label", "", "non-secret request-source label, forwarded as Authorization: Bearer <label>")
	timeoutS := fs.Int("timeout", 60, "per-request HTTP timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *apiURL == "" {
		fs.Usage()
		return errors.New("-api URL is required")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("mvd-mcp starting", "api", *apiURL, "label", *label)

	backend := newProxyBackend(*apiURL, *label, time.Duration(*timeoutS)*time.Second)
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Title:   "QuakeWorld MVD analytics (proxy to mvd-api)",
		Version: GitTag,
	}, nil)
	registerTools(srv, backend)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("mvd-mcp shutting down", "signal", sig.String())
		cancel()
	}()

	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		// stdin EOF / ctx cancel = clean shutdown the way MCP clients
		// signal "I'm done." The SDK wraps EOF in its own
		// "server is closing" error string; match on that too.
		if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
			strings.Contains(err.Error(), "server is closing") {
			return nil
		}
		return fmt.Errorf("mcp run: %w", err)
	}
	return nil
}

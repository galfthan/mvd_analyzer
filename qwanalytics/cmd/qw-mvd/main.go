// qw-mvd is the network-facing binary for the mvd-analyzer pipeline.
// It exposes the analytics view surface (qwanalytics/view) over HTTP
// REST and stdio MCP, backed by a two-tier disk cache that fetches
// demos from hub.quakeworld.nu on demand.
//
// Subcommands:
//
//	qw-mvd serve          # HTTP REST API on the qwanalytics view surface
//	qw-mvd mcp            # MCP over stdio, local mode (imports view directly)
//	qw-mvd mcp -api URL   # MCP over stdio, proxying tool calls to a remote serve
//	qw-mvd version        # build info
//	qw-mvd help           # this message
//
// The REST API and the MCP tool surface mirror each other 1:1. The
// distributable stdio binary (qw-mvd mcp -api URL) lets non-Go MCP
// clients (Claude Desktop, Cursor, etc.) reach a hosted serve without
// importing Go.
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

const usage = `qw-mvd — network-facing analytics for QuakeWorld demos.

Usage:
  qw-mvd <command> [options]

Commands:
  serve     Start the HTTP REST server.
  mcp       Start a stdio MCP server. With -api URL, proxy tool calls to a remote serve.
  version   Print build info as JSON.
  help      Print this message.

Run 'qw-mvd <command> -h' for command-specific options.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "serve":
		if err := runServe(args); err != nil {
			fmt.Fprintf(os.Stderr, "qw-mvd serve: %v\n", err)
			os.Exit(1)
		}
	case "mcp":
		if err := runMCP(args); err != nil {
			fmt.Fprintf(os.Stderr, "qw-mvd mcp: %v\n", err)
			os.Exit(1)
		}
	case "version":
		printVersion()
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "qw-mvd: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func printVersion() {
	info := map[string]string{
		"hash":      GitHash,
		"tag":       GitTag,
		"buildDate": BuildDate,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(info)
}

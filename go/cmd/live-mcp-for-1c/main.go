// Command live-mcp-for-1c runs the standalone AIDataRead MCP server over stdio.
//
// It exposes the six contract tools and speaks MCP on stdout while writing
// diagnostics to stderr. Connection settings come from the process
// environment. It is a stdio child of an MCP client, not a network daemon.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"live-mcp-for-1c/internal/config"
	mcpserver "live-mcp-for-1c/internal/mcp"
)

const version = "0.2.0"

func main() {
	settings, err := config.Load(os.Environ())
	if err != nil {
		fmt.Fprintf(
			os.Stderr,
			"%s: configuration error: %v\n",
			mcpserver.ServerName,
			err,
		)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	server := mcpserver.New(settings, version)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintf(
			os.Stderr,
			"%s: fatal error: %v\n",
			mcpserver.ServerName,
			err,
		)
		os.Exit(1)
	}
}

package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/minimal-realtime-assistant/realtime"
)

// Bridge owns the live MCP client sessions and the realtime tools they expose.
// Close it when the assistant shuts down to terminate the server subprocesses.
type Bridge struct {
	sessions []*sdk.ClientSession
	tools    []realtime.Tool
}

// Tools returns the realtime tools discovered across all connected servers, in a
// deterministic order (servers in the order connect received them, tools in the
// order each server listed them).
func (b *Bridge) Tools() []realtime.Tool { return b.tools }

// Close shuts every MCP session down (stopping its subprocess). It attempts all
// closes and returns the first error, if any.
func (b *Bridge) Close() error {
	var first error
	for _, s := range b.sessions {
		if err := s.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// namedTransport pairs a server name (for error messages and ordering) with the
// transport to dial it.
type namedTransport struct {
	name      string
	transport sdk.Transport
}

// connect dials each transport in order, lists its tools (paging through the
// cursor), and wraps them as realtime tools. Any failure — a dial error, a list
// error, or a tool name already seen on another server — tears down every
// session opened so far and returns an error (fail fast).
func connect(ctx context.Context, servers []namedTransport) (*Bridge, error) {
	client := sdk.NewClient(&sdk.Implementation{Name: "minimal-realtime-assistant", Version: "0.1.0"}, nil)
	b := &Bridge{}
	seen := map[string]string{} // tool name -> server it came from

	for _, srv := range servers {
		session, err := client.Connect(ctx, srv.transport, nil)
		if err != nil {
			b.Close()
			return nil, fmt.Errorf("connect mcp server %q: %w", srv.name, err)
		}
		b.sessions = append(b.sessions, session)

		cursor := ""
		for {
			res, err := session.ListTools(ctx, &sdk.ListToolsParams{Cursor: cursor})
			if err != nil {
				b.Close()
				return nil, fmt.Errorf("list tools from mcp server %q: %w", srv.name, err)
			}
			for _, tl := range res.Tools {
				if from, dup := seen[tl.Name]; dup {
					b.Close()
					return nil, fmt.Errorf("duplicate tool name %q from mcp servers %q and %q", tl.Name, from, srv.name)
				}
				seen[tl.Name] = srv.name
				b.tools = append(b.tools, &bridgedTool{
					session:     session,
					name:        tl.Name,
					description: tl.Description,
					schema:      tl.InputSchema,
				})
			}
			if res.NextCursor == "" {
				break
			}
			cursor = res.NextCursor
		}
	}
	return b, nil
}

// Connect starts every configured MCP server as a stdio subprocess, discovers
// its tools, and returns a Bridge exposing them as realtime tools. Servers are
// dialed in sorted-name order so tool ordering is deterministic across runs. Any
// failure aborts and tears down already-started servers (fail fast). Call
// Bridge.Close to stop the subprocesses.
func Connect(ctx context.Context, cfg Config) (*Bridge, error) {
	names := make([]string, 0, len(cfg.MCPServers))
	for name := range cfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]namedTransport, 0, len(names))
	for _, name := range names {
		spec := cfg.MCPServers[name]
		cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
		cmd.Env = os.Environ()
		for k, v := range spec.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
		servers = append(servers, namedTransport{
			name:      name,
			transport: &sdk.CommandTransport{Command: cmd},
		})
	}
	return connect(ctx, servers)
}

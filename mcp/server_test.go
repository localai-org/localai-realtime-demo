package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// echoArgs is the input schema for the test echo tools.
type echoArgs struct {
	Text string `json:"text,omitempty" jsonschema:"text to echo back"`
}

// echoOut is an empty structured-output type (the test tools return their result
// as explicit text Content; Out is required by the generic AddTool signature).
type echoOut struct{}

// startEchoServer runs an in-memory MCP server exposing one echo tool per name.
// Each tool returns the text "<name>:<input text>" as a single TextContent. The
// returned transport is the client side; the server stops when ctx is cancelled.
func startEchoServer(t *testing.T, ctx context.Context, toolNames ...string) sdk.Transport {
	t.Helper()
	serverT, clientT := sdk.NewInMemoryTransports()
	server := sdk.NewServer(&sdk.Implementation{Name: "echo", Version: "v1"}, nil)
	for _, name := range toolNames {
		sdk.AddTool(server, &sdk.Tool{Name: name, Description: "echo tool " + name},
			func(ctx context.Context, req *sdk.CallToolRequest, in echoArgs) (*sdk.CallToolResult, echoOut, error) {
				return &sdk.CallToolResult{
					Content: []sdk.Content{&sdk.TextContent{Text: name + ":" + in.Text}},
				}, echoOut{}, nil
			})
	}
	go func() { _ = server.Run(ctx, serverT) }()
	return clientT
}

// startEmptyErrorServer runs an in-memory MCP server with one tool "silent"
// that returns IsError with no content at all (no text), to exercise the
// empty-error fallback. The Content field is set to a non-nil empty slice so
// the SDK does not auto-populate it with the JSON of the typed output value.
func startEmptyErrorServer(t *testing.T, ctx context.Context) sdk.Transport {
	t.Helper()
	serverT, clientT := sdk.NewInMemoryTransports()
	server := sdk.NewServer(&sdk.Implementation{Name: "silent", Version: "v1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "silent", Description: "errors with no message"},
		func(ctx context.Context, req *sdk.CallToolRequest, in echoArgs) (*sdk.CallToolResult, echoOut, error) {
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{}}, echoOut{}, nil
		})
	go func() { _ = server.Run(ctx, serverT) }()
	return clientT
}

// startErrorServer runs an in-memory MCP server with one tool "boom" that always
// returns a tool-level error (IsError) carrying the text "boom: it failed".
func startErrorServer(t *testing.T, ctx context.Context) sdk.Transport {
	t.Helper()
	serverT, clientT := sdk.NewInMemoryTransports()
	server := sdk.NewServer(&sdk.Implementation{Name: "err", Version: "v1"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: "boom", Description: "always errors"},
		func(ctx context.Context, req *sdk.CallToolRequest, in echoArgs) (*sdk.CallToolResult, echoOut, error) {
			return &sdk.CallToolResult{
				IsError: true,
				Content: []sdk.Content{&sdk.TextContent{Text: "boom: it failed"}},
			}, echoOut{}, nil
		})
	go func() { _ = server.Run(ctx, serverT) }()
	return clientT
}

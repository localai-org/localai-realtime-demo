package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// bridgedTool adapts one MCP server tool to the realtime.Tool interface. The
// session is the live client connection the call is dispatched over.
type bridgedTool struct {
	session     *sdk.ClientSession
	name        string
	description string
	schema      any // the server's InputSchema, passed through verbatim
}

func (t *bridgedTool) Name() string        { return t.name }
func (t *bridgedTool) Description() string { return t.description }
func (t *bridgedTool) Parameters() any     { return t.schema }

// Execute forwards the model's JSON arguments to the MCP server and returns the
// joined text of the result. A tool-level error (IsError) is surfaced as a Go
// error so the realtime client reports the failure back to the model.
func (t *bridgedTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	args := map[string]any{}
	if s := strings.TrimSpace(argsJSON); s != "" {
		if err := json.Unmarshal([]byte(s), &args); err != nil {
			return "", fmt.Errorf("parse %s args: %w", t.name, err)
		}
	}
	res, err := t.session.CallTool(ctx, &sdk.CallToolParams{Name: t.name, Arguments: args})
	if err != nil {
		return "", fmt.Errorf("call %s: %w", t.name, err)
	}
	text := contentText(res.Content)
	if res.IsError {
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

// contentText joins the text parts of an MCP tool result, newline-separated.
// Non-text content (images, audio) is ignored — the realtime tool protocol is
// text-only.
func contentText(content []sdk.Content) string {
	var parts []string
	for _, c := range content {
		if tc, ok := c.(*sdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

package realtime

import (
	"context"

	openairt "github.com/WqyJh/go-openai-realtime/v2"
)

// Tool is a function the assistant can call during a conversation.
type Tool interface {
	Name() string
	Description() string
	// Parameters returns the JSON schema for the tool arguments, typically
	// produced by jsonschema.GenerateSchemaForType.
	Parameters() any
	// Execute runs the tool with the raw JSON arguments and returns the
	// output string that is sent back to the model.
	Execute(ctx context.Context, argsJSON string) (string, error)
}

// Registry holds the tools available to the assistant.
type Registry struct {
	tools map[string]Tool
	order []string
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
	if _, exists := r.tools[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// ToolUnions builds the tool definitions for the realtime session update.
func (r *Registry) ToolUnions() []openairt.ToolUnion {
	unions := make([]openairt.ToolUnion, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		unions = append(unions, openairt.ToolUnion{
			Function: &openairt.ToolFunction{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Parameters(),
			},
		})
	}
	return unions
}

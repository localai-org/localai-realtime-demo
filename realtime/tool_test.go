package realtime

import (
	"context"
	"testing"
)

type fakeTool struct{}

func (fakeTool) Name() string       { return "do_thing" }
func (fakeTool) Description() string { return "does a thing" }
func (fakeTool) Parameters() any     { return map[string]any{"type": "object"} }
func (fakeTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	return "done:" + argsJSON, nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})

	got, ok := r.Get("do_thing")
	if !ok {
		t.Fatal("expected do_thing to be registered")
	}
	out, err := got.Execute(context.Background(), `{"x":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `done:{"x":1}` {
		t.Fatalf("unexpected output: %q", out)
	}

	if _, ok := r.Get("missing"); ok {
		t.Fatal("expected missing tool to be absent")
	}
}

func TestRegistryToolUnions(t *testing.T) {
	r := NewRegistry()
	r.Register(fakeTool{})

	unions := r.ToolUnions()
	if len(unions) != 1 {
		t.Fatalf("expected 1 union, got %d", len(unions))
	}
	if unions[0].Function == nil || unions[0].Function.Name != "do_thing" {
		t.Fatalf("unexpected union: %+v", unions[0])
	}
}

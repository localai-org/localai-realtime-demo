package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestConnectListsAndExecutesTools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := startEchoServer(t, ctx, "alpha", "beta")
	b, err := connect(ctx, []namedTransport{{name: "s1", transport: tr}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	tools := b.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name() != "alpha" || tools[1].Name() != "beta" {
		t.Fatalf("unexpected tool order: %q, %q", tools[0].Name(), tools[1].Name())
	}
	if tools[0].Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}
	out, err := tools[0].Execute(ctx, `{"text":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "alpha:hi" {
		t.Fatalf("output = %q, want alpha:hi", out)
	}
}

func TestConnectDuplicateToolNameFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t1 := startEchoServer(t, ctx, "dup")
	t2 := startEchoServer(t, ctx, "dup")
	_, err := connect(ctx, []namedTransport{{"s1", t1}, {"s2", t2}})
	if err == nil {
		t.Fatal("expected duplicate tool name error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v, want it to mention duplicate", err)
	}
}

func TestConnectAcrossServersOrdersByServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t1 := startEchoServer(t, ctx, "alpha")
	t2 := startEchoServer(t, ctx, "beta")
	b, err := connect(ctx, []namedTransport{{"s1", t1}, {"s2", t2}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	got := []string{b.Tools()[0].Name(), b.Tools()[1].Name()}
	if got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("tool order = %v, want [alpha beta]", got)
	}
}

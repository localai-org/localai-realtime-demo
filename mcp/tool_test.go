package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestExecuteToolErrorBecomesGoError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := startErrorServer(t, ctx)
	b, err := connect(ctx, []namedTransport{{"s1", tr}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	_, err = b.Tools()[0].Execute(ctx, `{}`)
	if err == nil {
		t.Fatal("expected error from IsError result")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want it to contain boom", err)
	}
}

func TestExecuteEmptyToolErrorHasMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := startEmptyErrorServer(t, ctx)
	b, err := connect(ctx, []namedTransport{{"s1", tr}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	_, err = b.Tools()[0].Execute(ctx, "")
	if err == nil {
		t.Fatal("expected an error from an IsError result")
	}
	if err.Error() == "" {
		t.Fatal("expected a non-empty error message")
	}
	if !strings.Contains(err.Error(), "silent") {
		t.Fatalf("error = %v, want it to name the tool (silent)", err)
	}
}

func TestExecuteEmptyArgs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := startEchoServer(t, ctx, "alpha")
	b, err := connect(ctx, []namedTransport{{"s1", tr}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	out, err := b.Tools()[0].Execute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "alpha:" {
		t.Fatalf("output = %q, want alpha:", out)
	}
}

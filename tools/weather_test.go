package tools

import (
	"context"
	"strings"
	"testing"
)

func TestWeatherMetadata(t *testing.T) {
	w, err := NewWeather()
	if err != nil {
		t.Fatal(err)
	}
	if w.Name() != "get_weather" {
		t.Fatalf("name = %q", w.Name())
	}
	if w.Parameters() == nil {
		t.Fatal("expected non-nil parameters schema")
	}
}

func TestWeatherExecute(t *testing.T) {
	w, _ := NewWeather()
	out, err := w.Execute(context.Background(), `{"location":"Paris, France"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Paris, France") {
		t.Fatalf("expected location echoed, got %q", out)
	}
	if !strings.Contains(out, "temperature_c") {
		t.Fatalf("expected temperature_c, got %q", out)
	}
}

func TestWeatherExecuteBadJSON(t *testing.T) {
	w, _ := NewWeather()
	if _, err := w.Execute(context.Background(), `not json`); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

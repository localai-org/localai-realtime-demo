package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sashabaranov/go-openai/jsonschema"
)

type weatherParams struct {
	Location string `json:"location" jsonschema_description:"City and optional state/country, e.g. 'Paris, France'"`
}

// Weather is a mock get_weather tool that returns canned data. It exists to
// demonstrate the function-calling round trip end to end.
type Weather struct {
	schema any
}

func NewWeather() (*Weather, error) {
	schema, err := jsonschema.GenerateSchemaForType(weatherParams{})
	if err != nil {
		return nil, fmt.Errorf("generate weather schema: %w", err)
	}
	return &Weather{schema: schema}, nil
}

func (w *Weather) Name() string        { return "get_weather" }
func (w *Weather) Description() string { return "Get the current weather for a given location." }
func (w *Weather) Parameters() any     { return w.schema }

func (w *Weather) Execute(ctx context.Context, argsJSON string) (string, error) {
	var p weatherParams
	if err := json.Unmarshal([]byte(argsJSON), &p); err != nil {
		return "", fmt.Errorf("parse get_weather args: %w", err)
	}
	if p.Location == "" {
		p.Location = "unknown"
	}
	out, err := json.Marshal(map[string]any{
		"location":      p.Location,
		"temperature_c": 21,
		"conditions":    "Sunny",
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

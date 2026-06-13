package scaffold

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// GalleryIndexURL is the canonical LocalAI model gallery index. FetchGalleryModels
// reads it directly (no running LocalAI required) so the optional "show all"
// path lists the same catalog `docker compose` would gallery-install from.
const GalleryIndexURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/index.yaml"

// ModelOption is the slice of a gallery entry the scaffolder presents in a pick
// list.
type ModelOption struct {
	Name        string
	Description string
	Tags        []string
}

// galleryEntry mirrors the fields we decode from index.yaml; the file has many
// more, but yaml ignores unmapped keys.
type galleryEntry struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
}

// FetchGalleryModels downloads the gallery index and returns the entries whose
// tags (known usecases) include usecase, matched case-insensitively (e.g.
// "llm", "tts"). An empty usecase returns everything. Results are sorted by name
// for a stable menu.
//
// It errors cleanly when offline or the catalog is unreachable so the wizard can
// say "show all" is unavailable and fall back to the curated tables.
func FetchGalleryModels(ctx context.Context, usecase string) ([]ModelOption, error) {
	return fetchAndFilter(ctx, nil, GalleryIndexURL, usecase)
}

// fetchAndFilter is the parameterized core of FetchGalleryModels, split out so
// tests can point it at an httptest server instead of the live gallery.
func fetchAndFilter(ctx context.Context, httpClient *http.Client, indexURL, usecase string) ([]ModelOption, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gallery: build request: %w", err)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gallery: fetch index (offline?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gallery: index returned %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gallery: read index: %w", err)
	}

	var entries []galleryEntry
	if err := yaml.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("gallery: parse index: %w", err)
	}

	want := strings.ToLower(strings.TrimSpace(usecase))
	out := make([]ModelOption, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			continue
		}
		if want != "" && !hasTag(e.Tags, want) {
			continue
		}
		out = append(out, ModelOption{Name: e.Name, Description: e.Description, Tags: e.Tags})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.ToLower(t) == want {
			return true
		}
	}
	return false
}

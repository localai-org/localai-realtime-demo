package scaffold

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const galleryFixture = `
- name: gemma-4-e2b-it-qat-q4_0
  description: Gemma chat model
  tags:
    - llm
    - gemma
- name: vits-piper-it_IT-paola-sherpa
  description: Italian streaming voice
  tags:
    - tts
    - audio
- name: parakeet-cpp-tdt-0.6b-v3
  description: transcription
  tags:
    - audio
    - transcription
`

func TestFetchGalleryModelsFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(galleryFixture))
	}))
	defer srv.Close()

	models, err := fetchAndFilter(context.Background(), srv.Client(), srv.URL, "tts")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(models) != 1 || models[0].Name != "vits-piper-it_IT-paola-sherpa" {
		t.Fatalf("tts filter = %+v, want the sherpa voice only", models)
	}

	all, err := fetchAndFilter(context.Background(), srv.Client(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Name != "gemma-4-e2b-it-qat-q4_0" {
		t.Errorf("empty usecase should return all 3 sorted; got %+v", all)
	}
}

func TestFetchGalleryModelsOffline(t *testing.T) {
	if _, err := fetchAndFilter(context.Background(), http.DefaultClient, "http://127.0.0.1:1/index.yaml", "llm"); err == nil {
		t.Error("expected an error when the gallery is unreachable")
	}
}

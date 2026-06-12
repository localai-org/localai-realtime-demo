//go:build localvqe_embed && linux

package localvqe

import (
	"bytes"
	"os"
	"testing"
)

func TestEnsureEmbeddedExtracts(t *testing.T) {
	libPath, modelPath, ok, err := EnsureEmbedded()
	if err != nil {
		t.Fatalf("EnsureEmbedded returned error: %v", err)
	}
	if !ok {
		t.Fatal("EnsureEmbedded returned ok=false; expected bundled assets")
	}

	gotLib, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("read extracted lib %s: %v", libPath, err)
	}
	if !bytes.Equal(gotLib, embeddedLib) {
		t.Errorf("extracted lib bytes do not match embeddedLib")
	}

	gotModel, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatalf("read extracted model %s: %v", modelPath, err)
	}
	if !bytes.Equal(gotModel, embeddedModel) {
		t.Errorf("extracted model bytes do not match embeddedModel")
	}
}

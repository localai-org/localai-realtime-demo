package localvqe

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// EnsureEmbedded materializes the bundled LocalVQE shared library and GGUF model
// to disk and returns their paths. purego.Dlopen and localvqe_new both need real
// filesystem paths, so the embedded bytes cannot be used in memory.
//
// When no assets are bundled (a build without the localvqe_embed tag, or a
// non-linux target), it returns ok == false and no error: AEC is simply disabled.
func EnsureEmbedded() (libPath, modelPath string, ok bool, err error) {
	lib, model := embeddedAssets()
	if len(lib) == 0 || len(model) == 0 {
		return "", "", false, nil
	}

	base, berr := os.UserCacheDir()
	if berr != nil {
		base = os.TempDir()
	}

	// Content-hashed dir so swapping the bundled model picks a fresh location and
	// stale extractions are never reused.
	key := hashPrefix(lib) + hashPrefix(model)
	dir := filepath.Join(base, "localai-realtime-demo", "localvqe", key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", false, err
	}

	libPath = filepath.Join(dir, "liblocalvqe.so")
	modelPath = filepath.Join(dir, "model.gguf")

	if err := writeIfMissing(libPath, lib); err != nil {
		return "", "", false, err
	}
	if err := writeIfMissing(modelPath, model); err != nil {
		return "", "", false, err
	}

	return libPath, modelPath, true, nil
}

// hashPrefix returns the first 16 hex chars of the SHA-256 of b.
func hashPrefix(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// writeIfMissing writes data to path atomically (temp file in the same dir +
// rename) unless the path already exists.
func writeIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

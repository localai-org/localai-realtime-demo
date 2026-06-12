//go:build localvqe_embed && linux

package localvqe

import _ "embed"

//go:embed assets/liblocalvqe.so
var embeddedLib []byte

//go:embed assets/model.gguf
var embeddedModel []byte

func embeddedAssets() (lib, model []byte) { return embeddedLib, embeddedModel }

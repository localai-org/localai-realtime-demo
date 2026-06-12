//go:build !localvqe_embed || !linux

package localvqe

func embeddedAssets() (lib, model []byte) { return nil, nil }

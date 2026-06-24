package utils

import (
	cryptorand "crypto/rand"
	"encoding/hex"
)

func NewNamespaceName() (string, error) {
	p := make([]byte, 12)
	_, err := cryptorand.Read(p)
	if err != nil {
		return "", err
	}
	return ("test-" + hex.EncodeToString(p))[:12], nil
}

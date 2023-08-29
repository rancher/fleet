package utils

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
)

func NewNamespaceName() (string, error) {
	p := make([]byte, 12)
	_, err := cryptorand.Read(p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("test-%s", hex.EncodeToString(p))[:12], nil
}

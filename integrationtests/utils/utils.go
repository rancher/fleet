package utils

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"
)

func NewNamespaceName() (string, error) {
	rand.Seed(time.Now().UnixNano())
	p := make([]byte, 12)
	_, err := cryptorand.Read(p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("test-%s", hex.EncodeToString(p))[:12], nil
}

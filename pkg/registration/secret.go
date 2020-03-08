package registration

import (
	"crypto/sha256"
	"encoding/hex"
)

func SecretName(clientID, clientRandom string) string {
	d := sha256.New()
	d.Write([]byte(clientID))
	d.Write([]byte(clientRandom))
	return ("c-" + hex.EncodeToString(d.Sum(nil)))[:63]
}

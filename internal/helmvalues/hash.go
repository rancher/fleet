package helmvalues

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

const (
	ValuesKey       = "values"
	StagedValuesKey = "stagedValues"
)

// HashValuesSecret hashes the data of a secret. This is used for the bundle
// values secret created by fleet apply to detect changes and trigger updates.
func HashValuesSecret(data map[string][]byte) (string, error) {
	hasher := sha256.New()
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	hasher.Write(b)
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// HashOptions hashes the bytes passed in. This is used to create a hash of the
// bundledeployment's helm options and staged helm options.
func HashOptions(bytes ...[]byte) string {
	hasher := sha256.New()
	for _, b := range bytes {
		hasher.Write(b)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil))
}

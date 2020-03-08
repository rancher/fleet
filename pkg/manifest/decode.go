package manifest

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
)

func sha256Matches(r io.Reader, hash string) ([]byte, error) {
	buf := &bytes.Buffer{}

	d := sha256.New()
	w := io.MultiWriter(buf, d)
	if _, err := io.Copy(w, r); err != nil {
		return nil, err
	}

	finalID := toSHA256ID(d.Sum(nil))
	if finalID != hash {
		return nil, fmt.Errorf("content does not match hash got %s, expected %s", finalID, hash)
	}

	return buf.Bytes(), nil
}

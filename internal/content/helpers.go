package content

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"strings"
)

func GUnzip(content []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewBuffer(content))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func Base64GZ(data []byte) (string, error) {
	gz, err := Gzip(data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(gz), nil
}

func Decode(content, encoding string) ([]byte, error) {
	var data []byte

	if encoding == "base64" || strings.HasPrefix(encoding, "base64+") {
		d, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return nil, err
		}
		data = d
		encoding = strings.TrimPrefix(encoding, "base64")
		encoding = strings.TrimPrefix(encoding, "+")
	} else {
		data = []byte(content)
	}

	if encoding == "gz" {
		return GUnzip(data)
	}

	return data, nil
}

func Gzip(data []byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	w := gzip.NewWriter(buf)
	defer w.Close()

	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

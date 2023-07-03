// Package writer provides a writer that can be used to write to a file or stdout.
package writer

import (
	"io"
	"os"
	"path/filepath"
)

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }

func NewDefaultNone(output string) io.WriteCloser {
	if output == "" {
		return nil
	}
	return New(output)
}

func New(output string) io.WriteCloser {
	switch output {
	case "":
		return nopCloser{Writer: io.Discard}
	case "-":
		return os.Stdout
	default:
		return &lazyFileWriter{
			path: output,
		}
	}
}

type lazyFileWriter struct {
	path string
	file *os.File
}

func (l *lazyFileWriter) Write(data []byte) (int, error) {
	if l.file == nil {
		dir := filepath.Dir(l.path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return 0, err
		}
		f, err := os.Create(l.path)
		if err != nil {
			return 0, err
		}
		l.file = f
	}
	return l.file.Write(data)
}

func (l *lazyFileWriter) Close() error {
	if l.file == nil {
		return nil
	}
	return l.file.Close()
}

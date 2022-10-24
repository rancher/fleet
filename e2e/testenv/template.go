package testenv

import (
	"html/template"
	"math/rand"
	"os"
	"path"
	"strconv"
	"strings"
)

// Template loads a gotemplate from a file and writes templated output to
// another file, preferably in a temp dir
func Template(output string, tmplPath string, data any) error {
	b, err := os.ReadFile(tmplPath)
	if err != nil {
		return err
	}

	tmpl := template.Must(template.New("test").Parse(string(b)))
	var sb strings.Builder
	err = tmpl.Execute(&sb, data)
	if err != nil {
		return err
	}
	err = os.WriteFile(output, []byte(sb.String()), 0644) // nolint:gosec // test code
	if err != nil {
		return err
	}
	return nil
}

// RandomName returns a slightly random name, so temporary assets don't conflict
func RandomFilename(filename string) string {
	ext := path.Ext(filename)
	r := strconv.Itoa(rand.Intn(99999)) // nolint:gosec // Non-crypto use
	return strings.TrimSuffix(filename, ext) + r + ext
}

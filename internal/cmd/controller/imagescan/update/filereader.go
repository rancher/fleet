/*
Copyright 2020, 2021 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package update

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/kio/kioutil"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

// ScreeningReader is a kio.Reader that includes only files that are
// pertinent to automation. In practice this means looking for a
// particular token in each file, and ignoring those files without the
// token. This avoids most problematic cases -- e.g., templates in a
// Helm chart, which won't parse as YAML -- and cheaply filters for
// only those files that need processing.
type ScreeningLocalReader struct {
	Token string
	Path  string

	// This records the relative path of each file that passed
	// screening (i.e., contained the token), but couldn't be parsed.
	ProblemFiles []string
}

// Read scans the .Path recursively for files that contain .Token, and
// parses any that do. It applies the filename annotation used by
// [`kio.LocalPackageWriter`](https://godoc.org/sigs.k8s.io/kustomize/kyaml/kio#LocalPackageWriter)
// so that the same will write files back to their original
// location. The implementation follows that of
// [LocalPackageReader.Read](https://godoc.org/sigs.k8s.io/kustomize/kyaml/kio#LocalPackageReader.Read),
// adapting lightly (mainly to leave features out).
func (r *ScreeningLocalReader) Read() ([]*yaml.RNode, error) {
	if r.Path == "" {
		return nil, fmt.Errorf("must supply path to scan for files")
	}

	root, err := filepath.Abs(r.Path)
	if err != nil {
		return nil, fmt.Errorf("path field cannot be made absolute: %w", err)
	}

	// For the filename annotation, I want a directory for filenames
	// to be relative to; but I don't know whether path is a directory
	// or file yet so this must wait until the body of the filepath.Walk.
	var relativePath string

	tokenbytes := []byte(r.Token)

	var result []*yaml.RNode
	err = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("walking path for files: %w", err)
		}

		if p == root {
			if info.IsDir() {
				relativePath = p
				return nil // keep walking
			}
			relativePath = filepath.Dir(p)
		}

		if info.IsDir() {
			return nil
		}

		if ext := filepath.Ext(p); ext != ".yaml" && ext != ".yml" {
			return nil
		}

		// To check for the token, I need the file contents. This
		// assumes the file is encoded as UTF8.
		filebytes, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("reading YAML file: %w", err)
		}

		if !bytes.Contains(filebytes, tokenbytes) {
			return nil
		}

		path, err := filepath.Rel(relativePath, p)
		if err != nil {
			return fmt.Errorf("relativising path: %w", err)
		}
		annotations := map[string]string{
			kioutil.PathAnnotation: path,
		}

		rdr := &kio.ByteReader{
			Reader:            bytes.NewBuffer(filebytes),
			SetAnnotations:    annotations,
			PreserveSeqIndent: true,
		}

		nodes, err := rdr.Read()
		// Having screened the file and decided it's worth examining,
		// an error at this point is most unfortunate. However, it
		// doesn't need to be the end of the matter; we can record
		// this file as problematic, and continue.
		if err != nil {
			r.ProblemFiles = append(r.ProblemFiles, path)
			return nil
		}
		result = append(result, nodes...)
		return nil
	})

	return result, err
}

package testenv

import (
	"fmt"
	"html/template"
	"math/rand"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/rancher/gitjob/e2e/testenv"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

const gitrepoTemplate = "gitrepo-template.yaml"

// GitRepoData can be used with the gitrepo-template.yaml asset when no custom
// GitRepo properties are required. All fields are required.
type GitRepoData struct {
	Name            string
	Branch          string
	Paths           []string
	TargetNamespace string
}

// CreateGitRepo uses the template to create a gitrepo resource. The namespace is the TargetNamespace for the workloads.
func CreateGitRepo(k kubectl.Command, namespace string, name string, branch string, paths ...string) error {
	return ApplyTemplate(k, AssetPath(gitrepoTemplate), GitRepoData{
		TargetNamespace: namespace,
		Name:            name,
		Branch:          branch,
		Paths:           paths,
	})
}

// ApplyTemplate templates a file and applies it to the cluster.
func ApplyTemplate(k kubectl.Command, asset string, data interface{}) error {
	tmpdir, _ := os.MkdirTemp("", "fleet-")
	defer os.RemoveAll(tmpdir)

	output := path.Join(tmpdir, RandomFilename(asset))
	if err := Template(output, testenv.AssetPath(asset), data); err != nil {
		return err
	}
	out, err := k.Apply("-f", output)
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

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
	filename = path.Base(filename)
	r := strconv.Itoa(rand.Intn(99999)) // nolint:gosec // Non-crypto use
	return strings.TrimSuffix(filename, ext) + r + "." + ext
}

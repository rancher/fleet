package testenv

import (
	"fmt"
	"math/rand"
	"os"
	"path"
	"strconv"
	"strings"
	"text/template"

	ginkgo "github.com/onsi/ginkgo/v2"

	"github.com/rancher/fleet/e2e/testenv/kubectl"
)

const gitrepoTemplate = "gitrepo-template.yaml"
const helmopTemplate = "helmop-template.yaml"
const clusterTemplate = "cluster-template.yaml"
const clustergroupTemplate = "clustergroup-template.yaml"

var r = rand.New(rand.NewSource(ginkgo.GinkgoRandomSeed()))

// GitRepoData can be used with the gitrepo-template.yaml asset when no custom
// GitRepo properties are required. All fields except Shard are required.
type GitRepoData struct {
	Name            string
	Branch          string
	Paths           []string
	TargetNamespace string
	Shard           string
}

// HelmOpData can be used with the helmop-template.yaml asset when no custom
// HelmOp properties are required. All fields except Shard are required.
type HelmOpData struct {
	Name      string
	Chart     string
	Version   string
	Namespace string
	Shard     string
}

// CreateGitRepo uses the template to create a gitrepo resource. The namespace
// is the TargetNamespace for the workloads.
func CreateGitRepo(
	k kubectl.Command,
	namespace string,
	name string,
	branch string,
	shard string,
	paths ...string,
) error {
	return ApplyTemplate(k, AssetPath(gitrepoTemplate), GitRepoData{
		TargetNamespace: namespace,
		Name:            name,
		Branch:          branch,
		Paths:           paths,
		Shard:           shard,
	})
}

// CreateHelmOp uses the template to create a HelmOp resource. The namespace
// is the namespace for the workloads.
func CreateHelmOp(
	k kubectl.Command,
	namespace string,
	name string,
	chart string,
	version string,
	shard string,
) error {
	return ApplyTemplate(k, AssetPath(helmopTemplate), HelmOpData{
		Namespace: namespace,
		Name:      name,
		Chart:     chart,
		Version:   version,
		Shard:     shard,
	})
}

func CreateCluster(
	k kubectl.Command,
	namespace,
	name string,
	labels map[string]string,
	spec map[string]string,
) error {
	return ApplyTemplate(k, AssetPath(clusterTemplate), map[string]interface{}{
		"Name":      name,
		"Namespace": namespace,
		"Labels":    labels,
		"Spec":      spec,
	})
}

// CreateClusterGroup uses the template to create a clustergroup resource.
func CreateClusterGroup(
	k kubectl.Command,
	namespace,
	name string,
	matchLabels map[string]string,
	labels map[string]string,
) error {
	return ApplyTemplate(k, AssetPath(clustergroupTemplate), map[string]interface{}{
		"Name":        name,
		"Namespace":   namespace,
		"MatchLabels": matchLabels,
		"Labels":      labels,
	})
}

// ApplyTemplate templates a file and applies it to the cluster.
func ApplyTemplate(k kubectl.Command, asset string, data interface{}) error {
	tmpdir, _ := os.MkdirTemp("", "fleet-")
	defer os.RemoveAll(tmpdir)

	output := path.Join(
		tmpdir, RandomFilename(
			asset,
			r,
		),
	)
	if err := Template(output, AssetPath(asset), data); err != nil {
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
	err = os.WriteFile(output, []byte(sb.String()), 0644)
	if err != nil {
		return err
	}
	return nil
}

// RandomFilename returns a slightly random name, so temporary assets don't conflict
func RandomFilename(filename string, r *rand.Rand) string {
	ext := path.Ext(filename)
	filename = path.Base(filename)
	rv := strconv.Itoa(r.Intn(99999))
	return strings.TrimSuffix(filename, ext) + rv + ext
}

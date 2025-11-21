package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/rancher/fleet/internal/content"
	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	"k8s.io/apimachinery/pkg/util/yaml"

	"github.com/onsi/gomega/gbytes"
)

const (
	AssetsPath = "../assets/"
	separator  = "-------\n"
	apiVersion = "apiVersion: fleet.cattle.io/v1alpha1"
)

func GetBundleFromOutput(w io.Writer) (*v1alpha1.Bundle, error) {
	buf, ok := w.(*gbytes.Buffer)
	if !ok {
		return nil, errors.New("can't convert to gbytes.Buffer")
	}
	bundle := &v1alpha1.Bundle{}
	err := yaml.NewYAMLToJSONDecoder(bytes.NewBuffer(buf.Contents())).Decode(bundle)
	if err != nil {
		return nil, err
	}

	return bundle, nil
}

func GetBundleListFromOutput(w io.Writer) ([]*v1alpha1.Bundle, error) {
	buf, ok := w.(*gbytes.Buffer)
	if !ok {
		return nil, errors.New("can't convert to gbytes.Buffer")
	}
	bundles := []*v1alpha1.Bundle{}
	bundlesWithSeparator := strings.ReplaceAll(string(buf.Contents()), apiVersion, separator+apiVersion)
	bundlesStr := strings.Split(bundlesWithSeparator, separator)
	for _, bundleStr := range bundlesStr {
		if bundleStr != "" {
			bundle := &v1alpha1.Bundle{}
			err := yaml.NewYAMLToJSONDecoder(bytes.NewBufferString(bundleStr)).Decode(bundle)
			if err != nil {
				return nil, err
			}
			bundles = append(bundles, bundle)
		}
	}
	return bundles, nil
}

func IsResourcePresentInBundle(resourcePath string, resources []v1alpha1.BundleResource) (bool, error) {
	resourceFile, err := os.ReadFile(resourcePath)
	if err != nil {
		return false, err
	}

	for _, resource := range resources {
		if resource.Encoding == "base64+gz" {
			resourceFileEncoded, err := content.Base64GZ(resourceFile)
			if err != nil {
				return false, err
			}
			if resource.Content == resourceFileEncoded {
				return true, nil
			}
		} else if strings.ReplaceAll(resource.Content, "\n", "") == strings.ReplaceAll(string(resourceFile), "\n", "") {
			return true, nil
		}
	}

	return false, nil
}

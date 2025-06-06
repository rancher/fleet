package ocistorage

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rancher/fleet/internal/manifest"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

const (
	fileType     = "application/fleet.file"
	artifactType = "application/fleet.manifest"

	OCIStorageExperimentalFlag = "EXPERIMENTAL_OCI_STORAGE"
)

type OCIOpts struct {
	Reference       string
	Username        string
	Password        string
	AgentUsername   string
	AgentPassword   string
	BasicHTTP       bool
	InsecureSkipTLS bool
}

type OrasOps interface {
	PackManifest(ctx context.Context, pusher content.Pusher, packManifestVersion oras.PackManifestVersion, artifactType string, opts oras.PackManifestOptions) (ocispec.Descriptor, error)
	Copy(ctx context.Context, src oras.ReadOnlyTarget, srcRef string, dst oras.Target, dstRef string, opts oras.CopyOptions) (ocispec.Descriptor, error)
	NewStore() oras.Target
}

type OrasOperator struct{}

func (o *OrasOperator) NewStore() oras.Target {
	return memory.New()
}

func (o *OrasOperator) PackManifest(ctx context.Context, pusher content.Pusher, packManifestVersion oras.PackManifestVersion, artifactType string, opts oras.PackManifestOptions) (ocispec.Descriptor, error) {
	return oras.PackManifest(ctx, pusher, packManifestVersion, artifactType, opts)
}

func (o *OrasOperator) Copy(ctx context.Context, src oras.ReadOnlyTarget, srcRef string, dst oras.Target, dstRef string, opts oras.CopyOptions) (ocispec.Descriptor, error) {
	return oras.Copy(ctx, src, srcRef, dst, dstRef, opts)
}

type OCIWrapper struct {
	oci OrasOps
}

func NewOCIWrapper() *OCIWrapper {
	return &OCIWrapper{
		oci: &OrasOperator{},
	}
}

func getHTTPClient(insecureSkipTLS bool) *http.Client {
	if !insecureSkipTLS {
		return retry.DefaultClient
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402
		},
	}
}

func getAuthClient(opts OCIOpts) *auth.Client {
	client := &auth.Client{
		Client: getHTTPClient(opts.InsecureSkipTLS),
		Cache:  auth.NewCache(),
	}
	if opts.Username != "" {
		cred := auth.Credential{
			Username: opts.Username,
			Password: opts.Password,
		}
		client.Credential = func(ctx context.Context, s string) (auth.Credential, error) {
			return cred, nil
		}
	}
	return client
}

func newOCIRepository(id string, opts OCIOpts) (*remote.Repository, error) {
	repo, err := remote.NewRepository(join(opts.Reference, id))
	if err != nil {
		return nil, err
	}
	repo.PlainHTTP = opts.BasicHTTP
	repo.Client = getAuthClient(opts)
	return repo, nil
}

// join cleans and joins the elements with slash. We avoid filepath.Join, since
// it uses backslashes on Windows.
func join(elem ...string) string {
	for i, e := range elem {
		if e != "" {
			return filepath.Clean(strings.Join(elem[i:], "/"))
		}
	}
	return ""
}

func getDataFromDescriptor(ctx context.Context, store oras.Target, desc ocispec.Descriptor) ([]byte, error) {
	rc, err := store.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	err = rc.Close()
	if err != nil {
		return nil, err
	}
	return data, nil
}

func checkIDAnnotation(desc ocispec.Descriptor, id string) error {
	if len(desc.Annotations) != 1 {
		return fmt.Errorf("expecting 1 Annotation in layer descriptor. Found %d", len(desc.Annotations))
	}
	idFound, ok := desc.Annotations["id"]
	if !ok || idFound != id {
		return fmt.Errorf("could not find expected id in Descriptor's annotations")
	}
	return nil
}

func (o *OCIWrapper) pushFile(ctx context.Context, opts OCIOpts, reader io.Reader, desc ocispec.Descriptor, id string) error {
	s := o.oci.NewStore()
	err := s.Push(ctx, desc, reader)
	if err != nil {
		return err
	}

	fileDescriptors := make([]ocispec.Descriptor, 0, 1)
	fileDescriptors = append(fileDescriptors, desc)
	ociOpts := oras.PackManifestOptions{
		Layers: fileDescriptors,
	}
	manifestDescriptor, err := o.oci.PackManifest(ctx, s, oras.PackManifestVersion1_1, artifactType, ociOpts)
	if err != nil {
		return err
	}
	tag := "latest"
	err = s.Tag(ctx, manifestDescriptor, tag)
	if err != nil {
		return err
	}
	repo, err := newOCIRepository(id, opts)
	if err != nil {
		return err
	}

	_, err = o.oci.Copy(ctx, s, tag, repo, tag, oras.DefaultCopyOptions)
	return err
}

func (o *OCIWrapper) pullFile(ctx context.Context, opts OCIOpts, id string) ([]byte, error) {
	s := o.oci.NewStore()

	// use the agent credentials (read only) if present
	if opts.AgentUsername != "" {
		opts.Username = opts.AgentUsername
	}
	if opts.AgentPassword != "" {
		opts.Password = opts.AgentPassword
	}

	// copy from remote OCI registry to local memory store
	repo, err := newOCIRepository(id, opts)
	if err != nil {
		return nil, err
	}
	tag := "latest"
	_, err = o.oci.Copy(ctx, repo, tag, s, tag, oras.DefaultCopyOptions)
	if err != nil {
		return nil, err
	}

	// access the root node
	rootDesc, err := s.Resolve(ctx, tag)
	if err != nil {
		return nil, err
	}

	// fetch the root node of the manifest
	rootData, err := getDataFromDescriptor(ctx, s, rootDesc)
	if err != nil {
		return nil, err
	}

	// unmarshall the root node in order to access the layers
	var root struct {
		MediaType string `json:"mediaType"`
		Layers    []ocispec.Descriptor
	}
	if err := json.Unmarshal(rootData, &root); err != nil {
		return nil, err
	}
	if len(root.Layers) != 1 {
		return nil, fmt.Errorf("expected 1 layer in OCI manifest, %d found", len(root.Layers))
	}
	// get the layer descriptor and fetch from the store
	desc := root.Layers[0]
	// when pushing we add the id of the manifest to the annotations
	// it should match
	if err := checkIDAnnotation(desc, id); err != nil {
		return nil, err
	}

	// return the data for the layer (which is the original fleet manifest)
	return getDataFromDescriptor(ctx, s, desc)
}

// PushManifest creates and pushes an OCI manifest to a remote OCI registry with the
// contents of the given fleet manifest.
// The OCI manifest will be named after the given id.
func (o *OCIWrapper) PushManifest(ctx context.Context, opts OCIOpts, id string, m *manifest.Manifest) error {
	data, err := m.Content()
	if err != nil {
		return err
	}
	desc := ocispec.Descriptor{
		MediaType: fileType,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
		Annotations: map[string]string{
			"id": id,
		},
	}
	return o.pushFile(ctx, opts, bytes.NewReader(data), desc, id)
}

// PullManifest pulls the OCI manifest identified by the given id from a remote OCI registry
// and fills and returns a fleet manifest with the contents.
func (o *OCIWrapper) PullManifest(ctx context.Context, opts OCIOpts, id string) (*manifest.Manifest, error) {
	data, err := o.pullFile(ctx, opts, id)
	if err != nil {
		return nil, err
	}

	return manifest.FromJSON(data, "")
}

// DeleteManifest deletes the OCI manifest identified by the given id and "latest" tag from a remote OCI registry.
func (o *OCIWrapper) DeleteManifest(ctx context.Context, opts OCIOpts, id string) error {
	repo, err := newOCIRepository(id, opts)
	if err != nil {
		return fmt.Errorf("failed to create repository for %s: %w", id, err)
	}

	tag := "latest"
	desc, err := repo.Resolve(ctx, tag)
	if err != nil {
		return fmt.Errorf("failed to resolve tag '%s' for artifact '%s': %w", tag, id, err)
	}

	return repo.Delete(ctx, desc)
}

// ExperimentalOCIIsEnabled returns true if the EXPERIMENTAL_OCI_STORAGE env variable is set to true
// returns false otherwise
func ExperimentalOCIIsEnabled() bool {
	value, err := strconv.ParseBool(os.Getenv(OCIStorageExperimentalFlag))
	return err == nil && value
}

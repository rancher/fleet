package ociwrapper

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	"github.com/opencontainers/go-digest"
	"go.uber.org/mock/gomock"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/mocks"
	orasmemory "oras.land/oras-go/v2/content/memory"
)

type MockOrasOperator struct {
	ReturnOrasStore  bool
	Target           *mocks.MockTarget
	CopyMock         func(ctx context.Context, src oras.ReadOnlyTarget, srcRef string, dst oras.Target, dstRef string, opts oras.CopyOptions) (ocispec.Descriptor, error)
	PackManifestMock func(ctx context.Context, pusher content.Pusher, packManifestVersion oras.PackManifestVersion, artifactType string, opts oras.PackManifestOptions) (ocispec.Descriptor, error)
}

func (m *MockOrasOperator) PackManifest(ctx context.Context, pusher content.Pusher, packManifestVersion oras.PackManifestVersion, artifactType string, opts oras.PackManifestOptions) (ocispec.Descriptor, error) {
	if m.PackManifestMock != nil {
		return m.PackManifestMock(ctx, pusher, packManifestVersion, artifactType, opts)
	}
	return oras.PackManifest(ctx, pusher, packManifestVersion, artifactType, opts)
}

func (m *MockOrasOperator) Copy(ctx context.Context, src oras.ReadOnlyTarget, srcRef string, dst oras.Target, dstRef string, opts oras.CopyOptions) (ocispec.Descriptor, error) {
	if m.CopyMock != nil {
		return m.CopyMock(ctx, src, srcRef, dst, dstRef, opts)
	}
	return oras.Copy(ctx, src, srcRef, dst, dstRef, opts)
}

func (m *MockOrasOperator) NewStore() oras.Target {
	if m.ReturnOrasStore {
		return orasmemory.New()
	}
	return m.Target
}

func NewMockOrasOperator(ctrl *gomock.Controller, returnOrasStore bool) *MockOrasOperator {
	return &MockOrasOperator{
		ReturnOrasStore: returnOrasStore,
		Target:          mocks.NewMockTarget(ctrl),
	}
}

var _ = Describe("OCIUtils tests", func() {
	var (
		ctrl *gomock.Controller
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
	})

	It("returns an error when can't push to the store", func() {
		orasOperatorMock := NewMockOrasOperator(ctrl, false)
		orasOperatorMock.Target.EXPECT().Push(gomock.Any(), gomock.Any(), gomock.Any()).Return(fmt.Errorf("TEST ERROR")).Times(1)

		opts := OCIOpts{
			Reference: "test.com",
		}
		manifest := &manifest.Manifest{
			Commit: "123456",
			Resources: []fleet.BundleResource{
				{
					Name:     "resource1",
					Content:  "Content1",
					Encoding: "encoding1",
				},
			},
		}
		oci := &OCIWrapper{
			oci: orasOperatorMock,
		}
		err := oci.PushManifest(context.Background(), opts, "123", manifest)
		Expect(err.Error()).To(Equal("TEST ERROR"))
	})
	It("returns an error if oras fails packing the manifest", func() {
		orasOperatorMock := NewMockOrasOperator(ctrl, true)
		orasOperatorMock.PackManifestMock = func(_ context.Context,
			_ content.Pusher,
			_ oras.PackManifestVersion,
			_ string,
			_ oras.PackManifestOptions) (ocispec.Descriptor, error) {
			return ocispec.Descriptor{}, fmt.Errorf("ERROR PACKING")
		}
		oci := &OCIWrapper{
			oci: orasOperatorMock,
		}
		opts := OCIOpts{
			Reference: "test.com",
		}
		manifest := &manifest.Manifest{
			Commit: "123456",
			Resources: []fleet.BundleResource{
				{
					Name:     "resource1",
					Content:  "Content1",
					Encoding: "encoding1",
				},
			},
		}
		err := oci.PushManifest(context.Background(), opts, "123", manifest)
		Expect(err.Error()).To(Equal("ERROR PACKING"))
	})
	It("returns an error when is unable to connect to the registry", func() {
		oci := NewOCIWrapper()
		opts := OCIOpts{
			Reference: "127.0.0.0:2334",
		}
		manifest := &manifest.Manifest{
			Commit: "123456",
			Resources: []fleet.BundleResource{
				{
					Name:     "resource1",
					Content:  "Content1",
					Encoding: "encoding1",
				},
			},
		}
		err := oci.PushManifest(context.Background(), opts, "123", manifest)
		Expect(err.Error()).To(Or(ContainSubstring("connection refused"), ContainSubstring("i/o timeout")))
	})
	It("returns an OCI repository with the expected values when using basic HTTP", func() {
		opts := OCIOpts{
			Reference: "test.com",
			BasicHTTP: true,
		}
		repo, err := newOCIRepository("1234", opts)
		Expect(err).ToNot(HaveOccurred())
		Expect(repo.PlainHTTP).To(BeTrue())

		opts.BasicHTTP = false
		repo, err = newOCIRepository("1234", opts)
		Expect(err).ToNot(HaveOccurred())
		Expect(repo.PlainHTTP).To(BeFalse())
	})
	It("return the expected tls client", func() {
		client := getHTTPClient(true)
		expected := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
		Expect(client).To(Equal(expected))

		client = getHTTPClient(false)
		Expect(client).To(Equal(retry.DefaultClient))
	})
	It("return the expected credentials", func() {
		opts := OCIOpts{
			Reference: "test.com",
			BasicHTTP: true,
		}
		client := getAuthClient(opts)
		Expect(client.Credential).To(BeNil())

		opts.Username = "user"
		client = getAuthClient(opts)
		Expect(client.Credential).ToNot(BeNil())
		cred, err := client.Credential(context.Background(), "test")
		Expect(err).ToNot(HaveOccurred())
		Expect(cred).To(Equal(auth.Credential{
			Username: "user",
			Password: "",
		}))

		opts.Password = "pass"
		client = getAuthClient(opts)
		Expect(client.Credential).ToNot(BeNil())
		cred, err = client.Credential(context.Background(), "test")
		Expect(err).ToNot(HaveOccurred())
		Expect(cred).To(Equal(auth.Credential{
			Username: "user",
			Password: "pass",
		}))
	})
	It("returns an error when using an empty OCI registry reference", func() {
		opts := OCIOpts{
			Reference: "",
		}
		manifest := &manifest.Manifest{
			Commit: "123456",
			Resources: []fleet.BundleResource{
				{
					Name:     "resource1",
					Content:  "Content1",
					Encoding: "encoding1",
				},
			},
		}
		oci := NewOCIWrapper()
		err := oci.PushManifest(context.Background(), opts, "123", manifest)
		Expect(err.Error()).To(Equal("invalid reference: missing registry or repository"))
	})
	It("returns an error if the OCI manifest does not have the expected annotation id", func() {
		orasOperatorMock := NewMockOrasOperator(ctrl, true)
		orasOperatorMock.CopyMock = func(_ context.Context,
			_ oras.ReadOnlyTarget,
			_ string,
			target oras.Target,
			_ string,
			opts oras.CopyOptions) (ocispec.Descriptor, error) {
			// fill the store with data
			data := []byte("This is test")
			desc := ocispec.Descriptor{
				MediaType: "application/octet-stream",
				Digest:    digest.FromBytes(data),
				Size:      int64(len(data)),
				Annotations: map[string]string{
					"id": "example_id", // this is the annotation id that is not the expected one
				},
			}

			ctx := context.Background()
			err := target.Push(ctx, desc, bytes.NewReader(data))
			Expect(err).ToNot(HaveOccurred())

			fileDescriptors := make([]ocispec.Descriptor, 0, 1)
			fileDescriptors = append(fileDescriptors, desc)
			ociOpts := oras.PackManifestOptions{
				Layers: fileDescriptors,
			}

			manifestDescriptor, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, artifactType, ociOpts)
			Expect(err).ToNot(HaveOccurred())

			tag := "latest"
			err = target.Tag(ctx, manifestDescriptor, tag)
			Expect(err).ToNot(HaveOccurred())

			return ocispec.Descriptor{}, nil
		}

		opts := OCIOpts{
			Reference: "test.com",
		}
		oci := &OCIWrapper{
			oci: orasOperatorMock,
		}
		_, err := oci.PullManifest(context.Background(), opts, "s-123456")
		// s-123456 != example_id so PullManifest should return an error
		Expect(err.Error()).To(Equal("could not find expected id in Descriptor's annotations"))
	})
	It("returns an error if the OCI manifest does not have the expected tag", func() {
		orasOperatorMock := NewMockOrasOperator(ctrl, true)
		orasOperatorMock.CopyMock = func(_ context.Context, _ oras.ReadOnlyTarget, _ string, target oras.Target, _ string, opts oras.CopyOptions) (ocispec.Descriptor, error) {
			// fill the store with data
			data := []byte("This is test")
			desc := ocispec.Descriptor{
				MediaType: "application/octet-stream",
				Digest:    digest.FromBytes(data),
				Size:      int64(len(data)),
				Annotations: map[string]string{
					"id": "example_id", // this is the annotation id that is not the expected one
				},
			}

			ctx := context.Background()
			err := target.Push(ctx, desc, bytes.NewReader(data))
			Expect(err).ToNot(HaveOccurred())

			fileDescriptors := make([]ocispec.Descriptor, 0, 1)
			fileDescriptors = append(fileDescriptors, desc)
			ociOpts := oras.PackManifestOptions{
				Layers: fileDescriptors,
			}
			manifestDescriptor, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, artifactType, ociOpts)
			Expect(err).ToNot(HaveOccurred())

			tag := "this_tag_is_not_expected"
			err = target.Tag(ctx, manifestDescriptor, tag)
			Expect(err).ToNot(HaveOccurred())

			return ocispec.Descriptor{}, nil
		}

		opts := OCIOpts{
			Reference: "test.com",
		}
		oci := &OCIWrapper{
			oci: orasOperatorMock,
		}
		_, err := oci.PullManifest(context.Background(), opts, "s-123456")
		Expect(err.Error()).To(Equal("not found"))
	})
	It("returns an error if the OCI manifest is empty", func() {
		orasOperatorMock := NewMockOrasOperator(ctrl, true)
		orasOperatorMock.CopyMock = func(_ context.Context, _ oras.ReadOnlyTarget, _ string, target oras.Target, _ string, opts oras.CopyOptions) (ocispec.Descriptor, error) {
			// fill the store with an empty manifest
			ctx := context.Background()

			fileDescriptors := make([]ocispec.Descriptor, 0)
			ociOpts := oras.PackManifestOptions{
				Layers: fileDescriptors,
			}
			manifestDescriptor, err := oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, artifactType, ociOpts)
			Expect(err).ToNot(HaveOccurred())

			tag := "latest"
			err = target.Tag(ctx, manifestDescriptor, tag)
			Expect(err).ToNot(HaveOccurred())

			return ocispec.Descriptor{}, nil
		}

		opts := OCIOpts{
			Reference: "test.com",
		}
		oci := &OCIWrapper{
			oci: orasOperatorMock,
		}
		_, err := oci.PullManifest(context.Background(), opts, "s-123456")
		Expect(err.Error()).To(Equal("expecting 1 Annotation in layer descriptor. Found 0"))
	})
})

var _ = Describe("OCIUtils Experimental flag tests", func() {
	var envBeforeTest string
	const experimentalEnv = "EXPERIMENTAL_OCI_STORAGE"

	BeforeEach(func() {
		envBeforeTest = os.Getenv(experimentalEnv)
	})

	AfterEach(func() {
		if envBeforeTest != "" {
			// set the value it had before the test
			Expect(os.Setenv(experimentalEnv, envBeforeTest)).ToNot(HaveOccurred())
		} else {
			Expect(os.Unsetenv(experimentalEnv)).ToNot(HaveOccurred())
		}
	})

	DescribeTable("Check value returned is the expected one",
		func(value string, expected bool) {
			if value == "unset" {
				Expect(os.Unsetenv(experimentalEnv)).ToNot(HaveOccurred())
			} else {
				Expect(os.Setenv(experimentalEnv, value)).ToNot(HaveOccurred())
			}
			result := ExperimentalOCIIsEnabled()
			Expect(result).To(Equal(expected))
		},
		Entry("When setting to True", "True", true),
		Entry("When setting to true", "true", true),
		Entry("When setting to TRUE", "TRUE", true),
		Entry("When setting to tRue", "tRue", false),
		Entry("When setting to false", "false", false),
		Entry("When setting to whatever", "whatever", false),
		Entry("When not setting the value", "unset", false),
	)
})

package ocistorage

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/opencontainers/go-digest"
	"go.uber.org/mock/gomock"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	orasmemory "oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
		client := getHTTPClient(true, nil)

		// Custom path wraps transport in retry.Transport
		retryTransport, ok := client.Transport.(*retry.Transport)
		Expect(ok).To(BeTrue())
		innerTransport, ok := retryTransport.Base.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(innerTransport.TLSClientConfig).ToNot(BeNil())
		Expect(innerTransport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
		Expect(innerTransport.Proxy).ToNot(BeNil())

		client = getHTTPClient(false, nil)
		Expect(client).To(Equal(retry.DefaultClient))
	})
	It("should use custom CA bundle when provided", func() {
		// Use a valid test certificate from the codebase pattern (same as netutils_test.go)
		caBundle := []byte(`-----BEGIN CERTIFICATE-----
MIICGTCCAZ+gAwIBAgIQCeCTZaz32ci5PhwLBCou8zAKBggqhkjOPQQDAzBOMQsw
CQYDVQQGEwJVUzEXMBUGA1UEChMORGlnaUNlcnQsIEluYy4xJjAkBgNVBAMTHURp
Z2lDZXJ0IFRMUyBFQ0MgUDM4NCBSb290IEc1MB4XDTIxMDExNTAwMDAwMFoXDTQ2
MDExNDIzNTk1OVowTjELMAkGA1UEBhMCVVMxFzAVBgNVBAoTDkRpZ2lDZXJ0LCBJ
bmMuMSYwJAYDVQQDEx1EaWdpQ2VydCBUTFMgRUNDIFAzODQgUm9vdCBHNTB2MBAG
ByqGSM49AgEGBSuBBAAiA2IABMFEoc8Rl1Ca3iOCNQfN0MsYndLxf3c1TzvdlHJS
7cI7+Oz6e2tYIOyZrsn8aLN1udsJ7MgT9U7GCh1mMEy7H0cKPGEQQil8pQgO4CLp
0zVozptjn4S1mU1YoI71VOeVyaNCMEAwHQYDVR0OBBYEFMFRRVBZqz7nLFr6ICIS
B4CIfBFqMA4GA1UdDwEB/wQEAwIBhjAPBgNVHRMBAf8EBTADAQH/MAoGCCqGSM49
BAMDA2gAMGUCMQCJao1H5+z8blUD2WdsJk6Dxv3J+ysTvLd6jLRl0mlpYxNjOyZQ
LgGheQaRnUi/wr4CMEfDFXuxoJGZSZOoPHzoRgaLLPIxAJSdYsiJvRmEFOml+wG4
DXZDjC5Ty3zfDBeWUA==
-----END CERTIFICATE-----`)
		client := getHTTPClient(false, caBundle)

		// Verify transport is configured with custom TLS config
		retryT, ok := client.Transport.(*retry.Transport)
		Expect(ok).To(BeTrue())
		transport, ok := retryT.Base.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(transport.TLSClientConfig).ToNot(BeNil())
		Expect(transport.TLSClientConfig.RootCAs).ToNot(BeNil())
		Expect(transport.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)))
		Expect(transport.TLSClientConfig.InsecureSkipVerify).To(BeFalse())
	})
	It("should merge proxy CA bundle from environment variable", func() {
		// Use a valid certificate for proxy CA (same as custom test)
		proxyCA := `-----BEGIN CERTIFICATE-----
MIICGTCCAZ+gAwIBAgIQCeCTZaz32ci5PhwLBCou8zAKBggqhkjOPQQDAzBOMQsw
CQYDVQQGEwJVUzEXMBUGA1UEChMORGlnaUNlcnQsIEluYy4xJjAkBgNVBAMTHURp
Z2lDZXJ0IFRMUyBFQ0MgUDM4NCBSb290IEc1MB4XDTIxMDExNTAwMDAwMFoXDTQ2
MDExNDIzNTk1OVowTjELMAkGA1UEBhMCVVMxFzAVBgNVBAoTDkRpZ2lDZXJ0LCBJ
bmMuMSYwJAYDVQQDEx1EaWdpQ2VydCBUTFMgRUNDIFAzODQgUm9vdCBHNTB2MBAG
ByqGSM49AgEGBSuBBAAiA2IABMFEoc8Rl1Ca3iOCNQfN0MsYndLxf3c1TzvdlHJS
7cI7+Oz6e2tYIOyZrsn8aLN1udsJ7MgT9U7GCh1mMEy7H0cKPGEQQil8pQgO4CLp
0zVozptjn4S1mU1YoI71VOeVyaNCMEAwHQYDVR0OBBYEFMFRRVBZqz7nLFr6ICIS
B4CIfBFqMA4GA1UdDwEB/wQEAwIBhjAPBgNVHRMBAf8EBTADAQH/MAoGCCqGSM49
BAMDA2gAMGUCMQCJao1H5+z8blUD2WdsJk6Dxv3J+ysTvLd6jLRl0mlpYxNjOyZQ
LgGheQaRnUi/wr4CMEfDFXuxoJGZSZOoPHzoRgaLLPIxAJSdYsiJvRmEFOml+wG4
DXZDjC5Ty3zfDBeWUA==
-----END CERTIFICATE-----`
		os.Setenv("PROXY_CA_BUNDLE", proxyCA)
		defer os.Unsetenv("PROXY_CA_BUNDLE")

		// Use the same valid certificate for custom CA (simpler for testing)
		customCA := []byte(proxyCA)
		client := getHTTPClient(false, customCA)

		// Should have merged both CAs
		retryT, ok := client.Transport.(*retry.Transport)
		Expect(ok).To(BeTrue())
		transport, ok := retryT.Base.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(transport.TLSClientConfig).ToNot(BeNil())
		Expect(transport.TLSClientConfig.RootCAs).ToNot(BeNil())
		Expect(transport.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)))

		// Verify both CAs are valid and would have been loaded
		testPool := x509.NewCertPool()
		ok = testPool.AppendCertsFromPEM(customCA)
		Expect(ok).To(BeTrue(), "custom CA bundle should be valid PEM")
		ok = testPool.AppendCertsFromPEM([]byte(proxyCA))
		Expect(ok).To(BeTrue(), "proxy CA bundle should be valid PEM")
	})
	It("should handle invalid CA bundle gracefully", func() {
		invalidCA := []byte("not a valid PEM certificate")
		client := getHTTPClient(false, invalidCA)

		// Should still create a client with TLS config (warning logged)
		retryT, ok := client.Transport.(*retry.Transport)
		Expect(ok).To(BeTrue())
		transport, ok := retryT.Base.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(transport.TLSClientConfig).ToNot(BeNil())
		Expect(transport.TLSClientConfig.RootCAs).ToNot(BeNil())
	})
	It("should handle invalid proxy CA bundle gracefully", func() {
		os.Setenv("PROXY_CA_BUNDLE", "invalid proxy PEM")
		defer os.Unsetenv("PROXY_CA_BUNDLE")

		client := getHTTPClient(false, nil)

		// Should return default client when no valid CA and not insecure
		Expect(client).To(Equal(retry.DefaultClient))
	})
	It("should combine insecureSkipTLS with CA bundle", func() {
		caBundle := []byte(`-----BEGIN CERTIFICATE-----
MIICGTCCAZ+gAwIBAgIQCeCTZaz32ci5PhwLBCou8zAKBggqhkjOPQQDAzBOMQsw
CQYDVQQGEwJVUzEXMBUGA1UEChMORGlnaUNlcnQsIEluYy4xJjAkBgNVBAMTHURp
Z2lDZXJ0IFRMUyBFQ0MgUDM4NCBSb290IEc1MB4XDTIxMDExNTAwMDAwMFoXDTQ2
MDExNDIzNTk1OVowTjELMAkGA1UEBhMCVVMxFzAVBgNVBAoTDkRpZ2lDZXJ0LCBJ
bmMuMSYwJAYDVQQDEx1EaWdpQ2VydCBUTFMgRUNDIFAzODQgUm9vdCBHNTB2MBAG
ByqGSM49AgEGBSuBBAAiA2IABMFEoc8Rl1Ca3iOCNQfN0MsYndLxf3c1TzvdlHJS
7cI7+Oz6e2tYIOyZrsn8aLN1udsJ7MgT9U7GCh1mMEy7H0cKPGEQQil8pQgO4CLp
0zVozptjn4S1mU1YoI71VOeVyaNCMEAwHQYDVR0OBBYEFMFRRVBZqz7nLFr6ICIS
B4CIfBFqMA4GA1UdDwEB/wQEAwIBhjAPBgNVHRMBAf8EBTADAQH/MAoGCCqGSM49
BAMDA2gAMGUCMQCJao1H5+z8blUD2WdsJk6Dxv3J+ysTvLd6jLRl0mlpYxNjOyZQ
LgGheQaRnUi/wr4CMEfDFXuxoJGZSZOoPHzoRgaLLPIxAJSdYsiJvRmEFOml+wG4
DXZDjC5Ty3zfDBeWUA==
-----END CERTIFICATE-----`)
		client := getHTTPClient(true, caBundle)

		retryT, ok := client.Transport.(*retry.Transport)
		Expect(ok).To(BeTrue())
		transport, ok := retryT.Base.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(transport.TLSClientConfig).ToNot(BeNil())
		Expect(transport.TLSClientConfig.InsecureSkipVerify).To(BeTrue())
		Expect(transport.TLSClientConfig.RootCAs).ToNot(BeNil())
		Expect(transport.TLSClientConfig.MinVersion).To(Equal(uint16(tls.VersionTLS12)))

		// Verify the CA bundle is valid PEM (even though InsecureSkipVerify is set)
		testPool := x509.NewCertPool()
		ok = testPool.AppendCertsFromPEM(caBundle)
		Expect(ok).To(BeTrue(), "CA bundle should be valid PEM")
	})
	It("should not mutate the original CA bundle slice", func() {
		// validPEM is a real certificate so that AppendCertsFromPEM succeeds and
		// getHTTPClient actually reaches the append(caBundle, proxyBytes...) path.
		// An invalid PEM would make the function return early without appending,
		// meaning the defensive copy would never be exercised.
		validPEM := []byte(`-----BEGIN CERTIFICATE-----
MIICGTCCAZ+gAwIBAgIQCeCTZaz32ci5PhwLBCou8zAKBggqhkjOPQQDAzBOMQsw
CQYDVQQGEwJVUzEXMBUGA1UEChMORGlnaUNlcnQsIEluYy4xJjAkBgNVBAMTHURp
Z2lDZXJ0IFRMUyBFQ0MgUDM4NCBSb290IEc1MB4XDTIxMDExNTAwMDAwMFoXDTQ2
MDExNDIzNTk1OVowTjELMAkGA1UEBhMCVVMxFzAVBgNVBAoTDkRpZ2lDZXJ0LCBJ
bmMuMSYwJAYDVQQDEx1EaWdpQ2VydCBUTFMgRUNDIFAzODQgUm9vdCBHNTB2MBAG
ByqGSM49AgEGBSuBBAAiA2IABMFEoc8Rl1Ca3iOCNQfN0MsYndLxf3c1TzvdlHJS
7cI7+Oz6e2tYIOyZrsn8aLN1udsJ7MgT9U7GCh1mMEy7H0cKPGEQQil8pQgO4CLp
0zVozptjn4S1mU1YoI71VOeVyaNCMEAwHQYDVR0OBBYEFMFRRVBZqz7nLFr6ICIS
B4CIfBFqMA4GA1UdDwEB/wQEAwIBhjAPBgNVHRMBAf8EBTADAQH/MAoGCCqGSM49
BAMDA2gAMGUCMQCJao1H5+z8blUD2WdsJk6Dxv3J+ysTvLd6jLRl0mlpYxNjOyZQ
LgGheQaRnUi/wr4CMEfDFXuxoJGZSZOoPHzoRgaLLPIxAJSdYsiJvRmEFOml+wG4
DXZDjC5Ty3zfDBeWUA==
-----END CERTIFICATE-----`)

		os.Setenv("PROXY_CA_BUNDLE", string(validPEM))
		defer os.Unsetenv("PROXY_CA_BUNDLE")

		// Allocate with excess capacity so that append can write into the backing
		// array without reallocating. With len == cap (e.g. []byte("literal")),
		// append always reallocates and the defensive copy makes no observable
		// difference — the test would pass even without it.
		originalCA := make([]byte, len(validPEM), len(validPEM)+512)
		copy(originalCA, validPEM)

		// backing covers the full allocation so we can detect writes past len(originalCA).
		backing := originalCA[:cap(originalCA)]

		_ = getHTTPClient(false, originalCA)

		// The slice header and visible content must be unchanged.
		Expect(originalCA).To(Equal(validPEM))
		// Without the defensive copy, getHTTPClient would append '\n'+proxyBytes
		// directly into the excess capacity of the caller's backing array.
		Expect(backing[len(validPEM):]).To(Equal(make([]byte, 512)),
			"backing array beyond len(originalCA) must not be written to")
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
		Expect(err.Error()).To(ContainSubstring("not found"))
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

var _ = Describe("OCIUtils flag tests", func() {
	var envBeforeTest string

	BeforeEach(func() {
		envBeforeTest = os.Getenv(OCIStorageFlag)
	})

	AfterEach(func() {
		if envBeforeTest != "" {
			// set the value it had before the test
			Expect(os.Setenv(OCIStorageFlag, envBeforeTest)).ToNot(HaveOccurred())
		} else {
			Expect(os.Unsetenv(OCIStorageFlag)).ToNot(HaveOccurred())
		}
	})

	DescribeTable("Check value returned is the expected one",
		func(value string, expected bool) {
			if value == "unset" {
				Expect(os.Unsetenv(OCIStorageFlag)).ToNot(HaveOccurred())
			} else {
				Expect(os.Setenv(OCIStorageFlag, value)).ToNot(HaveOccurred())
			}
			result := OCIIsEnabled()
			Expect(result).To(Equal(expected))
		},
		Entry("When setting to True", "True", true),
		Entry("When setting to true", "true", true),
		Entry("When setting to TRUE", "TRUE", true),
		Entry("When setting to tRue", "tRue", true), // true because OCI storage is enabled by default.
		Entry("When setting to false", "false", false),
		Entry("When setting to whatever", "whatever", true), // true because OCI storage is enabled by default.
		Entry("When not setting the value", "unset", true),  // true because OCI storage is enabled by default.
	)
})

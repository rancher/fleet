package bundlereader_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rancher/fleet/internal/bundlereader"
)

// fsNode represents a directory structure used to model `.fleetignore` test cases.
type fsNode struct {
	name string

	contents string   // if not a directory
	children []fsNode // non-empty only in case of a directory

	isDir bool
}

// nolint: funlen
func TestGetContent(t *testing.T) {
	cases := []struct {
		name               string
		directoryStructure fsNode
		expectedFiles      map[string][]byte
		source             string
		auth               bundlereader.Auth
		expectedErr        *regexp.Regexp
	}{
		{
			name: "ensure panic doesn't occur when InsecureSkipVerify is set to false (#3782)",
			directoryStructure: fsNode{
				name: "fleet.yaml",
				contents: `namespace: fleet-helm-oci-with-auth-example
		 helm:
		   chart: "oci://ghcr.io/fleetqa/fleet-qa-examples/fleet-test-configmap-chart"
		   version: "0.1.0"
		   values:
		     replicas: 2`,
			},
			source: "oci://foo/bar/baz",
			auth: bundlereader.Auth{
				Username: "foo",
				Password: "bar",
				// InsecureSkipVerify is false by default
			},
			expectedErr: regexp.MustCompile("(no such host|server misbehaving)"),
		},
		{
			name: "no .fleetignore",
			directoryStructure: fsNode{
				isDir: true,
				name:  "no-fleetignore",
				children: []fsNode{
					{
						name:     "fleet.yaml",
						contents: "foo",
					},
					{
						name:  "chart",
						isDir: true,
						children: []fsNode{
							{
								name:     "myvalues.yaml",
								contents: "bar",
							},
						},
					},
					{
						name:     "something.yaml",
						contents: "foo",
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml": []byte("foo"),
			},
		},
		{
			name: "empty .fleetignore",
			directoryStructure: fsNode{
				isDir: true,
				name:  "empty-fleetignore",
				children: []fsNode{
					{
						name:     "fleet.yaml",
						contents: "foo",
					},
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     ".fleetignore",
						contents: "",
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml": []byte("foo"),
			},
		},
		{
			name: "ignore lines with leading # unless escaped",
			directoryStructure: fsNode{
				isDir: true,
				name:  "comments",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     "#something_else.yaml",
						contents: "bar",
					},
					{
						name:     ".fleetignore",
						contents: "#something.yaml\n\\#something_else.yaml",
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml": []byte("foo"),
			},
		},
		{
			name: "simple .fleetignore",
			directoryStructure: fsNode{
				isDir: true,
				name:  "simple-fleetignore",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     "something_else.yaml",
						contents: "bar",
					},
					{
						name:     ".fleetignore",
						contents: "not_here.txt\nsomething.yaml",
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something_else.yaml": []byte("bar"),
			},
		},
		{
			name: "glob syntax",
			directoryStructure: fsNode{
				isDir: true,
				name:  "glob-syntax",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     ".fleetignore",
						contents: "something*",
					},
				},
			},
			expectedFiles: map[string][]byte{},
		},
		{
			name: "ignore trailing spaces unless escaped",
			directoryStructure: fsNode{
				isDir: true,
				name:  "trim-space",
				children: []fsNode{
					{
						name:     "something.yaml ",
						contents: "foo",
					},
					{
						name:     "something_else.yaml  ",
						contents: "bar",
					},
					{
						name:     ".fleetignore",
						contents: "something_else.yaml\\ \\ \nsomething.yaml ",
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml ": []byte("foo"),
			},
		},
		{
			name: "ignore directories",
			directoryStructure: fsNode{
				isDir: true,
				name:  "ignore-directories",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     ".fleetignore",
						contents: "subdir",
					},
					{
						name:  "subdir",
						isDir: true,
						children: []fsNode{
							{
								name:     "in_dir.yaml",
								contents: "baz",
							},
						},
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml": []byte("foo"),
			},
		},
		{
			name: "ignore file multiple levels below .fleetignore",
			directoryStructure: fsNode{
				isDir: true,
				name:  "ignore-file-multiple-levels",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     ".fleetignore",
						contents: "in_dir.yaml",
					},
					{
						name:  "subdir",
						isDir: true,
						children: []fsNode{
							{
								name:  "subsubdir",
								isDir: true,
								children: []fsNode{
									{
										name:     "in_dir.yaml",
										contents: "bar",
									},
								},
							},
						},
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml": []byte("foo"),
			},
		},
		{
			name: ".fleetignore files in neighbour dirs do not interfere with one another",
			directoryStructure: fsNode{
				isDir: true,
				name:  "multiple-files-same-level",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:  "subdir1",
						isDir: true,
						children: []fsNode{
							{
								name:     "in_dir.yaml",
								contents: "from dir 1",
							},
							{
								name:     ".fleetignore",
								contents: "in_dir.yaml",
							},
						},
					},
					{
						name:  "subdir2",
						isDir: true,
						children: []fsNode{
							{
								name:     "in_dir.yaml",
								contents: "from dir 2",
							},
						},
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml":      []byte("foo"),
				"subdir2/in_dir.yaml": []byte("from dir 2"),
			},
		},
		{
			name: "entries from parent directories' .fleetignore files are added in lower directories",
			directoryStructure: fsNode{
				isDir: true,
				name:  "add-parent-entries",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     ".fleetignore",
						contents: "ignore-always.yaml",
					},
					{
						name:  "foo",
						isDir: true,
						children: []fsNode{
							{
								name:     "ignore-always.yaml",
								contents: "will be ignored",
							},
							{
								name:     "something.yaml",
								contents: "something",
							},
						},
					},
					{
						name:  "bar",
						isDir: true,
						children: []fsNode{
							{
								name:     ".fleetignore",
								contents: "something.yaml",
							},
							{
								name:     "something.yaml",
								contents: "something",
							},
							{
								name:     "something2.yaml",
								contents: "something2",
							},
							{
								name:     "ignore-always.yaml",
								contents: "will be ignored",
							},
						},
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml":      []byte("foo"),
				"foo/something.yaml":  []byte("something"),
				"bar/something2.yaml": []byte("something2"),
			},
		},
		{
			name: "root .fleetignore contains folder/* entries",
			directoryStructure: fsNode{
				isDir: true,
				name:  "root-fleetignore-all-files-in-dir",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     ".fleetignore",
						contents: "foo/*\n",
					},
					{
						name:  "foo",
						isDir: true,
						children: []fsNode{
							{
								name:     "ignore-always.yaml",
								contents: "will be ignored",
							},
							{
								name:     "something.yaml",
								contents: "will be ignored",
							},
						},
					},
					{
						name:  "bar",
						isDir: true,
						children: []fsNode{
							{
								name:     "something.yaml",
								contents: "something",
							},
							{
								name:     "something2.yaml",
								contents: "something2",
							},
							{
								name:  "foo",
								isDir: true,
								children: []fsNode{
									{
										name:     "ignore.yaml",
										contents: "will be ignored",
									},
									{
										name:     "ignore2.yaml",
										contents: "will be ignored",
									},
									{
										name:     "something.yaml",
										contents: "will be ignored",
									},
								},
							},
						},
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml":      []byte("foo"),
				"bar/something.yaml":  []byte("something"),
				"bar/something2.yaml": []byte("something2"),
			},
		},
		{
			name: "non root .fleetignore contains folder/* entries",
			directoryStructure: fsNode{
				isDir: true,
				name:  "non-root-fleetignore-all-files-in-dir",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:  "foo",
						isDir: true,
						children: []fsNode{
							{
								name:     "something1.yaml",
								contents: "something1",
							},
							{
								name:     "something2.yaml",
								contents: "something2",
							},
						},
					},
					{
						name:  "bar",
						isDir: true,
						children: []fsNode{
							{
								name:     "something.yaml",
								contents: "something",
							},
							{
								name:     "something2.yaml",
								contents: "something2",
							},
							{
								name:     ".fleetignore",
								contents: "foo/*\n",
							},
							{
								name:  "foo",
								isDir: true,
								children: []fsNode{
									{
										name:     "ignore.yaml",
										contents: "will be ignored",
									},
									{
										name:     "ignore2.yaml",
										contents: "will be ignored",
									},
									{
										name:     "something.yaml",
										contents: "will be ignored",
									},
								},
							},
						},
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml":      []byte("foo"),
				"foo/something1.yaml": []byte("something1"),
				"foo/something2.yaml": []byte("something2"),
				"bar/something.yaml":  []byte("something"),
				"bar/something2.yaml": []byte("something2"),
			},
		},
		{
			name: ".fleetignore contains folder/* entry does not apply to files",
			directoryStructure: fsNode{
				isDir: true,
				name:  "fleetignore-all-files-in-dir-does-not-apply-to-files",
				children: []fsNode{
					{
						name:     "something.yaml",
						contents: "foo",
					},
					{
						name:     ".fleetignore",
						contents: "foo/*\n",
					},
					{
						name:     "foo",
						contents: "everybody was a kung-foo fighting",
					},
					{
						name:  "bar",
						isDir: true,
						children: []fsNode{
							{
								name:     "something.yaml",
								contents: "something",
							},
							{
								name:     "something2.yaml",
								contents: "something2",
							},
							{
								name:     ".fleetignore",
								contents: "foo/*\n",
							},
							{
								name:  "foo",
								isDir: true,
								children: []fsNode{
									{
										name:     "ignore.yaml",
										contents: "will be ignored",
									},
									{
										name:     "ignore2.yaml",
										contents: "will be ignored",
									},
									{
										name:     "something.yaml",
										contents: "will be ignored",
									},
								},
							},
						},
					},
				},
			},
			expectedFiles: map[string][]byte{
				"something.yaml":      []byte("foo"),
				"foo":                 []byte("everybody was a kung-foo fighting"),
				"bar/something.yaml":  []byte("something"),
				"bar/something2.yaml": []byte("something2"),
			},
		},
	}

	base, err := os.MkdirTemp("", "test-fleet")
	require.NoError(t, err)

	defer os.RemoveAll(base)

	ignoreApplyConfigs := []string{"fleet.yaml", "chart/myvalues.yaml"}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {

			root := createDirStruct(t, base, c.directoryStructure)

			if c.source == "" {
				c.source = root
			}
			files, err := bundlereader.GetContent(context.Background(), root, c.source, "", c.auth, false, ignoreApplyConfigs)
			if c.expectedErr == nil {
				assert.NoError(t, err)
			} else {
				if !c.expectedErr.Match([]byte(err.Error())) {
					assert.Failf(t, "expected error to match", "expected: %s, got: %s", c.expectedErr.String(), err.Error())
				}
			}

			assert.Equal(t, len(c.expectedFiles), len(files))
			for k, v := range c.expectedFiles {
				assert.Equal(t, v, files[k])
			}
		})
	}
}

type authTester struct {
	t    *testing.T
	want string
}

func (a *authTester) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	url, err := r.URL.Parse(r.URL.String())
	if err != nil {
		http.Error(w, "Invalid URL", http.StatusBadRequest)
		return
	}
	if sskey := url.Query().Get("sshkey"); sskey != a.want {
		a.t.Errorf("wrong or no sshkey query parameter: want %s but got %s", a.want, sskey)
	}
	w.WriteHeader(http.StatusOK)
}

func TestGetContentSSHKey(t *testing.T) {
	cases := []struct {
		name, want string
		auth       bundlereader.Auth
	}{
		{
			name: "any URL with SSHPrivateKey set should be queried with sshkey query parameter",
			auth: bundlereader.Auth{
				SSHPrivateKey: []byte("foo"),
			},
			want: "Zm9v", // base64 encoding of "foo"
		},
		{
			name: "no query parameter if SSHPrivateKey is not set",
			auth: bundlereader.Auth{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			authTester := &authTester{t: t, want: c.want}
			s := httptest.NewServer(authTester)
			defer s.Close()

			base, err := os.MkdirTemp("", "test-fleet")
			require.NoError(t, err)
			defer os.RemoveAll(base)

			_, _ = bundlereader.GetContent(context.Background(), base, s.URL, "", c.auth, false, []string{})
		})
	}
}

func TestGetContentOCI(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		version string

		result      []string
		expectedErr string
	}{
		// Note: These tests rely on external hosts and DNS resolution.
		// We could just test the helm registry client is initialized
		// correctly, however for now these tests document different
		// scenarios nicely.
		{
			name:   "OCI URL without version",
			source: "oci://ghcr.io/rancher/fleet-test-configmap-chart",
			result: []string{
				"fleet-test-configmap-chart/Chart.yaml",
				"fleet-test-configmap-chart/values.yaml",
				"fleet-test-configmap-chart/templates/configmap.yaml",
			},
		},
		{
			name:    "OCI URL with version",
			source:  "oci://ghcr.io/rancher/fleet-test-configmap-chart",
			version: "0.1.0",
			result: []string{
				"fleet-test-configmap-chart/Chart.yaml",
				"fleet-test-configmap-chart/values.yaml",
				"fleet-test-configmap-chart/templates/configmap.yaml",
			},
		},
		{
			name:        "OCI URL with invalid version",
			source:      "oci://ghcr.io/rancher/fleet-test-configmap-chart",
			version:     "latest",
			expectedErr: "helm chart download: improper constraint: latest",
		},
		{
			name:        "Non-existing OCI URL without version",
			source:      "oci://non-existing-hostname/charts/chart",
			expectedErr: "dial tcp: lookup non-existing-hostname",
		},
		{
			name:        "Non-existing OCI URL with invalid version",
			source:      "oci://non-existing-hostname/charts/chart",
			version:     "latest",
			expectedErr: "dial tcp: lookup non-existing-hostname",
		},
		{
			name:        "OCI URL which includes version too",
			source:      "oci://ghcr.io/rancher/fleet-test-configmap-chart:1234.0",
			version:     "1.0",
			expectedErr: "chart reference and version mismatch: 1.0 is not 1234.0",
		},
		{
			name:        "Non-existing OCI URL with valid semver",
			source:      "oci://non-existing-hostname/charts/chart",
			version:     "1.0",
			expectedErr: "helm chart download: failed to perform",
		},
	}

	assert := assert.New(t)

	base, err := os.MkdirTemp("", "test-fleet")
	require.NoError(t, err)

	defer os.RemoveAll(base)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, err := bundlereader.GetContent(context.Background(), base, c.source, c.version, bundlereader.Auth{}, false, []string{})
			if c.expectedErr == "" {
				assert.NoError(err)
				for k := range result {
					assert.Contains(c.result, k)
				}
			} else {
				assert.ErrorContains(err, c.expectedErr, c.name)
			}
		})
	}
}

// createDirStruct generates and populates a directory structure which root is node, placing it at basePath.
func createDirStruct(t *testing.T, basePath string, node fsNode) string {
	path := filepath.Join(basePath, node.name)

	if !node.isDir {
		f, err := os.Create(path)
		require.NoError(t, err)

		_, err = io.WriteString(f, node.contents)
		require.NoError(t, err)

		return ""
	}

	err := os.Mkdir(path, 0777)
	require.NoError(t, err)

	for _, c := range node.children {
		createDirStruct(t, path, c)
	}

	return path
}

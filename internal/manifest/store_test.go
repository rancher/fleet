package manifest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/rancher/wrangler/v2/pkg/generic/fake"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func testManifest(t *testing.T, data string) *Manifest {
	t.Helper()

	m, err := FromJSON([]byte(data), "")
	if err != nil {
		t.Fatal(err)
	}
	if c, err := m.Content(); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal([]byte(data), c) {
		t.Fatalf("Created manifest does not match the original payload")
	}

	return m
}

func Test_contentStore_Store(t *testing.T) {
	resources := `{"resources": [{"name": "foo", "content": "bar"}]}`
	checksum := "752ebbb975f52eea5e87950ef2ca5de4055de3c68a17f54d94527d7fd79c21fd"
	type args struct {
		manifest *Manifest
		cached   bool
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "new manifest",
			args: args{
				manifest: testManifest(t, resources),
			},
			want: toSHA256ID(checksum),
		},
		{
			name: "existing manifest",
			args: args{
				manifest: testManifest(t, resources),
				cached:   true,
			},
			want: toSHA256ID(checksum),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			cache := fake.NewMockNonNamespacedCacheInterface[*fleet.Content](ctrl)
			client := fake.NewMockNonNamespacedClientInterface[*fleet.Content, *fleet.ContentList](ctrl)
			store := &contentStore{cache, client}

			if tt.args.cached {
				cache.EXPECT().Get(tt.want).Return(nil, nil)
				client.EXPECT().Create(gomock.Any()).Times(0)
			} else {
				cache.EXPECT().Get(tt.want).Return(nil, apierrors.NewNotFound(fleet.Resource("Content"), tt.want))
				client.EXPECT().Create(&contentMatcher{
					name:      tt.want,
					sha256sum: checksum,
				}).Times(1)
			}

			got, err := store.Store(tt.args.manifest)
			if err != nil {
				t.Errorf("Store() error = %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("Store() got = %v, want %v", got, tt.want)
			}
		})
	}
}

type contentMatcher struct {
	name      string
	sha256sum string
}

func (m contentMatcher) Matches(x interface{}) bool {
	content, ok := x.(*fleet.Content)
	if !ok {
		return false
	}
	if m.name != "" && m.name != content.Name {
		return false
	}
	if m.sha256sum != "" && m.sha256sum != content.SHA256Sum {
		return false
	}
	return true
}

func (m contentMatcher) String() string {
	var s []string
	if m.name != "" {
		s = append(s, "name is "+m.name)
	}
	if m.sha256sum != "" {
		s = append(s, "sha256sum is "+m.sha256sum)
	}
	return strings.Join(s, ";")
}

package manifest_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/rancher/fleet/internal/manifest"
	"github.com/rancher/fleet/internal/mocks"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func testManifest(t *testing.T, data string) *manifest.Manifest {
	t.Helper()

	m, err := manifest.FromJSON([]byte(data), "")
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
		manifest *manifest.Manifest
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
			want: manifest.ToSHA256ID(checksum),
		},
		{
			name: "existing manifest",
			args: args{
				manifest: testManifest(t, resources),
				cached:   true,
			},
			want: manifest.ToSHA256ID(checksum),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := mocks.NewMockClient(ctrl)

			store := &manifest.ContentStore{client}
			ctx := context.TODO()
			nsn := types.NamespacedName{Name: tt.want}

			if tt.args.cached {
				client.EXPECT().Get(ctx, nsn, gomock.Any()).Return(nil)
				client.EXPECT().Create(ctx, gomock.Any()).Times(0)
			} else {
				client.EXPECT().Get(ctx, nsn, gomock.Any()).Return(apierrors.NewNotFound(fleet.GroupResource("Content"), tt.want))
				client.EXPECT().Create(ctx, &contentMatcher{
					name:      tt.want,
					sha256sum: checksum,
				}).Times(1)
			}

			err := store.Store(ctx, tt.args.manifest)
			if err != nil {
				t.Errorf("Store() error = %v", err)
				return
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

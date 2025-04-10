package helmvalues_test

import (
	"testing"

	"github.com/rancher/fleet/internal/helmvalues"
	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func TestExtractValues(t *testing.T) {
	type args struct {
		bundle *fleet.Bundle
	}
	tests := []struct {
		name          string
		args          args
		wantHash      string
		wantData      map[string][]byte
		wantErr       bool
		wantErrString string
	}{
		{
			name: "nil helm options",
			args: args{
				bundle: &fleet.Bundle{Spec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Helm: nil,
					},
					Targets: []fleet.BundleTarget{
						{
							Name: "target",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Helm: nil,
							},
						},
					},
				}},
			},
			wantHash: "",
			wantData: map[string][]byte{},
			wantErr:  false,
		},
		{
			name: "nil values",
			args: args{
				bundle: &fleet.Bundle{Spec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: nil,
						},
					},
					Targets: []fleet.BundleTarget{
						{
							Name: "target",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Helm: &fleet.HelmOptions{
									Values: nil,
								},
							},
						},
					},
				}},
			},
			wantHash: "",
			wantData: map[string][]byte{},
			wantErr:  false,
		},
		{
			name: "empty values",
			args: args{
				bundle: &fleet.Bundle{Spec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: &fleet.GenericMap{},
						},
					},
					Targets: []fleet.BundleTarget{
						{
							Name: "target",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Helm: &fleet.HelmOptions{
									Values: &fleet.GenericMap{},
								},
							},
						},
					},
				}},
			},
			wantHash: "",
			wantData: map[string][]byte{},
			wantErr:  false,
		},
		{
			name: "values present",
			args: args{
				bundle: &fleet.Bundle{Spec: fleet.BundleSpec{
					BundleDeploymentOptions: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: &fleet.GenericMap{
								Data: map[string]interface{}{"key": "value"},
							},
						},
					},
					Targets: []fleet.BundleTarget{
						{
							Name: "target",
							BundleDeploymentOptions: fleet.BundleDeploymentOptions{
								Helm: &fleet.HelmOptions{
									Values: &fleet.GenericMap{
										Data: map[string]interface{}{"newkey": "value"},
									},
								},
							},
						},
					},
				}},
			},

			wantHash: "17e05a97acde825d03ca37c2b8fc1aecf3f8f9fde28c21702492c56d4d4a68f1",
			wantData: map[string][]byte{
				"values.yaml": []byte(`{"key":"value"}`),
				"target":      []byte(`{"newkey":"value"}`)},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		h, d, err := helmvalues.ExtractValues(tt.args.bundle)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: error = %v, wantErr %v", tt.name, err, tt.wantErr)
			return
		}
		if err != nil && err.Error() != tt.wantErrString {
			t.Errorf("%s: error = %s, wantErrString %v", tt.name, err, tt.wantErrString)
			return
		}
		if h != tt.wantHash {
			t.Errorf("%s: hash = %s, want %s", tt.name, h, tt.wantHash)
		}

		if len(d) != len(tt.wantData) {
			t.Errorf("%s: data = %v, want %v", tt.name, d, tt.wantData)
		}
		for k, v := range d {
			if string(v) != string(tt.wantData[k]) {
				t.Errorf("%s: data[%s] = %s, want %s", tt.name, k, v, tt.wantData[k])
			}
		}
	}
}

func TestExtractOptions(t *testing.T) {
	type args struct {
		bd *fleet.BundleDeployment
	}
	var nullMap *fleet.GenericMap
	tests := []struct {
		name          string
		args          args
		wantOptions   []byte
		wantStaged    []byte
		wantHash      string
		wantErr       bool
		wantErrString string
	}{
		{
			name: "nil helm options",
			args: args{
				bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: nil,
					},
				}},
			},
			wantOptions: []byte{},
			wantStaged:  []byte{},
			wantHash:    "",
			wantErr:     false,
		},
		{
			name: "nil values",
			args: args{
				bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{},
					},
					StagedOptions: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							TakeOwnership: true,
						},
					},
				}},
			},
			wantOptions: []byte{},
			wantStaged:  []byte{},
			wantHash:    "",
			wantErr:     false,
		},
		{
			name: "null values",
			args: args{
				bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: nullMap,
						},
					},
					StagedOptions: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							TakeOwnership: true,
							Values:        &fleet.GenericMap{},
						},
					},
				}},
			},
			wantOptions: []byte{},
			wantStaged:  []byte{},
			wantHash:    "",
			wantErr:     false,
		},
		{
			name: "empty values",
			args: args{
				bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: &fleet.GenericMap{Data: map[string]interface{}{}},
						},
					},
					StagedOptions: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							TakeOwnership: true,
							Values:        &fleet.GenericMap{Data: map[string]interface{}{}},
						},
					},
				}},
			},
			wantOptions: []byte(""),
			wantStaged:  []byte(""),
			wantHash:    "",
			wantErr:     false,
		},
		{
			name: "values present",
			args: args{
				bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: &fleet.GenericMap{
								Data: map[string]interface{}{"key": "value"},
							},
						},
					},
				}},
			},
			wantOptions: []byte(`{"key":"value"}`),
			wantStaged:  []byte{},
			wantHash:    "e43abcf3375244839c012f9633f95862d232a95b00d5bc7348b3098b9fed7f32",
			wantErr:     false,
		},
		{
			name: "values and staged present",
			args: args{
				bd: &fleet.BundleDeployment{Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: &fleet.GenericMap{
								Data: map[string]interface{}{"key": "value"},
							},
						},
					},
					StagedOptions: fleet.BundleDeploymentOptions{
						Helm: &fleet.HelmOptions{
							Values: &fleet.GenericMap{
								Data: map[string]interface{}{"newkey": "value"},
							},
						},
					},
				}},
			},
			wantOptions: []byte(`{"key":"value"}`),
			wantStaged:  []byte(`{"newkey":"value"}`),
			wantHash:    "01c44d8a446abccb870503db292e07cb2b8da135b6fec52b21048bdab8c84a7c",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		h, o, s, err := helmvalues.ExtractOptions(tt.args.bd)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: error = %v, wantErr %v", tt.name, err, tt.wantErr)
			return
		}
		if err != nil && err.Error() != tt.wantErrString {
			t.Errorf("%s: error = %s, wantErrString %v", tt.name, err, tt.wantErrString)
			return
		}
		if string(o) != string(tt.wantOptions) {
			t.Errorf("%s: options = %q, want %q", tt.name, o, tt.wantOptions)
		}
		if string(s) != string(tt.wantStaged) {
			t.Errorf("%s: staged = %q, want %q", tt.name, s, tt.wantStaged)
		}
		if h != tt.wantHash {
			t.Errorf("%s: hash = %q, want %q", tt.name, h, tt.wantHash)
		}
	}
}

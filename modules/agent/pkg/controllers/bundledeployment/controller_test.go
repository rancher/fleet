package bundledeployment

import (
	"fmt"
	"testing"
	"time"

	fleet "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
)

func Test_isWithinWindow(t *testing.T) {
	now := time.Now()
	currentHour := now.Hour()

	type args struct {
		schedule fleet.Schedule
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "Within window",
			args: args{
				schedule: fleet.Schedule{
					Cron:     fmt.Sprintf("0 %d * * *", currentHour),
					Duration: "1h",
				},
			},
			want:    true,
			wantErr: false,
		},
		{
			name: "Not within window",
			args: args{
				schedule: fleet.Schedule{
					Cron:     fmt.Sprintf("0 %d * * *", currentHour+1),
					Duration: "1h",
				},
			},
			want:    false,
			wantErr: false,
		},
		{
			name: "Bad cron format",
			args: args{
				schedule: fleet.Schedule{
					Cron:     "toe",
					Duration: "1h",
				},
			},
			want:    false,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bd := &fleet.BundleDeployment{
				Spec: fleet.BundleDeploymentSpec{
					Options: fleet.BundleDeploymentOptions{
						Schedule: &tt.args.schedule,
					},
				},
			}
			got, err := isWithinScheduleWindow(bd)
			if (err != nil) != tt.wantErr {
				t.Errorf("isWithinWindow() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("isWithinWindow() = %v, want %v", got, tt.want)
			}
		})
	}
}

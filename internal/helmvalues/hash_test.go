package helmvalues_test

import (
	"testing"

	"github.com/rancher/fleet/internal/helmvalues"
)

func TestHashOptions(t *testing.T) {
	type args struct {
		bytes [][]byte
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "empty",
			args: args{
				bytes: [][]byte{},
			},
			want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "empty string",
			args: args{
				bytes: [][]byte{[]byte("")},
			},
			want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "single word",
			args: args{
				bytes: [][]byte{
					[]byte("test"),
				},
			},
			want: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		},
		{
			name: "empty options",
			args: args{
				bytes: [][]byte{
					[]byte(""), []byte(""),
				},
			},
			want: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			name: "empty option",
			args: args{
				bytes: [][]byte{
					[]byte("{}"),
				},
			},
			want: "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
		},
		{
			name: "empty options",
			args: args{
				bytes: [][]byte{
					[]byte("{}"), []byte("{}"),
				},
			},
			want: "b51f08b698d88d8027a935d9db649774949f5fb41a0c559bfee6a9a13225c72d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := helmvalues.HashOptions(tt.args.bytes...); got != tt.want {
				t.Errorf("HashOptions() = %v, want %v", got, tt.want)
			}
		})
	}
}

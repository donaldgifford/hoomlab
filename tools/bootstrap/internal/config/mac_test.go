package config

import "testing"

func TestNormalizeMAC(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "canonical passes through", in: "02:50:99:a2:00:01", want: "02:50:99:a2:00:01"},
		{name: "uppercase lowered", in: "02:50:99:A2:00:01", want: "02:50:99:a2:00:01"},
		{name: "dashes to colons", in: "02-50-99-a2-00-01", want: "02:50:99:a2:00:01"},
		{name: "cisco dot groups", in: "0250.99a2.0001", want: "02:50:99:a2:00:01"},
		{name: "not a mac", in: "not-a-mac", wantErr: true},
		{name: "empty", in: "", wantErr: true},
		{name: "eui-64 rejected", in: "02:50:99:a2:00:01:02:03", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeMAC(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeMAC(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeMAC(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeMAC(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

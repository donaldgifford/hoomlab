package config

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func TestEnvFunc(t *testing.T) {
	env := map[string]string{
		"SET":   "value",
		"EMPTY": "",
	}
	fn := envFunc(func(name string) (string, bool) {
		v, ok := env[name]
		return v, ok
	})

	tests := []struct {
		name    string
		arg     string
		want    string
		wantErr string
	}{
		{name: "set", arg: "SET", want: "value"},
		{name: "unset", arg: "MISSING", wantErr: "environment variable MISSING is not set"},
		{name: "empty", arg: "EMPTY", wantErr: "environment variable EMPTY is set but empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fn.Call([]cty.Value{cty.StringVal(tt.arg)})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("env(%q) = %v, want error %q", tt.arg, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("env(%q) error = %q, want it to contain %q", tt.arg, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("env(%q) error: %v", tt.arg, err)
			}
			if got.AsString() != tt.want {
				t.Errorf("env(%q) = %q, want %q", tt.arg, got.AsString(), tt.want)
			}
		})
	}
}

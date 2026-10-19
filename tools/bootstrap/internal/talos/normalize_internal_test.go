package talos

import (
	"strings"
	"testing"
)

const (
	v1alpha1Doc = `version: v1alpha1
machine:
  type: worker
  token: supersecret
  install:
    image: factory.talos.dev/installer/abc:v1.13.11
cluster:
  clusterName: homelab
`
	hostnameDoc = `apiVersion: v1alpha1
kind: HostnameConfig
hostname: w1
`
)

func multiDoc(docs ...string) []byte { return []byte(strings.Join(docs, "---\n")) }

func TestConfigsEqual(t *testing.T) {
	t.Parallel()
	base := multiDoc(v1alpha1Doc, hostnameDoc)
	tests := []struct {
		name  string
		other []byte
		want  bool
	}{
		{"identical", base, true},
		{"reordered documents", multiDoc(hostnameDoc, v1alpha1Doc), true},
		{"comments and spacing", multiDoc("# live\n"+v1alpha1Doc, hostnameDoc), true},
		{"changed field", multiDoc(strings.ReplaceAll(v1alpha1Doc, "abc:", "def:"), hostnameDoc), false},
		{"missing document", multiDoc(v1alpha1Doc), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := configsEqual(base, tt.other)
			if err != nil {
				t.Fatalf("configsEqual: %v", err)
			}
			if got != tt.want {
				t.Errorf("configsEqual = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfigsEqualRejectsGarbage(t *testing.T) {
	t.Parallel()
	if _, err := configsEqual([]byte("{{ not yaml"), multiDoc(v1alpha1Doc)); err == nil {
		t.Error("configsEqual on an unparsable config must fail")
	}
}

func TestConfigDiff(t *testing.T) {
	t.Parallel()
	live := multiDoc(v1alpha1Doc, hostnameDoc)
	rendered := multiDoc(strings.ReplaceAll(v1alpha1Doc, "abc:", "def:"),
		strings.ReplaceAll(hostnameDoc, "w1", "w2"))

	diff, err := configDiff(live, rendered)
	if err != nil {
		t.Fatalf("configDiff: %v", err)
	}
	for _, want := range []string{
		"@@ HostnameConfig\n",
		"- hostname: w1\n",
		"+ hostname: w2\n",
		"installer/abc:v1.13.11",
		"installer/def:v1.13.11",
	} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff missing %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "supersecret") {
		t.Errorf("diff leaks a secret:\n%s", diff)
	}

	same, err := configDiff(live, multiDoc(hostnameDoc, v1alpha1Doc))
	if err != nil {
		t.Fatalf("configDiff (reordered): %v", err)
	}
	if same != "" {
		t.Errorf("reordered documents must diff empty, got:\n%s", same)
	}
}

func TestLineDiffContext(t *testing.T) {
	t.Parallel()
	live := "a\nb\nc\nd\ne\nf\ng\n"
	rendered := "a\nb\nc\nd\nX\nf\ng\n"
	want := "  …\n  c\n  d\n- e\n+ X\n  f\n  g\n"
	if got := lineDiff(live, rendered); got != want {
		t.Errorf("lineDiff =\n%q\nwant\n%q", got, want)
	}
}

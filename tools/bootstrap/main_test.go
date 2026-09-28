package main

import (
	"runtime/debug"
	"testing"
)

func TestBuildInfo(t *testing.T) {
	vcs := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.time", Value: "2026-09-27T12:00:00Z"},
	}
	tests := []struct {
		name                string
		stamped             [3]string
		info                *debug.BuildInfo
		ok                  bool
		wantV, wantC, wantD string
	}{
		{
			name:    "go install from the proxy",
			stamped: [3]string{defaultVersion, defaultCommit, defaultDate},
			info:    &debug.BuildInfo{Main: debug.Module{Version: "v0.3.0"}},
			ok:      true,
			wantV:   "v0.3.0", wantC: defaultCommit, wantD: defaultDate,
		},
		{
			name:    "local build records vcs",
			stamped: [3]string{defaultVersion, defaultCommit, defaultDate},
			info:    &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs},
			ok:      true,
			wantV:   defaultVersion, wantC: "abc123", wantD: "2026-09-27T12:00:00Z",
		},
		{
			name:    "ldflags stamp wins",
			stamped: [3]string{"v0.3.0", "fff999", "2026-09-02"},
			info:    &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}, Settings: vcs},
			ok:      true,
			wantV:   "v0.3.0", wantC: "fff999", wantD: "2026-09-02",
		},
		{
			name:    "no build info",
			stamped: [3]string{defaultVersion, defaultCommit, defaultDate},
			wantV:   defaultVersion, wantC: defaultCommit, wantD: defaultDate,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, commit, date = tt.stamped[0], tt.stamped[1], tt.stamped[2]
			t.Cleanup(func() { version, commit, date = defaultVersion, defaultCommit, defaultDate })

			v, c, d := buildInfo(tt.info, tt.ok)
			if v != tt.wantV || c != tt.wantC || d != tt.wantD {
				t.Errorf("buildInfo() = %q, %q, %q; want %q, %q, %q",
					v, c, d, tt.wantV, tt.wantC, tt.wantD)
			}
		})
	}
}

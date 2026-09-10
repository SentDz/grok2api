package egress

import (
	"testing"
	"time"
)

func TestProbeIntervalDisabledAndLargeValues(t *testing.T) {
	maxSeconds := int(^uint(0) >> 1)
	longDuration := time.Duration(1<<63 - 1)
	if int64(maxSeconds) <= int64(longDuration/time.Second) {
		longDuration = time.Duration(maxSeconds) * time.Second
	}
	for _, tc := range []struct {
		seconds int
		want    time.Duration
	}{
		{0, 0}, {60, time.Minute}, {86401, 86401 * time.Second},
		{maxSeconds, longDuration},
	} {
		if got := (OperationsConfig{ProbeIntervalSeconds: tc.seconds}).ProbeInterval(); got != tc.want {
			t.Fatalf("seconds=%d duration=%s want=%s", tc.seconds, got, tc.want)
		}
	}
}

func TestSupportsScopePreservesPrimaryAndResourceCompatibility(t *testing.T) {
	tests := []struct {
		name               string
		nodeScope, request Scope
		want               bool
	}{
		{name: "Web serves submission", nodeScope: ScopeWeb, request: ScopeWebSubmit, want: true},
		{name: "submission serves submission", nodeScope: ScopeWebSubmit, request: ScopeWebSubmit, want: true},
		{name: "submission excludes uploads", nodeScope: ScopeWebSubmit, request: ScopeWeb, want: false},
		{name: "submission excludes downloads", nodeScope: ScopeWebSubmit, request: ScopeWebAsset, want: false},
		{name: "submission excludes Console", nodeScope: ScopeWebSubmit, request: ScopeConsole, want: false},
		{name: "exact Console asset", nodeScope: ScopeConsoleAsset, request: ScopeConsoleAsset, want: true},
		{name: "Console serves Console asset", nodeScope: ScopeConsole, request: ScopeConsoleAsset, want: true},
		{name: "Web serves Console asset", nodeScope: ScopeWeb, request: ScopeConsoleAsset, want: true},
		{name: "Web serves Web asset", nodeScope: ScopeWeb, request: ScopeWebAsset, want: true},
		{name: "Console asset does not serve primary Console", nodeScope: ScopeConsoleAsset, request: ScopeConsole, want: false},
		{name: "Web asset does not serve primary Web", nodeScope: ScopeWebAsset, request: ScopeWeb, want: false},
		{name: "Build remains isolated", nodeScope: ScopeBuild, request: ScopeConsoleAsset, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SupportsScope(test.nodeScope, test.request); got != test.want {
				t.Fatalf("SupportsScope(%q, %q) = %v, want %v", test.nodeScope, test.request, got, test.want)
			}
		})
	}
}

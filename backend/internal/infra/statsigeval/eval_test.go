package statsigeval

import (
	"context"
	"encoding/json"
	"github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"os"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--statsig-eval" {
		if Worker(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fixture(t *testing.T) statsig.Sample {
	t.Helper()
	data, err := os.ReadFile("../provider/web/testdata/statsig_live_pair.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample statsig.Sample
	if err = json.Unmarshal(data, &sample); err != nil {
		t.Fatal(err)
	}
	return sample
}

func TestSandboxRealFixtureAndIsolation(t *testing.T) {
	sample := fixture(t)
	value, err := Run(context.Background(), DefaultCode, sample)
	if err != nil || value != sample.HEX {
		t.Fatalf("fixture mismatch: %v %q", err, value)
	}
	for _, code := range []string{
		`function computeHex(){return process.env.SECRET;}`,
		`function computeHex(){return require('fs').readFileSync('/etc/passwd');}`,
		`function computeHex(){while(true){}}`,
		`function computeHex(){return 'not-hex';}`,
	} {
		start := time.Now()
		if _, err := Run(context.Background(), code, sample); err == nil {
			t.Fatalf("unsafe code accepted: %s", code)
		}
		if time.Since(start) > 4*time.Second {
			t.Fatal("sandbox deadline not enforced")
		}
	}
}

package web

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestGenerateLocalStatsigIsSelfConsistent(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC).Unix()
	value, err := generateLocalStatsig("post", "/rest/app-chat/conversations/new", now)
	if err != nil {
		t.Fatal(err)
	}
	if !validStatsigID(value) {
		t.Fatalf("generated id failed shape check: %q", value)
	}
	raw, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(raw) != 70 {
		t.Fatalf("decode = %d %v", len(raw), err)
	}
	key := raw[0]
	seed, hex, err := currentStatsigPair()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 48; i++ {
		if raw[1+i]^key != seed[i] {
			t.Fatalf("seed byte %d mismatch", i)
		}
	}
	number := uint32(raw[49]^key) | uint32(raw[50]^key)<<8 | uint32(raw[51]^key)<<16 | uint32(raw[52]^key)<<24
	if number != uint32(now-statsigEpoch) {
		t.Fatalf("number = %d", number)
	}
	input := "POST!/rest/app-chat/conversations/new!" + strconv.FormatUint(uint64(number), 10) + statsigSalt + hex
	sum := sha256.Sum256([]byte(input))
	for i := 0; i < 16; i++ {
		if raw[53+i]^key != sum[i] {
			t.Fatalf("sha byte %d mismatch", i)
		}
	}
	if raw[69]^key != statsigMark {
		t.Fatalf("mark = %d", raw[69]^key)
	}
}

func TestGenerateLocalStatsigUsesFreshKey(t *testing.T) {
	now := time.Now().Unix()
	first, err := generateLocalStatsig(httpMethodPost, "/rest/app-chat/conversations/new", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateLocalStatsig(httpMethodPost, "/rest/app-chat/conversations/new", now)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("expected random xor key to change the encoded id")
	}
}

const httpMethodPost = "POST"

func TestSignStatsigWithMetaUsesRequestSeedAndLiveCurves(t *testing.T) {
	raw, err := os.ReadFile("testdata/statsig_live_pair.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Seed  string   `json:"seed"`
		HEX   string   `json:"hex"`
		Paths []string `json:"paths"`
	}
	if json.Unmarshal(raw, &fixture) != nil || len(fixture.Paths) < 4 {
		t.Fatal("fixture invalid")
	}
	var paths [4]string
	copy(paths[:], fixture.Paths[:4])
	statsigSVGMu.RLock()
	previous := statsigSVGPaths
	statsigSVGMu.RUnlock()
	if replaceStatsigSVGPaths(paths[:]) != 4 {
		t.Fatal("failed to install live curves")
	}
	t.Cleanup(func() {
		replaceStatsigSVGPaths(previous[:])
	})
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC).Unix()
	value, err := signStatsigWithMeta(httpMethodPost, "/rest/app-chat/conversations/new", fixture.Seed, now)
	if err != nil {
		t.Fatal(err)
	}
	if !validStatsigID(value) {
		t.Fatalf("id = %q", value)
	}
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	key := decoded[0]
	seed, err := decodeStatsigSeed(fixture.Seed)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 48; i++ {
		if decoded[1+i]^key != seed[i] {
			t.Fatalf("seed byte %d mismatch", i)
		}
	}
	want, err := buildLocalStatsig(seed, fixture.HEX, httpMethodPost, "/rest/app-chat/conversations/new", now)
	if err != nil {
		t.Fatal(err)
	}
	wantRaw, _ := base64.RawStdEncoding.DecodeString(want)
	// Compare SHA payload bytes (53-68) after XOR-out; random key differs.
	for i := 53; i < 69; i++ {
		if decoded[i]^key != wantRaw[i]^wantRaw[0] {
			t.Fatalf("sha/hex payload byte %d mismatch", i)
		}
	}
}

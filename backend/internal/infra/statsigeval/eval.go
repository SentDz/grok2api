package statsigeval

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime/debug"
	"time"

	"github.com/chenyme/grok2api/backend/internal/domain/statsig"
	"github.com/dop251/goja"
)

//go:embed default.js
var DefaultCode string

type Input struct {
	Code   string         `json:"code"`
	Sample statsig.Sample `json:"sample"`
}

// Run executes only pure JavaScript in a separate Go process, without host APIs,
// inherited credentials, filesystem bindings or a JavaScript module loader.
func Run(ctx context.Context, code string, sample statsig.Sample) (string, error) {
	if len(code) > 64<<10 {
		return "", errors.New("signature code exceeds 64 KiB")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(Input{code, sample})
	cmd := exec.CommandContext(ctx, executable, "--statsig-eval")
	cmd.Env = []string{"GOMEMLIMIT=64MiB", "GOMAXPROCS=1"}
	cmd.Stdin = bytes.NewReader(body)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", errors.New("signature sandbox failed or exceeded its time limit")
	}
	var value string
	if stdout.Len() > 8192 || json.Unmarshal(stdout.Bytes(), &value) != nil || len(value) == 0 || len(value) > 4096 {
		return "", errors.New("invalid signature sandbox result")
	}
	return value, nil
}

func Worker(input io.Reader, output io.Writer) error {
	if err := limitWorkerMemory(); err != nil {
		return err
	}
	debug.SetMemoryLimit(64 << 20)
	var value Input
	if err := json.NewDecoder(io.LimitReader(input, 256<<10)).Decode(&value); err != nil {
		return err
	}
	result, err := Evaluate(value)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}

func Evaluate(input Input) (string, error) {
	if len(input.Code) > 64<<10 || len(input.Sample.Paths) != 4 {
		return "", errors.New("invalid sandbox input")
	}
	seed, err := base64.RawStdEncoding.DecodeString(input.Sample.Seed)
	if err != nil {
		seed, err = base64.StdEncoding.DecodeString(input.Sample.Seed)
	}
	if err != nil || len(seed) != 48 {
		return "", errors.New("invalid seed")
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(128)
	timer := time.AfterFunc(200*time.Millisecond, func() { vm.Interrupt("execution deadline") })
	defer timer.Stop()
	// Values are JSON literals, never host objects or functions.
	seedValues := make([]int, len(seed))
	for i, b := range seed {
		seedValues[i] = int(b)
	}
	args, _ := json.Marshal([]any{seedValues, input.Sample.Paths})
	result, err := vm.RunString("'use strict';\n" + input.Code + "\n;computeHex.apply(null," + string(args) + ")")
	if err != nil {
		return "", errors.New("signature JavaScript failed validation")
	}
	hex := result.String()
	if len(hex) == 0 || len(hex) > 4096 {
		return "", errors.New("invalid HEX size")
	}
	for _, c := range hex {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", errors.New("invalid HEX")
		}
	}
	return hex, nil
}

//go:build linux

package statsigeval

import "syscall"

func limitWorkerMemory() error {
	// A hard data-segment limit complements the Go GC target for hostile allocations.
	return syscall.Setrlimit(syscall.RLIMIT_DATA, &syscall.Rlimit{Cur: 256 << 20, Max: 256 << 20})
}

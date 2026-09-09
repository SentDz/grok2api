//go:build !linux

package statsigeval

func limitWorkerMemory() error { return nil }

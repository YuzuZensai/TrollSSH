//go:build unix

package main

import (
	"os"
	"syscall"
)

func readFrameFile(filename string) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size <= 0 || size != int64(int(size)) {
		return os.ReadFile(filename)
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return os.ReadFile(filename)
	}
	return data, nil
}

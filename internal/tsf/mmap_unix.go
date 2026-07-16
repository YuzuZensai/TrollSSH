//go:build unix

package tsf

import (
	"os"
	"syscall"
)

func readFrameFile(filename string) (*frameFile, error) {
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
		data, err := os.ReadFile(filename)
		return &frameFile{data: data}, err
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(size), syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		data, err := os.ReadFile(filename)
		return &frameFile{data: data}, err
	}
	return &frameFile{
		data: data,
		cleanup: func() error {
			return syscall.Munmap(data)
		},
	}, nil
}

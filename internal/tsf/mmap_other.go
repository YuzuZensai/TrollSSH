//go:build !unix

package tsf

import "os"

func readFrameFile(filename string) (*frameFile, error) {
	data, err := os.ReadFile(filename)
	return &frameFile{data: data}, err
}

func (f *frameFile) dropResident() {}

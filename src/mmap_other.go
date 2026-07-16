//go:build !unix

package main

import "os"

func readFrameFile(filename string) (*frameFile, error) {
	data, err := os.ReadFile(filename)
	return &frameFile{data: data}, err
}

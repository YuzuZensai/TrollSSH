//go:build !unix

package main

import "os"

func readFrameFile(filename string) ([]byte, error) {
	return os.ReadFile(filename)
}

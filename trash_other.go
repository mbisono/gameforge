//go:build !windows || !(amd64 || arm64)

package main

import "os"

// removeOriginal deletes a file.
func removeOriginal(path string) error { return os.Remove(path) }

// usesTrash says whether removed files go to the Recycle Bin.
const usesTrash = false

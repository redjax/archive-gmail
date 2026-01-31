package utils

import (
	"os"
)

func EnsureDir(path string, dry bool) error {
	if dry {
		return nil
	}

	return os.MkdirAll(path, 0755)
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

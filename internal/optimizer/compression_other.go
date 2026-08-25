//go:build !windows

package optimizer

import "errors"

func windowsCompressionSupported() bool { return false }

func compressWindowsArchive(string) (string, error) {
	return "", errors.New("Windows filesystem compression is only available on Windows")
}

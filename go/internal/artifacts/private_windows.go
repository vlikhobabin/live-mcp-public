//go:build windows

package artifacts

import "os"

// artifactDirectoryIsPrivate has no POSIX ownership/mode meaning on Windows;
// access is governed by the directory ACL inherited from the cache root.
// Confinement is still enforced by the os.Root handle chain.
func artifactDirectoryIsPrivate(os.FileInfo) bool {
	return true
}

// setArtifactFileMode is a no-op on Windows: os.Root and O_EXCL provide the
// exclusive-create guarantee, while POSIX mode 0600 has no Windows analogue.
func setArtifactFileMode(*os.File) error {
	return nil
}

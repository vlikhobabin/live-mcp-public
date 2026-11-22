//go:build !unix && !windows

package artifacts

import (
	"errors"
	"os"
)

// artifactDirectoryIsPrivate cannot verify POSIX ownership on this platform,
// so it refuses to publish rather than falling back to an unverified write.
func artifactDirectoryIsPrivate(os.FileInfo) bool {
	return false
}

func setArtifactFileMode(*os.File) error {
	return errors.New("private artifact file modes are not supported here")
}

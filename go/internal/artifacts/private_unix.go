//go:build unix

package artifacts

import (
	"os"
	"syscall"
)

// artifactDirectoryIsPrivate mirrors the Python artifact-directory guard: an
// existing dcs-artifacts directory must be owned by the current user and must
// not be group/other-writable. A pre-existing 0755 directory is accepted;
// 0775/0777 (or a foreign owner) is rejected because another user could race
// directory entries. The directory is never chmod-ed here.
func artifactDirectoryIsPrivate(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if int(stat.Uid) != os.Geteuid() {
		return false
	}
	return info.Mode().Perm()&0o022 == 0
}

// setArtifactFileMode pins the created file to 0600 even when a permissive
// umask was inherited.
func setArtifactFileMode(file *os.File) error {
	return file.Chmod(artifactFileMode)
}

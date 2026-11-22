// Package artifacts strictly validates and exclusively persists AIDataRead
// XLSX artifacts.
//
// The HTTP service sends the artifact inside JSON as Base64 with a Base64
// SHA-256 digest and a size. The MCP host never trusts the suggested
// fileName or any server-provided path: it verifies the bytes, generates its
// own name under the configured cache root, creates the file exclusively, and
// only then replaces contentBase64 with the client-local path.
//
// Directory and file operations are anchored to an os.Root handle: the
// explicitly configured CACHE_ROOT parent is resolved/pinned, the leaf and the
// dcs-artifacts directory are opened (or privately created) relative to it, and
// the file is created relative to the child handle with O_EXCL. The CACHE_ROOT
// leaf and dcs-artifacts must be real directories and are never links; parent
// aliases of the operator-provided path are trusted and may be followed. There
// is no path-based fallback: when a safe anchored write cannot be established
// the artifact stays unpublished and a diagnostic is returned.
package artifacts

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// ArtifactDirName is the only directory below the configured cache root
	// in which generated artifacts are published.
	ArtifactDirName = "dcs-artifacts"

	artifactFileMode = os.FileMode(0o600)
	artifactDirMode  = os.FileMode(0o700)
)

var errUnsafeArtifactDirectory = errors.New("artifact directory is not a private directory")

// PersistRenderArtifacts verifies and persists explicitly requested XLSX
// artifacts in a render envelope. Service error envelopes are returned
// unchanged. Only artifacts[] (and the client diagnostics appended to
// diagnostics) are transformed; data inside tableDocument or business rows is
// never reinterpreted. An XLSX that was not requested is never written and
// keeps no unverified metadata.
func PersistRenderArtifacts(
	payload map[string]any,
	output any,
	cacheRoot string,
) map[string]any {
	if success, ok := payload["success"].(bool); !ok || !success {
		return payload
	}

	wantsXLSX := requestedXLSX(output)
	rawArtifacts, _ := payload["artifacts"].([]any)
	published := make([]any, 0, len(rawArtifacts))
	diagnostics := serverDiagnostics(payload)
	sawXLSX := false

	for _, raw := range rawArtifacts {
		artifact, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		format, _ := artifact["format"].(string)
		if strings.ToLower(strings.TrimSpace(format)) != "xlsx" {
			published = append(published, publicOtherArtifact(artifact))
			continue
		}
		sawXLSX = true
		if !wantsXLSX {
			published = append(published, artifactPlaceholder())
			continue
		}
		descriptor, artifactDiagnostics := persistXLSX(artifact, cacheRoot)
		published = append(published, descriptor)
		diagnostics = append(diagnostics, artifactDiagnostics...)
	}

	if wantsXLSX && !sawXLSX {
		diagnostics = append(diagnostics, diagnostic(
			"artifact_missing",
			"Requested XLSX artifact was not returned by the render response.",
			nil,
		))
	}

	result := make(map[string]any, len(payload)+2)
	for key, value := range payload {
		result[key] = value
	}
	result["artifacts"] = published
	result["diagnostics"] = diagnostics
	return result
}

func artifactPlaceholder() map[string]any {
	return map[string]any{"format": "xlsx", "contentOmitted": true}
}

func requestedXLSX(output any) bool {
	object, ok := output.(map[string]any)
	if !ok {
		return false
	}
	artifacts, ok := object["artifacts"].([]any)
	if !ok {
		return false
	}
	for _, item := range artifacts {
		if text, ok := item.(string); ok && strings.ToLower(strings.TrimSpace(text)) == "xlsx" {
			return true
		}
	}
	return false
}

func publicOtherArtifact(artifact map[string]any) map[string]any {
	descriptor := map[string]any{}
	if format, ok := artifact["format"].(string); ok && format != "" {
		descriptor["format"] = format
	}
	descriptor["contentOmitted"] = true
	return descriptor
}

func serverDiagnostics(payload map[string]any) []any {
	raw, ok := payload["diagnostics"].([]any)
	if !ok {
		return []any{}
	}
	diagnostics := make([]any, 0, len(raw))
	for _, item := range raw {
		if _, ok := item.(map[string]any); ok {
			diagnostics = append(diagnostics, item)
		}
	}
	return diagnostics
}

func persistXLSX(
	artifact map[string]any,
	cacheRoot string,
) (map[string]any, []any) {
	decoded, decodeDiagnostics := decodeContent(artifact)
	if decoded == nil {
		return artifactPlaceholder(), decodeDiagnostics
	}
	metadataDiagnostics := verifyMetadata(artifact, decoded)
	if len(metadataDiagnostics) > 0 {
		return artifactPlaceholder(), metadataDiagnostics
	}
	if cacheRoot == "" {
		return artifactPlaceholder(), []any{diagnostic(
			"artifact_persistence_unavailable",
			"No cache root is configured for generated XLSX persistence.",
			nil,
		)}
	}
	if securePersistenceUnavailable() {
		return artifactPlaceholder(), []any{diagnostic(
			"artifact_persistence_unavailable",
			"Secure dirfd persistence for generated XLSX is unavailable here.",
			nil,
		)}
	}
	path, err := writeGeneratedXLSX(decoded, cacheRoot)
	if err != nil {
		// A successful service envelope must stay successful even when the
		// local persistence target is unusable.
		return artifactPlaceholder(), []any{diagnostic(
			"artifact_write_failed",
			"Generated XLSX artifact could not be written under the cache root.",
			nil,
		)}
	}
	digest := sha256.Sum256(decoded)
	descriptor := map[string]any{
		"format":         "xlsx",
		"fileName":       filepath.Base(path),
		"sizeBytes":      len(decoded),
		"sha256":         base64.StdEncoding.EncodeToString(digest[:]),
		"sha256Encoding": "base64",
		"localPath":      path,
		"contentOmitted": true,
	}
	return descriptor, []any{}
}

func decodeContent(artifact map[string]any) ([]byte, []any) {
	content, ok := artifact["contentBase64"].(string)
	if !ok || content == "" {
		return nil, []any{diagnostic(
			"artifact_missing",
			"Requested XLSX artifact has no embedded content.",
			nil,
		)}
	}
	normalized := removeASCIIWhitespace(content)
	if normalized == "" {
		return nil, []any{diagnostic(
			"artifact_metadata_invalid",
			"Requested XLSX artifact content is empty after Base64 whitespace removal.",
			nil,
		)}
	}
	decoded, err := decodeStrictBase64(normalized)
	if err != nil {
		return nil, []any{diagnostic(
			"artifact_metadata_invalid",
			"Requested XLSX artifact content is not strict Base64.",
			nil,
		)}
	}
	if len(decoded) == 0 {
		return nil, []any{diagnostic(
			"artifact_metadata_invalid",
			"Requested XLSX artifact decoded content is empty.",
			nil,
		)}
	}
	return decoded, nil
}

// decodeStrictBase64 rejects every character outside the standard Base64
// alphabet before decoding. Go's base64 decoder silently ignores CR/LF even in
// Strict mode; content Base64 is whitespace-normalized by the caller first, but
// metadata such as the SHA-256 digest must contain no whitespace at all.
func decodeStrictBase64(value string) ([]byte, error) {
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '+', character == '/', character == '=':
		default:
			return nil, errors.New("non-base64 character")
		}
	}
	return base64.StdEncoding.Strict().DecodeString(value)
}

// removeASCIIWhitespace removes only the ASCII whitespace set accepted by the
// contract; no other normalization is performed.
func removeASCIIWhitespace(value string) string {
	return strings.Map(func(character rune) rune {
		switch character {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			return -1
		}
		return character
	}, value)
}

func verifyMetadata(artifact map[string]any, decoded []byte) []any {
	diagnostics := []any{}

	sizeBytes, sizeValid := integerValue(artifact["sizeBytes"])
	if !sizeValid || sizeBytes < 0 {
		diagnostics = append(diagnostics, diagnostic(
			"artifact_metadata_invalid",
			"Requested XLSX artifact has no valid sizeBytes.",
			map[string]any{"field": "sizeBytes"},
		))
	} else if sizeBytes != int64(len(decoded)) {
		diagnostics = append(diagnostics, diagnostic(
			"artifact_size_mismatch",
			"Requested XLSX artifact sizeBytes does not match decoded bytes.",
			map[string]any{
				"expectedBytes": sizeBytes,
				"actualBytes":   len(decoded),
			},
		))
	}

	sha256Text, shaPresent := artifact["sha256"].(string)
	encoding, _ := artifact["sha256Encoding"].(string)
	if !shaPresent || sha256Text == "" || encoding != "base64" {
		diagnostics = append(diagnostics, diagnostic(
			"artifact_metadata_invalid",
			"Requested XLSX artifact requires sha256 and sha256Encoding=base64.",
			map[string]any{"field": "sha256"},
		))
		return diagnostics
	}
	suppliedDigest, err := decodeStrictBase64(sha256Text)
	if err != nil || len(suppliedDigest) != sha256.Size {
		diagnostics = append(diagnostics, diagnostic(
			"artifact_metadata_invalid",
			"Requested XLSX artifact sha256 must be strict Base64 of 32 bytes.",
			map[string]any{"field": "sha256"},
		))
		return diagnostics
	}
	actualDigest := sha256.Sum256(decoded)
	if !hmac.Equal(suppliedDigest, actualDigest[:]) {
		diagnostics = append(diagnostics, diagnostic(
			"artifact_digest_mismatch",
			"Requested XLSX artifact SHA-256 Base64 digest does not match decoded bytes.",
			nil,
		))
	}
	return diagnostics
}

func integerValue(value any) (int64, bool) {
	switch number := value.(type) {
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	case int:
		return int64(number), true
	case int64:
		return number, true
	}
	return 0, false
}

func diagnostic(code string, message string, details map[string]any) map[string]any {
	entry := map[string]any{
		"code":     code,
		"message":  message,
		"artifact": "xlsx",
	}
	filtered := map[string]any{}
	for key, value := range details {
		if value != nil {
			filtered[key] = value
		}
	}
	if len(filtered) > 0 {
		entry["details"] = filtered
	}
	return entry
}

// writeGeneratedXLSX verifies the decoded bytes before any filesystem
// operation and publishes one exclusively created file below
// cacheRoot/dcs-artifacts. It returns the client-visible absolute path only
// after the opened handles and the path name still refer to the same inodes.
func writeGeneratedXLSX(decoded []byte, cacheRoot string) (string, error) {
	name, err := newArtifactName()
	if err != nil {
		return "", err
	}
	return writeGeneratedXLSXAs(decoded, cacheRoot, name)
}

func writeGeneratedXLSXAs(
	decoded []byte,
	cacheRoot string,
	name string,
) (path string, err error) {
	// Resolve/pin only the explicitly configured parent, then open or create
	// the CACHE_ROOT leaf without following a link. The raw configured path is
	// preserved: applying filepath.Clean here would change the meaning of
	// "a/alias/../cache" when a/alias is a symlink (the kernel resolves ".."
	// after the link). Parent aliases and ".." are trusted operator
	// configuration, never HTTP input; the leaf itself and dcs-artifacts must
	// still be real directories.
	root, rootPath, err := openCacheRoot(cacheRoot)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := verifyRootPath(rootPath, root); err != nil {
		return "", err
	}

	child, childInfo, err := openArtifactDirectory(root)
	if err != nil {
		return "", err
	}
	defer child.Close()

	file, err := child.OpenFile(
		name,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		artifactFileMode,
	)
	if err != nil {
		return "", err
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", err
	}

	published := false
	fileClosed := false
	defer func() {
		if published {
			return
		}
		// Keep the created descriptor open through the identity check and
		// removal attempt: on POSIX the inode cannot be reused while the file
		// remains linked and open, so SameFile cannot match a different file.
		// On Windows a delete through our own open handle can hit a sharing
		// violation; in that case the verified own entry is left in the
		// private artifact directory rather than falling back to an
		// unverified close-then-delete.
		if !fileClosed {
			cleanupOwnedArtifact(child, name, fileInfo)
			_ = file.Close()
			fileClosed = true
		}
	}()

	if err := setArtifactFileMode(file); err != nil {
		return "", err
	}
	written := 0
	for written < len(decoded) {
		count, err := file.Write(decoded[written:])
		if err != nil {
			return "", err
		}
		if count <= 0 {
			return "", errors.New("short generated artifact write")
		}
		written += count
	}
	if err := file.Sync(); err != nil {
		return "", err
	}

	// Keep the file handle open through the final path verification: the open
	// descriptor pins the inode, so a concurrently replaced directory entry
	// cannot make a reused inode number look like ours.
	entryAfter, err := root.Lstat(ArtifactDirName)
	if err != nil || entryAfter.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(entryAfter, childInfo) {
		return "", errUnsafeArtifactDirectory
	}
	fileEntry, err := child.Lstat(name)
	if err != nil || !os.SameFile(fileEntry, fileInfo) {
		return "", errUnsafeArtifactDirectory
	}
	path = joinRawPath(rootPath, ArtifactDirName, name)
	pathInfo, err := os.Lstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(pathInfo, fileInfo) {
		return "", errUnsafeArtifactDirectory
	}
	if err := verifyRootPath(rootPath, root); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		fileClosed = true
		return "", err
	}
	fileClosed = true
	published = true
	return path, nil
}

// openCacheRoot trusts the explicitly configured parent path (the operator's
// setting, never HTTP input) and pins it with os.OpenRoot. The final CACHE_ROOT
// leaf is then opened or created relative to that parent handle without
// following a symlink/junction; a pre-existing leaf link is refused. The raw
// configured path is preserved for the parent and for verification so that
// kernel symlink + ".." semantics stay intact. The volume root itself is opened
// directly. This matches Python's final-component no-follow behavior while
// allowing intentional relocated-home aliases such as a symlinked ~/.cache.
func openCacheRoot(cacheRoot string) (*os.Root, string, error) {
	rootPath, parent, leaf, isRoot, err := splitCacheRoot(cacheRoot)
	if err != nil {
		return nil, "", err
	}
	if isRoot {
		root, err := os.OpenRoot(rootPath)
		return root, rootPath, err
	}
	if err := os.MkdirAll(parent, artifactDirMode); err != nil {
		return nil, "", err
	}
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return nil, "", err
	}
	defer parentRoot.Close()
	child, _, err := openOrCreateDirectory(parentRoot, leaf)
	if err != nil {
		return nil, "", err
	}
	return child, rootPath, nil
}

// splitCacheRoot separates the configured path into a raw parent prefix and a
// leaf name without cleaning or joining the raw intermediate. It removes only
// trailing separators and handles the volume-root case. A final "." or ".."
// component is ambiguous for the anchored leaf operation and is refused rather
// than silently changing the directory.
func splitCacheRoot(cacheRoot string) (
	rootPath string,
	parent string,
	leaf string,
	isRoot bool,
	err error,
) {
	if cacheRoot == "" || !filepath.IsAbs(cacheRoot) {
		return "", "", "", false, errUnsafeArtifactDirectory
	}
	trimmed := trimTrailingSeparators(cacheRoot)
	volumeName := filepath.VolumeName(trimmed)
	anchor := volumeName + string(filepath.Separator)
	anchorTrimmed := trimTrailingSeparators(anchor)
	if trimmed == "" || trimmed == anchorTrimmed {
		// POSIX "/" trims to ""; Windows "C:\" and a UNC share root trim to
		// their volume name. Open the anchor directly.
		return anchor, "", "", true, nil
	}

	lastSeparator := -1
	for index := 0; index < len(trimmed); index++ {
		if os.IsPathSeparator(trimmed[index]) {
			lastSeparator = index
		}
	}
	if lastSeparator < 0 {
		return "", "", "", false, errUnsafeArtifactDirectory
	}
	parent = trimmed[:lastSeparator+1]
	leaf = trimmed[lastSeparator+1:]
	if leaf == "" || leaf == "." || leaf == ".." {
		return "", "", "", false, errUnsafeArtifactDirectory
	}
	return trimmed, parent, leaf, false, nil
}

func trimTrailingSeparators(value string) string {
	end := len(value)
	for end > 0 && os.IsPathSeparator(value[end-1]) {
		end--
	}
	return value[:end]
}

// joinRawPath appends components without filepath.Join/Clean, preserving raw
// symlink and ".." components in the operator-configured prefix. Components
// are generated basenames, never service-provided paths.
func joinRawPath(base string, components ...string) string {
	result := base
	for _, component := range components {
		if result == "" || !os.IsPathSeparator(result[len(result)-1]) {
			result += string(filepath.Separator)
		}
		result += component
	}
	return result
}

// verifyRootPath requires the configured CACHE_ROOT path to still resolve to
// the opened leaf inode. It is checked before publishing and again after the
// write while the root descriptor remains open, so a replaced/renamed leaf or a
// changed parent alias is not published. The path is passed raw (only trailing
// separators removed), so symlink + ".." resolution stays native.
func verifyRootPath(cacheRoot string, root *os.Root) error {
	rootInfo, err := root.Stat(".")
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(cacheRoot)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(rootInfo, pathInfo) {
		return errUnsafeArtifactDirectory
	}
	return nil
}

// openArtifactDirectory opens (or privately creates) dcs-artifacts relative
// to the cache root handle. The directory itself must be a real directory:
// a symbolic link or junction is always refused, even when it would stay
// inside the root.
func openArtifactDirectory(root *os.Root) (*os.Root, os.FileInfo, error) {
	child, childInfo, err := openOrCreateDirectory(root, ArtifactDirName)
	if err != nil {
		return nil, nil, err
	}
	if !artifactDirectoryIsPrivate(childInfo) {
		_ = child.Close()
		return nil, nil, errUnsafeArtifactDirectory
	}
	return child, childInfo, nil
}

// openOrCreateDirectory is the single anchored directory primitive used for
// both the CACHE_ROOT leaf and the artifact child. It never opens a component
// by path: Lstat, creation, and OpenRoot are all relative to the already
// verified parent handle, and the opened identity is checked against the
// inspected entry before use.
func openOrCreateDirectory(
	parent *os.Root,
	name string,
) (*os.Root, os.FileInfo, error) {
	for attempt := 0; attempt < 2; attempt++ {
		entry, err := parent.Lstat(name)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return nil, nil, err
			}
			if mkdirErr := parent.Mkdir(name, artifactDirMode); mkdirErr != nil {
				if errors.Is(mkdirErr, fs.ErrExist) {
					continue
				}
				return nil, nil, mkdirErr
			}
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
			return nil, nil, errUnsafeArtifactDirectory
		}
		child, err := parent.OpenRoot(name)
		if err != nil {
			return nil, nil, err
		}
		childInfo, err := child.Stat(".")
		if err != nil {
			_ = child.Close()
			return nil, nil, err
		}
		// Verify the opened directory is exactly the entry we inspected.
		entryAgain, err := parent.Lstat(name)
		if err != nil || entryAgain.Mode()&os.ModeSymlink != 0 ||
			!os.SameFile(entryAgain, childInfo) || !os.SameFile(entry, childInfo) {
			_ = child.Close()
			return nil, nil, errUnsafeArtifactDirectory
		}
		return child, childInfo, nil
	}
	return nil, nil, errUnsafeArtifactDirectory
}

// cleanupOwnedArtifact removes name only while the entry is still the inode
// this call created. The caller keeps the created file descriptor open across
// this check and the removal attempt; a failed removal (for example a Windows
// sharing violation) leaves the verified own entry in place.
func cleanupOwnedArtifact(dir *os.Root, name string, expected os.FileInfo) {
	current, err := dir.Lstat(name)
	if err != nil || !os.SameFile(current, expected) {
		return
	}
	_ = dir.Remove(name)
}

func newArtifactName() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	// RFC 4122 version 4 variant bits, matching the generated uuid4().hex
	// shape of the Python implementation.
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return "dcs-" + hex.EncodeToString(raw[:]) + ".xlsx", nil
}

func securePersistenceUnavailable() bool {
	switch runtime.GOOS {
	case "js", "plan9", "wasip1":
		// os.Root cannot guarantee confinement on these platforms.
		return true
	}
	return false
}

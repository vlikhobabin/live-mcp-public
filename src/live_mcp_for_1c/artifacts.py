"""Strict validation and exclusive persistence of AIDataRead XLSX artifacts.

The HTTP service sends the artifact inside JSON as Base64 with a Base64 SHA-256
digest and a size. The MCP host never trusts the suggested ``fileName`` or any
server-provided path: it verifies the bytes, generates its own name under an
explicit user cache root, creates the file exclusively with mode ``0600``, and
only then replaces ``contentBase64`` with the client-local path.
"""

from __future__ import annotations

import base64
import binascii
import hashlib
import hmac
import os
import stat
from pathlib import Path
from typing import Any
from uuid import uuid4

from live_mcp_for_1c.config import DEFAULT_ARTIFACT_DIR_NAME

_ASCII_WHITESPACE = frozenset(" \t\n\r\v\f")
_ARTIFACT_PLACEHOLDER: dict[str, Any] = {"format": "xlsx", "contentOmitted": True}
_ARTIFACT_DIR_MODE = 0o700
_ARTIFACT_FILE_MODE = 0o600


class _ArtifactPersistenceUnavailable(Exception):
    """Raised when the platform lacks dirfd primitives for safe persistence."""


def _dirfd_primitives_available() -> bool:
    """Return whether the platform can create/cleanup files through open dirfds."""
    supported = getattr(os, "supports_dir_fd", frozenset())
    return (
        os.open in supported
        and os.mkdir in supported
        and os.unlink in supported
        and os.stat in supported
        and hasattr(os, "O_DIRECTORY")
        and hasattr(os, "O_NOFOLLOW")
        and hasattr(os, "fchmod")
    )


def _same_inode(first: os.stat_result, second: os.stat_result) -> bool:
    return first.st_dev == second.st_dev and first.st_ino == second.st_ino


def _artifact_directory_is_private(directory_stat: os.stat_result) -> bool:
    """An existing ``dcs-artifacts`` must be ours and not group/other-writable.

    A pre-existing ``0755`` directory owned by the current user is accepted;
    ``0775``/``0777`` (or a foreign owner) is rejected because another user
    could race directory entries even though file operations use dirfds. The
    directory is never chmod-ed here.
    """
    geteuid = getattr(os, "geteuid", None)
    if geteuid is None or directory_stat.st_uid != geteuid():
        return False
    return (directory_stat.st_mode & 0o022) == 0



def _open_directory(path: str | Path, *, dir_fd: int | None = None) -> int:
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    return os.open(path, flags, dir_fd=dir_fd)


def _open_artifact_directory(root_fd: int) -> int:
    """Open (or privately create) ``dcs-artifacts`` relative to the cache root."""
    name = DEFAULT_ARTIFACT_DIR_NAME
    try:
        return _open_directory(name, dir_fd=root_fd)
    except FileNotFoundError:
        pass
    try:
        os.mkdir(name, _ARTIFACT_DIR_MODE, dir_fd=root_fd)
    except FileExistsError:
        # A concurrent creator may have won the race. The following O_NOFOLLOW
        # open is the authority on whether the entry is a real directory.
        pass
    return _open_directory(name, dir_fd=root_fd)


def _unlink_owned_entry(
    directory_fd: int,
    name: str,
    expected: os.stat_result,
) -> None:
    """Remove ``name`` only while the entry is still the inode we created."""
    try:
        entry = os.stat(name, dir_fd=directory_fd, follow_symlinks=False)
    except OSError:
        return
    if not _same_inode(entry, expected):
        return
    try:
        os.unlink(name, dir_fd=directory_fd)
    except OSError:
        pass


def _verify_published_path(
    cache_root: Path,
    root_fd: int,
    root_stat: os.stat_result,
    child_fd: int,
    child_stat: os.stat_result,
    name: str,
    file_stat: os.stat_result,
) -> Path:
    """Verify the configured absolute path still names the opened inodes.

    Publication never switches to a descriptor-derived path: the caller sees
    only ``cache_root/dcs-artifacts/<generated>``. If the configured root, the
    child entry, or the generated file has been renamed or replaced, the path
    is not returned.
    """
    root_path_stat = os.stat(cache_root, follow_symlinks=False)
    if not _same_inode(root_stat, root_path_stat):
        raise OSError("cache root was replaced")
    child_entry_stat = os.stat(
        DEFAULT_ARTIFACT_DIR_NAME, dir_fd=root_fd, follow_symlinks=False
    )
    if not _same_inode(child_stat, child_entry_stat):
        raise OSError("artifact directory was replaced")
    entry_stat = os.stat(name, dir_fd=child_fd, follow_symlinks=False)
    if not _same_inode(file_stat, entry_stat):
        raise OSError("generated artifact entry was replaced")
    path = cache_root / DEFAULT_ARTIFACT_DIR_NAME / name
    path_stat = os.stat(path, follow_symlinks=False)
    if not _same_inode(file_stat, path_stat):
        raise OSError("generated artifact path was replaced")
    return path


def _diagnostic(code: str, message: str, **details: Any) -> dict[str, Any]:
    diagnostic: dict[str, Any] = {
        "code": code,
        "message": message,
        "artifact": "xlsx",
    }
    bounded = {key: value for key, value in details.items() if value is not None}
    if bounded:
        diagnostic["details"] = bounded
    return diagnostic


def _normalize_base64(value: str) -> str:
    """Remove ASCII whitespace only; no other normalization is accepted."""
    return "".join(
        character for character in value if character not in _ASCII_WHITESPACE
    )


def _decode_content(
    artifact: dict[str, Any],
) -> tuple[bytes | None, dict[str, Any] | None]:
    content = artifact.get("contentBase64")
    if not isinstance(content, str) or not content:
        return None, _diagnostic(
            "artifact_missing",
            "Requested XLSX artifact has no embedded content.",
        )
    normalized = _normalize_base64(content)
    if not normalized:
        return None, _diagnostic(
            "artifact_metadata_invalid",
            "Requested XLSX artifact content is empty after Base64 whitespace removal.",
        )
    try:
        decoded = base64.b64decode(normalized, validate=True)
    except (binascii.Error, ValueError, UnicodeEncodeError):
        return None, _diagnostic(
            "artifact_metadata_invalid",
            "Requested XLSX artifact content is not strict Base64.",
        )
    if not decoded:
        return None, _diagnostic(
            "artifact_metadata_invalid",
            "Requested XLSX artifact decoded content is empty.",
        )
    return decoded, None


def _verify_metadata(artifact: dict[str, Any], decoded: bytes) -> list[dict[str, Any]]:
    diagnostics: list[dict[str, Any]] = []

    size_bytes = artifact.get("sizeBytes")
    if (
        isinstance(size_bytes, bool)
        or not isinstance(size_bytes, int)
        or size_bytes < 0
    ):
        diagnostics.append(
            _diagnostic(
                "artifact_metadata_invalid",
                "Requested XLSX artifact has no valid sizeBytes.",
                field="sizeBytes",
            )
        )
    elif size_bytes != len(decoded):
        diagnostics.append(
            _diagnostic(
                "artifact_size_mismatch",
                "Requested XLSX artifact sizeBytes does not match decoded bytes.",
                expectedBytes=size_bytes,
                actualBytes=len(decoded),
            )
        )

    sha256 = artifact.get("sha256")
    sha256_encoding = artifact.get("sha256Encoding")
    if not isinstance(sha256, str) or not sha256 or sha256_encoding != "base64":
        diagnostics.append(
            _diagnostic(
                "artifact_metadata_invalid",
                "Requested XLSX artifact requires sha256 and sha256Encoding=base64.",
                field="sha256",
            )
        )
        return diagnostics

    try:
        supplied_digest = base64.b64decode(sha256, validate=True)
    except (binascii.Error, ValueError, UnicodeEncodeError):
        supplied_digest = b""
    if len(supplied_digest) != 32:
        diagnostics.append(
            _diagnostic(
                "artifact_metadata_invalid",
                "Requested XLSX artifact sha256 must be strict Base64 of 32 bytes.",
                field="sha256",
            )
        )
    elif not hmac.compare_digest(supplied_digest, hashlib.sha256(decoded).digest()):
        diagnostics.append(
            _diagnostic(
                "artifact_digest_mismatch",
                "Requested XLSX artifact SHA-256 Base64 digest does not match decoded bytes.",
            )
        )
    return diagnostics


def _write_generated_xlsx(decoded: bytes, cache_root: Path) -> Path:
    """Write bytes to a generated exclusive 0600 file through open dirfds.

    The cache root is opened once, ``dcs-artifacts`` is opened (or privately
    created) relative to that root descriptor with ``O_NOFOLLOW``, and the file
    is created relative to the child descriptor with ``O_EXCL|O_NOFOLLOW``.
    Cleanup also goes through the child descriptor and only removes the inode
    this call created. A path-based symlink swap between checks therefore
    cannot redirect the write or make cleanup delete somebody else's file.
    """
    if not _dirfd_primitives_available():
        raise _ArtifactPersistenceUnavailable(
            "secure dirfd artifact persistence is not supported on this platform"
        )

    # The configured cache root may legitimately not exist yet (default HOME
    # cache). It is the only path-based directory creation; the artifact child
    # and file operations are descriptor-relative.
    cache_root.mkdir(parents=True, exist_ok=True)

    root_fd = -1
    child_fd = -1
    file_fd = -1
    name = f"dcs-{uuid4().hex}.xlsx"
    file_stat: os.stat_result | None = None
    try:
        root_fd = _open_directory(cache_root)
        root_stat = os.fstat(root_fd)
        if not stat.S_ISDIR(root_stat.st_mode):
            raise OSError("cache root is not a directory")

        child_fd = _open_artifact_directory(root_fd)
        child_stat = os.fstat(child_fd)
        if not stat.S_ISDIR(child_stat.st_mode):
            raise OSError("artifact directory is not a directory")
        if not _artifact_directory_is_private(child_stat):
            raise OSError("artifact directory is not private to the current user")

        flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW
        file_fd = os.open(name, flags, _ARTIFACT_FILE_MODE, dir_fd=child_fd)
        file_stat = os.fstat(file_fd)
        os.fchmod(file_fd, _ARTIFACT_FILE_MODE)

        view = memoryview(decoded)
        while view:
            written = os.write(file_fd, view)
            if written <= 0:
                raise OSError("short generated artifact write")
            view = view[written:]
        os.fsync(file_fd)

        # Keep file_fd open through the final path verification and owned
        # cleanup: the open descriptor pins the inode, so a concurrently
        # replaced directory entry cannot make a reused inode number look like
        # ours. The descriptor is closed in finally.
        return _verify_published_path(
            cache_root,
            root_fd,
            root_stat,
            child_fd,
            child_stat,
            name,
            file_stat,
        )
    except BaseException:
        if child_fd >= 0 and file_stat is not None:
            _unlink_owned_entry(child_fd, name, file_stat)
        raise
    finally:
        for descriptor in (file_fd, child_fd, root_fd):
            if descriptor >= 0:
                try:
                    os.close(descriptor)
                except OSError:
                    pass


def _persist_xlsx(
    artifact: dict[str, Any], cache_root: Path | None
) -> tuple[dict[str, Any], list[dict[str, Any]]]:
    decoded, decode_diagnostic = _decode_content(artifact)
    if decoded is None:
        assert decode_diagnostic is not None
        return dict(_ARTIFACT_PLACEHOLDER), [decode_diagnostic]
    diagnostics = _verify_metadata(artifact, decoded)
    if diagnostics:
        return dict(_ARTIFACT_PLACEHOLDER), diagnostics
    if cache_root is None:
        return dict(_ARTIFACT_PLACEHOLDER), [
            _diagnostic(
                "artifact_persistence_unavailable",
                "No cache root is configured for generated XLSX persistence.",
            )
        ]
    try:
        path = _write_generated_xlsx(decoded, cache_root)
    except _ArtifactPersistenceUnavailable:
        return dict(_ARTIFACT_PLACEHOLDER), [
            _diagnostic(
                "artifact_persistence_unavailable",
                "Secure dirfd persistence for generated XLSX is unavailable here.",
            )
        ]
    except Exception:
        # A successful service envelope must stay successful even when the
        # local persistence target is unusable.
        return dict(_ARTIFACT_PLACEHOLDER), [
            _diagnostic(
                "artifact_write_failed",
                "Generated XLSX artifact could not be written under the cache root.",
            )
        ]
    descriptor = {
        "format": "xlsx",
        "fileName": path.name,
        "sizeBytes": len(decoded),
        "sha256": base64.b64encode(hashlib.sha256(decoded).digest()).decode("ascii"),
        "sha256Encoding": "base64",
        "localPath": str(path),
        "contentOmitted": True,
    }
    return descriptor, []


def requested_xlsx(output: Any) -> bool:
    """Return whether the render output explicitly requested an XLSX artifact."""
    if not isinstance(output, dict):
        return False
    artifacts = output.get("artifacts")
    if not isinstance(artifacts, list):
        return False
    return any(
        isinstance(item, str) and item.strip().lower() == "xlsx" for item in artifacts
    )


def _public_other_artifact(artifact: dict[str, Any]) -> dict[str, Any]:
    descriptor: dict[str, Any] = {}
    artifact_format = artifact.get("format")
    if isinstance(artifact_format, str) and artifact_format:
        descriptor["format"] = artifact_format
    descriptor["contentOmitted"] = True
    return descriptor


def _server_diagnostics(payload: dict[str, Any]) -> list[dict[str, Any]]:
    raw = payload.get("diagnostics")
    if not isinstance(raw, list):
        return []
    return [item for item in raw if isinstance(item, dict)]


def persist_render_artifacts(
    payload: dict[str, Any],
    *,
    output: Any,
    cache_root: Path | None,
) -> dict[str, Any]:
    """Verify and persist explicitly requested XLSX artifacts in a render envelope.

    Service error envelopes are returned unchanged. Only ``artifacts[]`` (and
    the client diagnostics appended to ``diagnostics``) are transformed; data
    inside ``tableDocument`` or business rows is never reinterpreted. An XLSX
    that was not requested is never written and keeps no unverified metadata.
    """
    if payload.get("success") is not True:
        return payload

    wants_xlsx = requested_xlsx(output)
    raw_artifacts = payload.get("artifacts")
    artifacts = raw_artifacts if isinstance(raw_artifacts, list) else []
    published: list[dict[str, Any]] = []
    diagnostics = _server_diagnostics(payload)
    saw_xlsx = False

    for artifact in artifacts:
        if not isinstance(artifact, dict):
            continue
        artifact_format = artifact.get("format")
        normalized_format = (
            artifact_format.strip().lower() if isinstance(artifact_format, str) else ""
        )
        if normalized_format != "xlsx":
            published.append(_public_other_artifact(artifact))
            continue
        saw_xlsx = True
        if not wants_xlsx:
            published.append(dict(_ARTIFACT_PLACEHOLDER))
            continue
        descriptor, artifact_diagnostics = _persist_xlsx(artifact, cache_root)
        published.append(descriptor)
        diagnostics.extend(artifact_diagnostics)

    if wants_xlsx and not saw_xlsx:
        diagnostics.append(
            _diagnostic(
                "artifact_missing",
                "Requested XLSX artifact was not returned by the render response.",
            )
        )

    result = dict(payload)
    result["artifacts"] = published
    result["diagnostics"] = diagnostics
    return result

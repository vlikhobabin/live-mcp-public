"""Local (non-service) error envelope shared by the MCP tools."""

from __future__ import annotations

from typing import Any, Literal

ErrorKind = Literal[
    "configuration_error",
    "invalid_argument",
    "authentication_failed",
    "permission_denied",
    "upstream_timeout",
    "upstream_connection_error",
    "upstream_invalid_response",
    "internal_error",
]


def build_local_error(
    error_kind: ErrorKind,
    message: str,
    *,
    http_status: int | None = None,
    details: dict[str, Any] | None = None,
) -> dict[str, Any]:
    """Build the MCP-local error object from the minimal standalone contract."""
    return {
        "ok": False,
        "error_kind": error_kind,
        "message": message,
        "http_status": http_status,
        "details": dict(details or {}),
    }


class LocalError(Exception):
    """A transport, argument, or internal failure without a service JSON envelope."""

    def __init__(
        self,
        error_kind: ErrorKind,
        message: str,
        *,
        http_status: int | None = None,
        details: dict[str, Any] | None = None,
    ) -> None:
        super().__init__(message)
        self.error_kind = error_kind
        self.message = message
        self.http_status = http_status
        self.details = dict(details or {})

    def to_envelope(self) -> dict[str, Any]:
        """Return the contract-shaped local error object."""
        return build_local_error(
            self.error_kind,
            self.message,
            http_status=self.http_status,
            details=self.details,
        )

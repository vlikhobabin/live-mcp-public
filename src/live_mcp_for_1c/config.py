"""Explicit process-environment configuration for the standalone MCP server.

The minimal product has exactly one AIDataRead connection and no discovery,
profile selection, ``.env`` loading, compatibility aliases, or fallback. These
settings are read from the process environment only and no network connection
is opened while they are loaded.
"""

from __future__ import annotations

import math
import os
from collections.abc import Mapping
from dataclasses import dataclass, field
from pathlib import Path
from urllib.parse import urlsplit

BASE_URL_ENV = "LIVE_MCP_FOR_1C_HTTP_SERVICE_BASE_URL"
USERNAME_ENV = "LIVE_MCP_FOR_1C_HTTP_SERVICE_USERNAME"
PASSWORD_ENV = "LIVE_MCP_FOR_1C_HTTP_SERVICE_PASSWORD"
VERIFY_TLS_ENV = "LIVE_MCP_FOR_1C_VERIFY_TLS"
TIMEOUT_ENV = "LIVE_MCP_FOR_1C_HTTP_TIMEOUT_SECONDS"
CACHE_ROOT_ENV = "LIVE_MCP_FOR_1C_CACHE_ROOT"

REQUIRED_ENV = (BASE_URL_ENV, USERNAME_ENV, PASSWORD_ENV)
DEFAULT_TIMEOUT_SECONDS = 60.0
DEFAULT_ARTIFACT_DIR_NAME = "dcs-artifacts"

_TRUE_VALUES = frozenset({"1", "true", "yes", "on"})
_FALSE_VALUES = frozenset({"0", "false", "no", "off"})


class ConfigurationError(ValueError):
    """Raised when explicit process configuration is missing or invalid."""


def default_cache_root() -> Path:
    """Return the portable user cache root, independent of the installation tree."""
    return Path.home() / ".cache" / "live-mcp-for-1c"


@dataclass(frozen=True, slots=True)
class Settings:
    """One resolved AIDataRead connection and local artifact cache configuration."""

    base_url: str
    username: str
    password: str
    verify_tls: bool = True
    timeout_seconds: float = DEFAULT_TIMEOUT_SECONDS
    cache_root: Path = field(default_factory=default_cache_root)

    def __post_init__(self) -> None:
        if not self.base_url or self.base_url.endswith("/"):
            raise ConfigurationError(
                f"{BASE_URL_ENV} must be the publication base including the service "
                "root and must not end with '/'."
            )
        if "?" in self.base_url or "#" in self.base_url:
            raise ConfigurationError(
                f"{BASE_URL_ENV} must not contain a query or fragment delimiter."
            )
        try:
            parts = urlsplit(self.base_url)
            port = parts.port
        except ValueError as exc:
            raise ConfigurationError(
                f"{BASE_URL_ENV} is not a valid absolute http(s) URL."
            ) from exc
        if (
            parts.scheme not in {"http", "https"}
            or not parts.netloc
            or not parts.hostname
        ):
            raise ConfigurationError(
                f"{BASE_URL_ENV} must be an absolute http(s) URL without userinfo, "
                "query, or fragment."
            )
        if "@" in parts.netloc or (port is not None and not 0 < port < 65536):
            raise ConfigurationError(
                f"{BASE_URL_ENV} must not contain userinfo and must use a valid port."
            )
        if not self.username:
            raise ConfigurationError(f"{USERNAME_ENV} must not be empty.")
        if isinstance(self.timeout_seconds, bool) or not isinstance(
            self.timeout_seconds, (int, float)
        ):
            raise ConfigurationError(f"{TIMEOUT_ENV} must be a positive number.")
        if not math.isfinite(self.timeout_seconds) or self.timeout_seconds <= 0:
            raise ConfigurationError(f"{TIMEOUT_ENV} must be a positive number.")
        if not isinstance(self.cache_root, Path) or not self.cache_root.is_absolute():
            raise ConfigurationError(
                f"{CACHE_ROOT_ENV} must be an absolute directory path when set."
            )

    @property
    def artifact_dir(self) -> Path:
        """Return the lazily created directory for generated XLSX files."""
        return self.cache_root / DEFAULT_ARTIFACT_DIR_NAME


def _required_value(environ: Mapping[str, str], name: str) -> str:
    if name not in environ:
        raise ConfigurationError(f"missing required environment variable: {name}")
    return environ[name]


def _parse_boolean(name: str, raw: str | None, *, default: bool) -> bool:
    if raw is None:
        return default
    normalized = raw.strip().lower()
    if normalized in _TRUE_VALUES:
        return True
    if normalized in _FALSE_VALUES:
        return False
    raise ConfigurationError(
        f"{name} must be one of true/false, 1/0, yes/no, or on/off."
    )


def _parse_timeout(raw: str | None) -> float:
    if raw is None:
        return DEFAULT_TIMEOUT_SECONDS
    try:
        value = float(raw.strip())
    except (TypeError, ValueError) as exc:
        raise ConfigurationError(f"{TIMEOUT_ENV} must be a positive number.") from exc
    if not math.isfinite(value) or value <= 0:
        raise ConfigurationError(f"{TIMEOUT_ENV} must be a positive number.")
    return value


def _parse_cache_root(raw: str | None) -> Path:
    if raw is None:
        return default_cache_root()
    # Preserve the explicit value exactly: no trim, no ``~`` expansion and no
    # other path rewriting. A non-absolute value is a configuration error
    # rather than a silent fallback to the default cache.
    path = Path(raw)
    if not path.is_absolute():
        raise ConfigurationError(
            f"{CACHE_ROOT_ENV} must be an absolute directory path when set."
        )
    return path


def load_settings(environ: Mapping[str, str] | None = None) -> Settings:
    """Load the six documented settings from the process environment.

    Only the variables listed in :data:`REQUIRED_ENV` are mandatory. Optional
    values use their documented defaults. No file is read and no directory is
    created here.
    """
    env = os.environ if environ is None else environ
    base_url = _required_value(env, BASE_URL_ENV).strip()
    # The username is an explicit credential component: whitespace is part of
    # the value and must not be normalized away.
    username = _required_value(env, USERNAME_ENV)
    password = _required_value(env, PASSWORD_ENV)
    return Settings(
        base_url=base_url,
        username=username,
        password=password,
        verify_tls=_parse_boolean(
            VERIFY_TLS_ENV, env.get(VERIFY_TLS_ENV), default=True
        ),
        timeout_seconds=_parse_timeout(env.get(TIMEOUT_ENV)),
        cache_root=_parse_cache_root(env.get(CACHE_ROOT_ENV)),
    )

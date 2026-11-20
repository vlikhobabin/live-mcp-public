"""Direct, fixed-route HTTP transport for the AIDataRead custom HTTP service.

There is exactly one request per tool call: no retry, no redirect following, no
environment proxy/redirect discovery, and no fallback route or profile. The
service response body is the MCP result; only local/transport failures become
``LocalError`` objects.
"""

from __future__ import annotations

from typing import Any

import httpx

from live_mcp_for_1c.config import Settings
from live_mcp_for_1c.errors import LocalError

PING_PATH = "/ping/"
INFO_PATH = "/info/"
QUERY_VALIDATE_PATH = "/v1/query/validate/"
QUERY_EXECUTE_PATH = "/v1/query/execute/"
DCS_EXECUTE_PATH = "/v1/dcs/execute/"
DCS_RENDER_PATH = "/v1/dcs/render/"

SERVICE_NAME = "ai-data-read"

# Only platform texts with a known, static meaning may be shown back to the MCP
# caller. HTTP-REFERENCE §13.3 documents this exact 403 text. Everything else
# (401 pages, proxy diagnostics, or arbitrary attacker-controlled text) is
# replaced by a constant placeholder so credentials and arbitrary upstream
# content never cross the local error boundary.
_CANONICAL_AUTH_TEXTS = frozenset(
    {"Недостаточно прав для использования ресурса с данным HTTP методом"}
)
_BODY_EXCERPT_PLACEHOLDER = "platform response text withheld (<redacted>)"


def _is_success_envelope(payload: Any, http_status: int) -> bool:
    return (
        http_status == 200
        and isinstance(payload, dict)
        and payload.get("success") is True
        and payload.get("service") == SERVICE_NAME
    )


def _is_error_envelope(payload: Any, http_status: int) -> bool:
    """Return whether ``payload`` is the complete HTTP-REFERENCE §1.5 error envelope."""
    return (
        400 <= http_status <= 599
        and isinstance(payload, dict)
        and payload.get("success") is False
        and payload.get("service") == SERVICE_NAME
        and isinstance(payload.get("code"), str)
        and isinstance(payload.get("message"), str)
        and isinstance(payload.get("details"), dict)
    )


class AIDataReadClient:
    """Bounded synchronous client for the six fixed AIDataRead routes."""

    def __init__(self, settings: Settings) -> None:
        self._settings = settings
        self._client = httpx.Client(
            auth=httpx.BasicAuth(settings.username, settings.password),
            timeout=settings.timeout_seconds,
            verify=settings.verify_tls,
            follow_redirects=False,
            trust_env=False,
        )

    @property
    def base_url(self) -> str:
        """Return the configured publication base without a trailing slash."""
        return self._settings.base_url

    def close(self) -> None:
        """Close the underlying HTTP client."""
        self._client.close()

    def get(self, path: str) -> dict[str, Any]:
        """Call one fixed GET route and return the service envelope."""
        return self._request("GET", path)

    def post(self, path: str, body: dict[str, Any]) -> dict[str, Any]:
        """Call one fixed POST route and return the service envelope."""
        return self._request("POST", path, body=body)

    def _request(
        self,
        method: str,
        path: str,
        *,
        body: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        url = f"{self._settings.base_url}{path}"
        try:
            response = self._client.request(method, url, json=body)
        except httpx.TimeoutException as exc:
            raise LocalError(
                "upstream_timeout",
                "AIDataRead request timed out.",
                details={"endpoint_path": path},
            ) from exc
        except httpx.HTTPError as exc:
            raise LocalError(
                "upstream_connection_error",
                "AIDataRead request failed before a response was received.",
                details={"endpoint_path": path},
            ) from exc

        http_status = response.status_code
        payload = self._json_body(response)

        if _is_success_envelope(payload, http_status):
            return payload  # type: ignore[return-value]
        # A 4xx/5xx body is the service result only when the complete §1.5
        # envelope is present. ``success:false`` plus ``service`` alone is not
        # enough: a partial object must not be mistaken for a CFE error.
        if _is_error_envelope(payload, http_status):
            return payload  # type: ignore[return-value]

        if http_status == 401:
            raise LocalError(
                "authentication_failed",
                "AIDataRead authentication failed.",
                http_status=http_status,
                details={
                    "endpoint_path": path,
                    "body_excerpt": self._safe_excerpt(response),
                },
            )
        if http_status == 403:
            raise LocalError(
                "permission_denied",
                "AIDataRead denied access to the HTTP method.",
                http_status=http_status,
                details={
                    "endpoint_path": path,
                    "body_excerpt": self._safe_excerpt(response),
                },
            )
        raise LocalError(
            "upstream_invalid_response",
            "AIDataRead did not return the expected JSON service envelope.",
            http_status=http_status,
            details={"endpoint_path": path, "http_status": http_status},
        )

    def _safe_excerpt(self, response: httpx.Response) -> str:
        """Return only a known canonical platform text or a fixed placeholder.

        The upstream body is never copied, excerpted, trimmed-to-fit, or
        partially redacted. Partial redaction of arbitrary text (Bearer
        headers, HTML entities, multi-line secrets) cannot be made reliable, so
        only an exact allowlisted canonical message is surfaced.
        """
        try:
            text = response.text or ""
        except Exception:  # noqa: BLE001 - undecodable upstream text is not surfaced
            return _BODY_EXCERPT_PLACEHOLDER
        normalized = text.strip()
        if normalized in _CANONICAL_AUTH_TEXTS:
            return normalized
        return _BODY_EXCERPT_PLACEHOLDER

    @staticmethod
    def _json_body(response: httpx.Response) -> Any:
        try:
            return response.json()
        except ValueError:
            return None

"""Minimal MCP server exposing the six AIDataRead operations over stdio.

The server uses the official low-level MCP SDK server with
``validate_input=False``. That lets the published ``parameters`` schema remain
an array (never a type-inferred map) while the tool still returns the contract
``invalid_argument`` object for a map or any other locally invalid form instead
of a protocol validation error. Unknown tools and fields are rejected before
the single HTTP connection is used.
"""

from __future__ import annotations

import asyncio
import sys
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Any, Callable

from mcp import types
from mcp.server.lowlevel import Server
from mcp.server.stdio import stdio_server

from live_mcp_for_1c import __version__
from live_mcp_for_1c.artifacts import persist_render_artifacts
from live_mcp_for_1c.config import ConfigurationError, Settings, load_settings
from live_mcp_for_1c.errors import LocalError, build_local_error
from live_mcp_for_1c.http_client import (
    DCS_EXECUTE_PATH,
    DCS_RENDER_PATH,
    INFO_PATH,
    PING_PATH,
    QUERY_EXECUTE_PATH,
    QUERY_VALIDATE_PATH,
    AIDataReadClient,
)

SERVER_NAME = "live-mcp-for-1c"

MAX_QUERY_TEXT_CHARS = 65536
MAX_SCHEMA_XML_BYTES = 1048576
MAX_PARAMETERS = 64

_STRING = {"type": "string"}
_PARAMETERS = {
    "type": "array",
    "items": {"type": "object"},
    "default": [],
    "maxItems": MAX_PARAMETERS,
}
_LIMITS = {"type": "object", "default": {}}
_OUTPUT_FORMAT = {"type": "string", "enum": ["json", "csv"], "default": "json"}


def _string(description: str) -> dict[str, Any]:
    return {**_STRING, "description": description}


def _query_text(description: str) -> dict[str, Any]:
    return {
        "type": "string",
        "maxLength": MAX_QUERY_TEXT_CHARS,
        "description": description,
    }


def _schema_xml() -> dict[str, Any]:
    return _string(
        "Data Composition Schema XML with schemaVersion 1; at most 1048576 UTF-8 bytes."
    )


TOOL_SCHEMAS: dict[str, dict[str, Any]] = {
    "validate_1c_query": {
        "type": "object",
        "properties": {
            "query_text": _query_text("Read-only 1C query text to parse and admit."),
            "parameters": _PARAMETERS,
            "limits": _LIMITS,
            "correlation_id": _string("Optional correlation id echoed by the service."),
            "requested_user": _string(
                "Optional diagnostic user hint; never impersonation."
            ),
        },
        "required": ["query_text"],
        "additionalProperties": False,
    },
    "execute_1c_query": {
        "type": "object",
        "properties": {
            "query_text": _query_text("Admitted read-only 1C query text to execute."),
            "parameters": _PARAMETERS,
            "output_format": _OUTPUT_FORMAT,
            "limits": _LIMITS,
            "correlation_id": _string("Optional correlation id echoed by the service."),
            "requested_user": _string(
                "Optional diagnostic user hint; never impersonation."
            ),
        },
        "required": ["query_text"],
        "additionalProperties": False,
    },
    "execute_dcs_schema": {
        "type": "object",
        "properties": {
            "schema_xml": _schema_xml(),
            "parameters": _PARAMETERS,
            "output_format": _OUTPUT_FORMAT,
            "limits": _LIMITS,
            "correlation_id": _string("Optional correlation id echoed by the service."),
            "requested_user": _string(
                "Optional diagnostic user hint; never impersonation."
            ),
        },
        "required": ["schema_xml"],
        "additionalProperties": False,
    },
    "render_dcs_schema": {
        "type": "object",
        "properties": {
            "report_id": _string(
                "Report identifier used by the service for the artifact name."
            ),
            "schema_xml": _schema_xml(),
            "parameters": _PARAMETERS,
            "output": {
                "type": "object",
                "default": {"format": "json", "artifacts": []},
            },
            "limits": _LIMITS,
            "correlation_id": _string("Optional correlation id echoed by the service."),
            "requested_user": _string(
                "Optional diagnostic user hint; never impersonation."
            ),
        },
        "required": ["report_id", "schema_xml"],
        "additionalProperties": False,
    },
    "ping_service": {
        "type": "object",
        "properties": {},
        "additionalProperties": False,
    },
    "get_service_info": {
        "type": "object",
        "properties": {},
        "additionalProperties": False,
    },
}


@dataclass(frozen=True, slots=True)
class ToolSpec:
    """One advertised MCP tool and its fixed handler."""

    name: str
    description: str
    input_schema: dict[str, Any]
    handler: Callable[[AIDataReadClient, dict[str, Any]], dict[str, Any]]


def _validated(
    schema: dict[str, Any],
    arguments: Any,
) -> dict[str, Any]:
    if not isinstance(arguments, dict):
        raise LocalError(
            "invalid_argument",
            "Tool arguments must be a JSON object.",
        )
    properties: dict[str, Any] = schema["properties"]
    unknown = sorted(set(arguments) - set(properties))
    if unknown:
        raise LocalError(
            "invalid_argument",
            "Unknown tool argument(s).",
            details={"unknown_fields": unknown},
        )
    for field in schema.get("required", []):
        if field not in arguments:
            raise LocalError(
                "invalid_argument",
                f"Missing required argument: {field}.",
                details={"field": field},
            )
    for field, value in arguments.items():
        expected = properties[field].get("type")
        if expected == "string" and not isinstance(value, str):
            raise LocalError(
                "invalid_argument",
                f"Argument {field} must be a string.",
                details={"field": field, "expected": "string"},
            )
        if expected == "array":
            if not isinstance(value, list):
                raise LocalError(
                    "invalid_argument",
                    f"Argument {field} must be an array.",
                    details={"field": field, "expected": "array"},
                )
            items = properties[field].get("items", {})
            if items.get("type") == "object" and any(
                not isinstance(item, dict) for item in value
            ):
                raise LocalError(
                    "invalid_argument",
                    f"Argument {field} must contain objects only.",
                    details={"field": field, "expected": "array of objects"},
                )
        if expected == "object" and not isinstance(value, dict):
            raise LocalError(
                "invalid_argument",
                f"Argument {field} must be an object.",
                details={"field": field, "expected": "object"},
            )
        enum_values = properties[field].get("enum")
        if enum_values is not None and value not in enum_values:
            raise LocalError(
                "invalid_argument",
                f"Argument {field} has an unsupported value.",
                details={"field": field, "allowed": list(enum_values)},
            )
        # Only query_text/schema_xml must be non-empty. report_id is required by
        # the render route but an empty string is valid: the service truncates
        # it to 80 characters and derives its fileName from it.
        if field in {"query_text", "schema_xml"} and isinstance(value, str) and not value:
            raise LocalError(
                "invalid_argument",
                f"Argument {field} must not be empty.",
                details={"field": field},
            )
        if (
            field == "query_text"
            and isinstance(value, str)
            and len(value) > MAX_QUERY_TEXT_CHARS
        ):
            raise LocalError(
                "invalid_argument",
                f"Argument {field} exceeds the maximum length.",
                details={"field": field, "maxLength": MAX_QUERY_TEXT_CHARS},
            )
        if field == "schema_xml" and isinstance(value, str):
            try:
                schema_bytes = len(value.encode("utf-8"))
            except UnicodeEncodeError as error:
                raise LocalError(
                    "invalid_argument",
                    "Argument schema_xml is not valid UTF-8 text.",
                    details={"field": field},
                ) from error
            if schema_bytes > MAX_SCHEMA_XML_BYTES:
                raise LocalError(
                    "invalid_argument",
                    "Argument schema_xml exceeds the maximum UTF-8 size.",
                    details={"field": field, "maxBytes": MAX_SCHEMA_XML_BYTES},
                )
        if (
            field == "parameters"
            and isinstance(value, list)
            and len(value) > MAX_PARAMETERS
        ):
            raise LocalError(
                "invalid_argument",
                "Argument parameters exceeds the maximum item count.",
                details={"field": field, "maxItems": MAX_PARAMETERS},
            )
    return arguments


def _put_optional(
    body: dict[str, Any], key: str, arguments: dict[str, Any], name: str
) -> None:
    if name in arguments:
        body[key] = arguments[name]


def _query_body(arguments: dict[str, Any]) -> dict[str, Any]:
    body: dict[str, Any] = {
        "query": {
            "language": "1c",
            "text": arguments["query_text"],
        }
    }
    if "parameters" in arguments:
        body["parameters"] = arguments["parameters"]
    if "limits" in arguments:
        body["limits"] = arguments["limits"]
    _put_optional(body, "correlationId", arguments, "correlation_id")
    _put_optional(body, "requestedUser", arguments, "requested_user")
    return body


def _call_validate(
    client: AIDataReadClient, arguments: dict[str, Any]
) -> dict[str, Any]:
    return client.post(QUERY_VALIDATE_PATH, _query_body(arguments))


def _call_execute(
    client: AIDataReadClient, arguments: dict[str, Any]
) -> dict[str, Any]:
    body = _query_body(arguments)
    if "output_format" in arguments:
        body["output"] = {"format": arguments["output_format"]}
    return client.post(QUERY_EXECUTE_PATH, body)


def _dcs_body(arguments: dict[str, Any]) -> dict[str, Any]:
    body: dict[str, Any] = {
        "schemaVersion": 1,
        "schema": {
            "format": "DataCompositionSchemaXml",
            "xml": arguments["schema_xml"],
        },
    }
    if "parameters" in arguments:
        body["parameters"] = arguments["parameters"]
    if "limits" in arguments:
        body["limits"] = arguments["limits"]
    _put_optional(body, "correlationId", arguments, "correlation_id")
    _put_optional(body, "requestedUser", arguments, "requested_user")
    return body


def _call_dcs_execute(
    client: AIDataReadClient, arguments: dict[str, Any]
) -> dict[str, Any]:
    body = _dcs_body(arguments)
    if "output_format" in arguments:
        body["output"] = {"format": arguments["output_format"]}
    return client.post(DCS_EXECUTE_PATH, body)


def _call_dcs_render(
    client: AIDataReadClient, arguments: dict[str, Any]
) -> dict[str, Any]:
    body = _dcs_body(arguments)
    body["reportId"] = arguments["report_id"]
    if "output" in arguments:
        body["output"] = arguments["output"]
    return client.post(DCS_RENDER_PATH, body)


def _call_ping(client: AIDataReadClient, arguments: dict[str, Any]) -> dict[str, Any]:
    del arguments
    return client.get(PING_PATH)


def _call_info(client: AIDataReadClient, arguments: dict[str, Any]) -> dict[str, Any]:
    del arguments
    return client.get(INFO_PATH)


TOOL_SPECS: tuple[ToolSpec, ...] = (
    ToolSpec(
        name="validate_1c_query",
        description="Parse and admit a read-only 1C query without executing it.",
        input_schema=TOOL_SCHEMAS["validate_1c_query"],
        handler=_call_validate,
    ),
    ToolSpec(
        name="execute_1c_query",
        description="Execute an admitted read-only 1C query through AIDataRead.",
        input_schema=TOOL_SCHEMAS["execute_1c_query"],
        handler=_call_execute,
    ),
    ToolSpec(
        name="execute_dcs_schema",
        description="Execute a supported Data Composition Schema into a value collection.",
        input_schema=TOOL_SCHEMAS["execute_dcs_schema"],
        handler=_call_dcs_execute,
    ),
    ToolSpec(
        name="render_dcs_schema",
        description="Render a supported Data Composition Schema and optionally persist XLSX.",
        input_schema=TOOL_SCHEMAS["render_dcs_schema"],
        handler=_call_dcs_render,
    ),
    ToolSpec(
        name="ping_service",
        description="Check AIDataRead service availability through GET /ping/.",
        input_schema=TOOL_SCHEMAS["ping_service"],
        handler=_call_ping,
    ),
    ToolSpec(
        name="get_service_info",
        description="Read AIDataRead version, endpoints, auth, limits, and capabilities.",
        input_schema=TOOL_SCHEMAS["get_service_info"],
        handler=_call_info,
    ),
)

_TOOL_BY_NAME = {spec.name: spec for spec in TOOL_SPECS}


def build_server(settings: Settings) -> Server:
    """Build the low-level MCP server for one explicit AIDataRead connection."""
    client = AIDataReadClient(settings)

    @asynccontextmanager
    async def lifespan(_server: Server):
        try:
            yield {}
        finally:
            client.close()

    server: Server = Server(SERVER_NAME, version=__version__, lifespan=lifespan)

    @server.list_tools()
    async def list_tools() -> list[types.Tool]:
        return [
            types.Tool(
                name=spec.name,
                description=spec.description,
                inputSchema=spec.input_schema,
            )
            for spec in TOOL_SPECS
        ]

    @server.call_tool(validate_input=False)
    async def call_tool(name: str, arguments: dict[str, Any]) -> dict[str, Any]:
        try:
            spec = _TOOL_BY_NAME.get(name)
            if spec is None:
                raise LocalError(
                    "invalid_argument",
                    "Unknown tool.",
                    details={"tool": name},
                )
            validated = _validated(spec.input_schema, arguments)
            payload = await asyncio.to_thread(spec.handler, client, validated)
            if name == "render_dcs_schema":
                payload = await asyncio.to_thread(
                    persist_render_artifacts,
                    payload,
                    output=validated.get("output"),
                    cache_root=settings.cache_root,
                )
            return payload
        except LocalError as error:
            return error.to_envelope()
        except Exception as error:  # noqa: BLE001 - never leak a traceback to stdout
            print(
                f"{SERVER_NAME}: tool {name} failed: {type(error).__name__}",
                file=sys.stderr,
                flush=True,
            )
            return build_local_error(
                "internal_error",
                "Internal MCP error.",
                details={"tool": name},
            )

    return server


async def _serve(settings: Settings) -> None:
    server = build_server(settings)
    async with stdio_server() as (read_stream, write_stream):
        await server.run(
            read_stream,
            write_stream,
            server.create_initialization_options(),
        )


def main() -> int:
    """Run the stdio MCP server; return 1 when explicit configuration is absent."""
    try:
        settings = load_settings()
    except ConfigurationError as error:
        print(
            f"{SERVER_NAME}: configuration error: {error}", file=sys.stderr, flush=True
        )
        return 1
    try:
        asyncio.run(_serve(settings))
    except KeyboardInterrupt:
        return 130
    except Exception as error:  # noqa: BLE001 - stderr diagnostic, stdout stays protocol-only
        print(
            f"{SERVER_NAME}: fatal error: {type(error).__name__}",
            file=sys.stderr,
            flush=True,
        )
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

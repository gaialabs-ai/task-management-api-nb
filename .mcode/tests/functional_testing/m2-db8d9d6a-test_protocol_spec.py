#!/usr/bin/env python3
"""
BE Testing - API Contract Validation Tests

Generated pytest script to validate the API spec against the running application.
Each endpoint is tested as a parameterized test case using pytest.

This script supports two modes:
1. SRC Validation: Tests endpoints and captures responses (no expected_response)
2. DST Contract Validation: Tests endpoints and validates responses match expected (has expected_response)

Generated at: 2026-03-03T13:35:14.850927+00:00
Project: task-management-api-src
Milestone: 2
"""

import json
import os
import re
import sys
import time
from typing import Any

import pytest
import requests

# =============================================================================
# Test Configuration (embedded from spec validation)
# =============================================================================

_ENV_PLACEHOLDER = re.compile(r'\$\{([A-Za-z_][A-Za-z0-9_]*)\}')


def resolve_env_placeholders(obj: Any) -> Any:
    """Recursively resolve ${VAR_NAME} environment variable placeholders in test data.

    Only resolves braced ${VAR} syntax to avoid unintentional expansion of
    unrelated $VAR patterns (e.g. $HOME, $stored.KEY).
    """
    if isinstance(obj, str):
        return _ENV_PLACEHOLDER.sub(lambda m: os.environ.get(m.group(1), m.group(0)), obj)
    if isinstance(obj, dict):
        return {k: resolve_env_placeholders(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [resolve_env_placeholders(item) for item in obj]
    return obj


# Parse JSON at runtime, then resolve any ${VAR_NAME} env var placeholders
# that the agent may have substituted for detected secrets.
TEST_CASES: list[dict[str, Any]] = resolve_env_placeholders(
    json.loads(r'''[
    {
        "name": "health_check_happy_path",
        "category": "HAPPY_PATH",
        "endpoint": "/health",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {},
            "body": null
        },
        "expected_status": 200,
        "description": "Health endpoint returns exact JSON structure: status=healthy, service=task-api-go, version=1.0.0",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_default_pagination",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {},
            "body": null
        },
        "expected_status": 200,
        "description": "List tasks with no params returns 200 with tasks array and total_count",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_page_size_boundary_max",
        "category": "BOUNDARY",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {
                "page_size": "100"
            },
            "body": null
        },
        "expected_status": 200,
        "description": "page_size=100 is the max allowed value \u2014 should return 200 with up to 100 tasks",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_page_size_exceeds_max",
        "category": "INVALID_INPUT",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {
                "page_size": "101"
            },
            "body": null
        },
        "expected_status": 400,
        "description": "page_size > 100 should return 400 VALIDATION_ERROR",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_page_size_zero",
        "category": "INVALID_INPUT",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {
                "page_size": "0"
            },
            "body": null
        },
        "expected_status": 400,
        "description": "page_size=0 is below minimum of 1, should return 400 VALIDATION_ERROR",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_filter_by_status",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {
                "status": "pending"
            },
            "body": null
        },
        "expected_status": 200,
        "description": "Filtering by status=pending should return 200 with matching tasks only (case-sensitive match)",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_filter_by_tags_and_logic",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {
                "tags": "go,api"
            },
            "body": null
        },
        "expected_status": 200,
        "description": "Tags filter with comma-separated values applies AND logic \u2014 only tasks with ALL specified tags returned",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_sort_by_priority_desc",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {
                "sort_order": "priority_desc"
            },
            "body": null
        },
        "expected_status": 200,
        "description": "Sorting by priority_desc should return 200 with tasks ordered highest priority first",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "list_tasks_invalid_page_token",
        "category": "BOUNDARY",
        "endpoint": "/api/v1/tasks",
        "method": "GET",
        "request_data": {
            "path": {},
            "query": {
                "page_token": "not-a-number"
            },
            "body": null
        },
        "expected_status": 200,
        "description": "Non-numeric page_token is silently treated as startIndex=0, returns 200",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "create_task_happy_path",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_data": {
            "path": {},
            "query": {},
            "body": {
                "title": "Test Task for Creation",
                "description": "A task created by the test suite",
                "priority": "high",
                "tags": [
                    "test",
                    "api"
                ]
            }
        },
        "expected_status": 201,
        "description": "Creating a task with valid fields returns 201, Location header, and task JSON with tags always included",
        "setup": null,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "create_task_with_all_optional_fields",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_data": {
            "path": {},
            "query": {},
            "body": {
                "title": "Full Task",
                "description": "Task with all optional fields set",
                "priority": "critical",
                "tags": [
                    "integration",
                    "full"
                ],
                "assigned_to": "user@example.com",
                "due_date": "2026-12-31T23:59:59Z"
            }
        },
        "expected_status": 201,
        "description": "Task with assigned_to and due_date returns 201; these fields are present in response when set",
        "setup": null,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "create_task_missing_required_title",
        "category": "MISSING_REQUIRED",
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_data": {
            "path": {},
            "query": {},
            "body": {
                "description": "No title provided",
                "priority": "low"
            }
        },
        "expected_status": 400,
        "description": "Missing required title field returns 400 with VALIDATION_ERROR code",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "create_task_title_too_long",
        "category": "INVALID_INPUT",
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_data": {
            "path": {},
            "query": {},
            "body": {
                "title": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                "description": "Title exceeds 200 character limit"
            }
        },
        "expected_status": 400,
        "description": "Title exceeding 200 characters returns 400 with VALIDATION_ERROR code",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "create_task_invalid_priority",
        "category": "INVALID_INPUT",
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_data": {
            "path": {},
            "query": {},
            "body": {
                "title": "Task with bad priority",
                "priority": "ultra"
            }
        },
        "expected_status": 400,
        "description": "Invalid priority value returns 400 with VALIDATION_ERROR code",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "create_task_default_priority_medium",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks",
        "method": "POST",
        "request_data": {
            "path": {},
            "query": {},
            "body": {
                "title": "Task Without Priority"
            }
        },
        "expected_status": 201,
        "description": "Task created without priority defaults to medium; tags is always [] (not omitted) in response",
        "setup": null,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "get_task_happy_path",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "GET",
        "description": "Create a task, retrieve it by ID, verify JSON structure, then delete it",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Get Task Test",
                "description": "Task for retrieval testing",
                "priority": "medium",
                "tags": [
                    "test"
                ]
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": null
        },
        "expected_status": 200,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "get_task_not_found",
        "category": "NOT_FOUND",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "GET",
        "request_data": {
            "path": {
                "id": "00000000-0000-0000-0000-000000000000"
            },
            "query": {},
            "body": null
        },
        "expected_status": 404,
        "description": "Fetching a non-existent task ID returns 404 with NOT_FOUND error code",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "update_task_happy_path",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "PUT",
        "description": "Create a task, partially update its title and status, then delete it",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Task To Update",
                "description": "Original description",
                "priority": "low",
                "tags": [
                    "original"
                ]
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": {
                "title": "Updated Task Title",
                "status": "in_progress"
            }
        },
        "expected_status": 200,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "update_task_not_found",
        "category": "NOT_FOUND",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "PUT",
        "request_data": {
            "path": {
                "id": "00000000-0000-0000-0000-000000000000"
            },
            "query": {},
            "body": {
                "title": "Updated Title"
            }
        },
        "expected_status": 404,
        "description": "Updating a non-existent task returns 404 with NOT_FOUND error code",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "update_task_invalid_priority",
        "category": "INVALID_INPUT",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "PUT",
        "description": "Updating task with invalid priority value returns 400 with VALIDATION_ERROR code",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Task For Invalid Update",
                "priority": "low"
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": {
                "priority": "super_high"
            }
        },
        "expected_status": 400,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "update_task_status_to_completed_sets_completed_at",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "PUT",
        "description": "Create a task, update status to completed \u2014 completed_at should appear in response",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Task To Complete",
                "priority": "medium"
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": {
                "status": "completed"
            }
        },
        "expected_status": 200,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "patch_task_status_happy_path",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks/{id}/status",
        "method": "PATCH",
        "description": "Create a task, patch its status to in_progress, then delete it",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Task For Status Patch",
                "priority": "high"
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": {
                "status": "in_progress"
            }
        },
        "expected_status": 200,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "patch_task_status_invalid_value",
        "category": "INVALID_INPUT",
        "endpoint": "/api/v1/tasks/{id}/status",
        "method": "PATCH",
        "description": "PATCH status with invalid status value returns 400 with VALIDATION_ERROR code",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Task For Bad Status Patch",
                "priority": "low"
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": {
                "status": "not_a_status"
            }
        },
        "expected_status": 400,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "patch_task_status_missing_status_field",
        "category": "MISSING_REQUIRED",
        "endpoint": "/api/v1/tasks/{id}/status",
        "method": "PATCH",
        "description": "PATCH status with missing status field returns 400 with VALIDATION_ERROR code",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Task For Missing Status Patch"
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": {}
        },
        "expected_status": 400,
        "cleanup": {
            "endpoint": "/api/v1/tasks/{id}",
            "method": "DELETE",
            "path": {
                "id": "$setup_id"
            }
        }
    },
    {
        "name": "patch_task_status_not_found",
        "category": "NOT_FOUND",
        "endpoint": "/api/v1/tasks/{id}/status",
        "method": "PATCH",
        "request_data": {
            "path": {
                "id": "00000000-0000-0000-0000-000000000000"
            },
            "query": {},
            "body": {
                "status": "cancelled"
            }
        },
        "expected_status": 404,
        "description": "PATCH status on non-existent task returns 404 with NOT_FOUND error code",
        "setup": null,
        "cleanup": null
    },
    {
        "name": "delete_task_happy_path",
        "category": "HAPPY_PATH",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "DELETE",
        "description": "Create a task then delete it \u2014 expects HTTP 204 with no body",
        "setup": {
            "endpoint": "/api/v1/tasks",
            "method": "POST",
            "body": {
                "title": "Task To Delete",
                "priority": "low"
            },
            "extract_id_from": "id"
        },
        "request_data": {
            "path": {
                "id": "$setup_id"
            },
            "query": {},
            "body": null
        },
        "expected_status": 204,
        "cleanup": null
    },
    {
        "name": "delete_task_not_found",
        "category": "NOT_FOUND",
        "endpoint": "/api/v1/tasks/{id}",
        "method": "DELETE",
        "request_data": {
            "path": {
                "id": "00000000-0000-0000-0000-000000000000"
            },
            "query": {},
            "body": null
        },
        "expected_status": 404,
        "description": "Deleting a non-existent task returns 404 with NOT_FOUND error code",
        "setup": null,
        "cleanup": null
    }
]''')
)

# Base URL for API requests (from app discovery, includes host:port)
BASE_URL = os.path.expandvars("")
HEALTH_CHECK_ENDPOINT = os.path.expandvars("")
REQUEST_TIMEOUT = 30
HEALTH_CHECK_URL = f"{BASE_URL.rstrip('/')}/{HEALTH_CHECK_ENDPOINT.lstrip('/')}"
# Per-endpoint routing table for microservices DST
# Maps "METHOD /path" -> {"base_url": "http://host:port", "endpoint": "/new/path"}
# When empty, all requests use BASE_URL (backward-compatible default)
ENDPOINT_ROUTING: dict[str, dict[str, str]] = json.loads(r'''{}''')

# =============================================================================
# HTTP Sessions
# =============================================================================

# Two sessions: authenticated (carries auth state) and anonymous (for skip_auth tests).
# Using requests.Session gives us automatic cookie persistence, connection pooling,
# and header reuse — behaving like a real HTTP client.
#
# Auth starts empty.  Login/register tests propagate credentials to _auth_session
# via store_auth or auto-detect (see update_auth_from_response).
_auth_session: requests.Session = requests.Session()
_anon_session: requests.Session = requests.Session()

# =============================================================================
# Response Contract Validation Utilities
# =============================================================================

# Fields that are expected to differ between SRC and DST (dynamic values)
# These fields are checked for presence and type, but values are not compared
DYNAMIC_FIELDS = {
    "id", "uuid", "created_at", "updated_at", "timestamp", "modified_at",
    "created_date", "updated_date", "last_modified", "date_created", "date_updated",
    "_id", "createdAt", "updatedAt", "modifiedAt", "token", "access_token",
    "refresh_token", "session_id", "request_id", "trace_id", "correlation_id",
}



def get_type_name(value: Any) -> str:
    """Get a human-readable type name for a value."""
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, int):
        return "integer"
    if isinstance(value, float):
        return "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "array"
    if isinstance(value, dict):
        return "object"
    return type(value).__name__


def is_same_type(actual: Any, expected: Any) -> bool:
    """Check if two values have compatible types."""
    if actual is None and expected is None:
        return True
    if actual is None or expected is None:
        return False
    if isinstance(actual, bool) or isinstance(expected, bool):
        return isinstance(actual, bool) and isinstance(expected, bool)
    if isinstance(actual, (int, float)) and isinstance(expected, (int, float)):
        return True
    return type(actual) == type(expected)


def validate_sensitive_field(
    actual: Any,
    expected_meta: dict[str, Any],
    path: str = "",
) -> tuple[bool, list[str]]:
    """Validate actual value against a sanitized sensitive field descriptor.

    Sensitive values (JWTs, tokens, etc.) are replaced with metadata markers
    during SRC validation so no secret material is persisted.  Instead of
    comparing exact values (which are non-deterministic), this checks type
    compatibility, format hints, and approximate length.
    """
    violations: list[str] = []
    expected_type = expected_meta.get("type", "string")
    actual_type = get_type_name(actual)

    if actual_type != expected_type:
        violations.append(
            f"{path or 'root'}: type mismatch on sensitive field - "
            f"expected {expected_type}, got {actual_type}"
        )
        return False, violations

    if expected_type == "string" and isinstance(actual, str):
        expected_format = expected_meta.get("format", "")
        expected_length = expected_meta.get("length", 0)

        # JWT format check
        if expected_format == "jwt" and not actual.startswith("eyJ"):
            violations.append(f"{path or 'root'}: expected JWT format token")

        # Approximate length check (wide tolerance — tokens may vary in size)
        if expected_length > 0 and len(actual) < max(int(expected_length * 0.2), 8):
            violations.append(
                f"{path or 'root'}: sensitive value too short - "
                f"expected ~{expected_length} chars, got {len(actual)}"
            )

    return len(violations) == 0, violations


def validate_contract(
    actual: Any,
    expected: Any,
    path: str = "",
) -> tuple[bool, list[str]]:
    """Validate that actual response matches the expected response contract.

    Checks structure, types, and values.  Dynamic fields (id, timestamps, tokens)
    are only checked for presence and type — values are expected to differ.
    """
    violations: list[str] = []

    if expected is None:
        return True, []
    if actual is None:
        violations.append(f"{path or 'root'}: expected {get_type_name(expected)} but got null")
        return False, violations

    # Handle sanitized sensitive values (JWTs, tokens replaced with metadata markers).
    # Must come before type check since the marker is a dict but actual is a string.
    if isinstance(expected, dict) and expected.get("__sensitive__"):
        return validate_sensitive_field(actual, expected, path)

    if not is_same_type(actual, expected):
        violations.append(
            f"{path or 'root'}: type mismatch - expected {get_type_name(expected)}, "
            f"got {get_type_name(actual)}"
        )
        return False, violations

    if isinstance(expected, dict):
        for key, exp_val in expected.items():
            key_path = f"{path}.{key}" if path else key
            if key not in actual:
                violations.append(f"{key_path}: missing required field")
                continue
            if key.lower() in {f.lower() for f in DYNAMIC_FIELDS}:
                if isinstance(exp_val, dict) and exp_val.get("__sensitive__"):
                    _, sub = validate_sensitive_field(actual[key], exp_val, key_path)
                    violations.extend(sub)
                elif not is_same_type(actual[key], exp_val):
                    violations.append(
                        f"{key_path}: type mismatch - expected {get_type_name(exp_val)}, "
                        f"got {get_type_name(actual[key])}"
                    )
            else:
                _, sub = validate_contract(actual[key], exp_val, key_path)
                violations.extend(sub)
        return len(violations) == 0, violations

    if isinstance(expected, list):
        if len(actual) != len(expected):
            violations.append(
                f"{path or 'root'}: array length mismatch - expected {len(expected)} items, "
                f"got {len(actual)} items"
            )
        for i, (act_item, exp_item) in enumerate(zip(actual, expected)):
            _, sub = validate_contract(act_item, exp_item, f"{path}[{i}]" if path else f"[{i}]")
            violations.extend(sub)
        return len(violations) == 0, violations

    # Scalar comparison (str, int, float, bool)
    if actual != expected:
        violations.append(f"{path or 'root'}: value mismatch - expected {expected!r}, got {actual!r}")
        return False, violations
    return True, []


def format_response_diff(differences: list[str], max_items: int = 10) -> str:
    """Format response differences for error message."""
    if not differences:
        return "No differences"
    lines = [f"  - {d}" for d in differences[:max_items]]
    if len(differences) > max_items:
        lines.append(f"  ... and {len(differences) - max_items} more differences")
    return "\n".join(lines)


# =============================================================================
# Test Results Collection
# =============================================================================

test_results: list[dict[str, Any]] = []


def record_result(
    name: str,
    endpoint: str,
    method: str,
    expected_status: int,
    actual_status: int,
    passed: bool,
    duration_ms: float,
    category: str | None = None,
    description: str | None = None,
    error: str | None = None,
    response_body: str | None = None,
    response_match: bool | None = None,
) -> None:
    """Record a test result for final JSON output."""
    result: dict[str, Any] = {
        "name": name,
        "endpoint": endpoint,
        "method": method,
        "expected_status": expected_status,
        "actual_status": actual_status,
        "passed": passed,
        "duration_ms": duration_ms,
        "category": category,
        "description": description,
    }
    if error:
        result["error"] = error
    if response_match is not None:
        result["response_match"] = response_match
    if response_body:
        if passed:
            try:
                result["response"] = json.loads(response_body)
            except json.JSONDecodeError:
                result["response_body"] = response_body
        else:
            result["response_body"] = response_body
    test_results.append(result)


# =============================================================================
# Response Store & Auth Cascade
# =============================================================================

# In-memory store for values extracted from responses and shared across test cases.
# Test cases with a "store" field save values here; later tests reference them
# via "$stored.KEY" placeholders.
_response_store: dict[str, Any] = {}


def extract_by_json_path(data: Any, json_path: str) -> Any:
    """Extract a value from nested data using a dot-separated JSON path.

    E.g. "data.users.0.id" -> data["data"]["users"][0]["id"]
    """
    current = data
    for key in json_path.split("."):
        if current is None:
            return None
        if isinstance(current, dict):
            current = current.get(key)
        elif isinstance(current, list):
            try:
                current = current[int(key)]
            except (ValueError, IndexError):
                return None
        else:
            return None
    return current


def store_response_values(test_case: dict[str, Any], response_json: Any) -> None:
    """Extract values from a response and save them in the response store."""
    store_config = test_case.get("store")
    if not store_config or not isinstance(store_config, dict):
        return
    if not isinstance(response_json, (dict, list)):
        return
    for placeholder_name, json_path in store_config.items():
        if not isinstance(json_path, str):
            continue
        value = extract_by_json_path(response_json, json_path)
        if value is not None:
            _response_store[placeholder_name] = value
            print(f"  Stored: ${placeholder_name} = <{len(str(value))} chars>")
        else:
            print(f"  Warning: store path '{json_path}' resolved to None for '{placeholder_name}'")


def update_auth_from_response(test_case: dict[str, Any], response_json: Any) -> None:
    """Update the auth session from a test response.

    Called after a successful test that has ``store_auth`` configuration or
    category ``AUTH``/``SETUP``.  This lets login/register tests propagate
    credentials to all subsequent requests via ``_auth_session``.

    ``store_auth`` format::

        {
            "headers": {"Authorization": "Bearer {access_token}"},
            "cookies": {"session_id": "session_id"}
        }

    Cookies from ``Set-Cookie`` response headers are persisted automatically
    by the session — no explicit handling is needed.
    """
    store_auth = test_case.get("store_auth")
    category = (test_case.get("category") or "").upper()

    if not store_auth and category not in ("SETUP", "AUTH"):
        return

    # --- Explicit store_auth ---
    explicit_set = False
    if store_auth and isinstance(store_auth, dict):
        for header_name, template in store_auth.get("headers", {}).items():
            resolved = str(template)
            for key, value in _response_store.items():
                resolved = resolved.replace(f"{{{key}}}", str(value))
            if isinstance(response_json, dict):
                for key, value in response_json.items():
                    if isinstance(value, (str, int, float, bool)):
                        resolved = resolved.replace(f"{{{key}}}", str(value))
            if re.search(r"\{[a-zA-Z_]\w*\}", resolved):
                print(f"  Warning: unresolved placeholder in '{header_name}', skipping: {resolved}")
                continue
            _auth_session.headers[header_name] = resolved
            explicit_set = True

        for cookie_name, json_path in store_auth.get("cookies", {}).items():
            if isinstance(response_json, (dict, list)):
                value = extract_by_json_path(response_json, json_path)
                if value is not None:
                    _auth_session.cookies.set(cookie_name, str(value))
                    explicit_set = True

        if explicit_set:
            custom_headers = [k for k in _auth_session.headers if k.lower() not in
                              ("user-agent", "accept-encoding", "accept", "connection")]
            print(f"  Auth session updated: headers={custom_headers}, cookies={list(_auth_session.cookies.keys())}")
            return
        print("  Warning: store_auth did not resolve, trying auto-detect")

    # --- Auto-detect auth from SETUP/AUTH category tests ---
    if isinstance(response_json, dict):
        for field in ("access_token", "token", "accessToken", "jwt",
                       "id_token", "idToken", "auth_token", "authToken"):
            value = response_json.get(field)
            if value and isinstance(value, str):
                prefix = "Bearer " if field != "jwt" else ""
                _auth_session.headers["Authorization"] = f"{prefix}{value}"
                print(f"  Auth auto-detected: Authorization set from '{field}'")
                break


def resolve_stored_placeholders(obj: Any) -> Any:
    """Replace $stored.KEY placeholders with values from the response store.

    - Exact: "$stored.key" -> stored value (preserves type)
    - Embedded: "Bearer $stored.token" -> string interpolation
    - Recursive through dicts and lists
    """
    if not _response_store:
        return obj
    if isinstance(obj, str):
        if obj.startswith("$stored."):
            key = obj[len("$stored."):]
            if key in _response_store:
                return _response_store[key]
        if "$stored." in obj:
            result = obj
            for key, value in _response_store.items():
                result = result.replace(f"$stored.{key}", str(value))
            return result
        return obj
    if isinstance(obj, dict):
        return {k: resolve_stored_placeholders(v) for k, v in obj.items()}
    if isinstance(obj, list):
        return [resolve_stored_placeholders(item) for item in obj]
    return obj


# =============================================================================
# HTTP Helpers
# =============================================================================


def build_url(
    endpoint: str,
    path_params: dict[str, Any],
    query_params: dict[str, Any],
    method: str = "GET",
) -> str:
    """Build the full request URL with path and query parameters.

    When ENDPOINT_ROUTING is populated (microservices DST), looks up the
    routing table by "METHOD /endpoint" to resolve a per-service base_url
    and optionally a remapped endpoint path. Falls back to BASE_URL when
    no routing entry matches.
    """
    base_url = BASE_URL
    routed_endpoint = endpoint

    if ENDPOINT_ROUTING:
        key = f"{method.upper()} {endpoint}"
        route = ENDPOINT_ROUTING.get(key)
        if route:
            base_url = route.get("base_url", BASE_URL)
            routed_endpoint = route.get("endpoint", endpoint)

    for name, value in path_params.items():
        routed_endpoint = routed_endpoint.replace(f"{{{name}}}", str(value))
    if query_params:
        routed_endpoint = f"{routed_endpoint}?{'&'.join(f'{k}={v}' for k, v in query_params.items())}"
    return f"{base_url.rstrip('/')}{routed_endpoint}"


def make_request(
    method: str,
    endpoint: str,
    path_params: dict[str, Any],
    query_params: dict[str, Any],
    body: Any,
    request_headers: dict[str, str] | None = None,
    skip_auth: bool = False,
    content_type: str | None = None,
) -> requests.Response:
    """Make an HTTP request using the appropriate session.

    Authenticated requests use ``_auth_session`` (carries auth headers +
    cookie jar).  ``skip_auth`` requests use ``_anon_session``.

    Set ``content_type`` to ``"form"`` for form-urlencoded bodies (OAuth2).
    """
    url = build_url(endpoint, path_params, query_params, method=method)
    session = _anon_session if skip_auth else _auth_session

    body_kwargs: dict[str, Any] = {}
    if body is not None:
        if content_type and "form" in content_type.lower():
            body_kwargs["data"] = body
        else:
            body_kwargs["json"] = body

    return session.request(
        method.upper(), url,
        headers=request_headers or {},
        timeout=REQUEST_TIMEOUT,
        **body_kwargs,
    )


def run_setup(setup_config: dict[str, Any]) -> str | None:
    """Run a setup request and return the extracted ID.

    Handles 'already exists' (409/422) gracefully by trying to recover the
    existing resource ID from the error response or a follow-up GET.
    """
    endpoint = setup_config.get("endpoint", "/")
    method = setup_config.get("method", "POST")
    body = resolve_stored_placeholders(setup_config.get("body"))
    extract_path = setup_config.get("extract_id_from", "id")
    ct = setup_config.get("content_type")

    resp = make_request(method, endpoint, {}, {}, body, content_type=ct)

    if resp.status_code < 400:
        extracted = extract_by_json_path(resp.json() if resp.text else {}, extract_path)
        if extracted is not None:
            return str(extracted)
        print(f"Setup: Created resource but could not extract ID via '{extract_path}'")
        return None

    if resp.status_code in (409, 422):
        print(f"Setup: Resource may already exist ({resp.status_code}). Attempting recovery...")
        try:
            extracted = extract_by_json_path(resp.json(), extract_path)
            if extracted is not None:
                print(f"  Recovered ID from error response: {extracted}")
                return str(extracted)
        except (json.JSONDecodeError, TypeError):
            pass
        try:
            get_resp = make_request("GET", endpoint, {}, {}, None)
            if get_resp.status_code < 400 and get_resp.text:
                get_data = get_resp.json()
                if isinstance(get_data, list) and get_data:
                    get_data = get_data[-1]
                extracted = extract_by_json_path(get_data, extract_path)
                if extracted is not None:
                    print(f"  Recovered ID via GET: {extracted}")
                    return str(extracted)
        except (json.JSONDecodeError, TypeError, requests.RequestException) as e:
            print(f"  GET fallback failed: {e}")
        print(f"Setup: Could not recover. Response: {resp.text[:500]}")
        return None

    print(f"Setup failed: {resp.status_code} - {resp.text[:500]}")
    return None


def run_cleanup(cleanup_config: dict[str, Any], setup_id: str | None) -> None:
    """Run a cleanup request (best effort, errors logged but don't fail)."""
    if not cleanup_config:
        return
    endpoint = cleanup_config.get("endpoint", "/")
    method = cleanup_config.get("method", "DELETE")
    path_params = dict(cleanup_config.get("path", {}))
    body = cleanup_config.get("body")
    if setup_id:
        path_params = {k: (setup_id if v == "$setup_id" else v) for k, v in path_params.items()}
    try:
        resp = make_request(method, endpoint, path_params, {}, body)
        if resp.status_code >= 400:
            print(f"Cleanup warning: {resp.status_code} - {resp.text}")
    except Exception as e:
        print(f"Cleanup warning: {e}")


# =============================================================================
# Pytest Fixtures
# =============================================================================


@pytest.fixture(scope="session", autouse=True)
def wait_for_app_health() -> None:
    """Wait for the application to be healthy before running tests."""
    print(f"\nWaiting for app to be healthy at {HEALTH_CHECK_URL}...")
    max_attempts = 60
    for attempt in range(max_attempts):
        try:
            resp = _anon_session.get(HEALTH_CHECK_URL, timeout=5)
            if resp.status_code < 400:
                print(f"App is healthy after {attempt + 1} attempts")
                return
        except Exception as e:
            if attempt % 10 == 0:
                print(f"Health check attempt {attempt + 1}/{max_attempts}: {e}")
        time.sleep(2)
    pytest.fail(f"Application failed health check at {HEALTH_CHECK_URL} after {max_attempts} attempts")


# =============================================================================
# Test Cases
# =============================================================================


def get_test_ids() -> list[str]:
    """Generate test IDs for parametrization."""
    return [tc.get("name", f"test_{i}") for i, tc in enumerate(TEST_CASES)]


@pytest.mark.parametrize("test_case", TEST_CASES, ids=get_test_ids())
def test_api_endpoint(test_case: dict[str, Any]) -> None:
    """Test a single API endpoint based on test case configuration."""
    name = test_case.get("name", "unnamed")
    endpoint = test_case.get("endpoint", "/")
    method = test_case.get("method", "GET").upper()
    expected_status = test_case.get("expected_status", 200)
    request_data = test_case.get("request_data", {})
    category = test_case.get("category")
    description = test_case.get("description")
    setup_config = test_case.get("setup")
    cleanup_config = test_case.get("cleanup")
    skip_auth = test_case.get("skip_auth", False)
    expected_response = test_case.get("actual_response") or test_case.get("expected_response")

    setup_id: str | None = None

    try:
        # --- Setup ---
        if setup_config:
            setup_id = run_setup(setup_config)
            setup_required = setup_config.get("required", True) if isinstance(setup_config, dict) else True
            if setup_id is None and setup_required:
                record_result(name=name, endpoint=endpoint, method=method,
                              expected_status=expected_status, actual_status=0, passed=False,
                              duration_ms=0, category=category, description=description,
                              error=f"Setup failed - cannot create required resource for test '{name}'")
                pytest.fail(f"Setup failed for test '{name}' - cannot create required resource")

        # --- Build request ---
        path_params = dict(request_data.get("path", {}))
        query_params = dict(request_data.get("query", {}))
        req_headers = request_data.get("headers", {})
        body = request_data.get("body")
        content_type = request_data.get("content_type")

        if setup_id:
            path_params = {k: (setup_id if v == "$setup_id" else v) for k, v in path_params.items()}

        # Resolve $stored.* placeholders from previous test responses
        path_params = resolve_stored_placeholders(path_params)
        query_params = resolve_stored_placeholders(query_params)
        req_headers = resolve_stored_placeholders(req_headers)
        body = resolve_stored_placeholders(body)
        endpoint = resolve_stored_placeholders(endpoint)

        # --- Execute ---
        start_time = time.time()
        try:
            resp = make_request(method, endpoint, path_params, query_params, body,
                                req_headers, skip_auth, content_type)
            duration_ms = (time.time() - start_time) * 1000
            actual_status = resp.status_code
            response_body = resp.text

            # Store values & propagate auth (before assertions)
            status_passed = actual_status == expected_status
            if status_passed:
                try:
                    resp_json = json.loads(response_body)
                    store_response_values(test_case, resp_json)
                    update_auth_from_response(test_case, resp_json)
                except (json.JSONDecodeError, TypeError):
                    pass

            error_msg: str | None = None if status_passed else (
                f"Expected status {expected_status}, got {actual_status}"
            )

            # --- Contract validation (DST mode) ---
            response_match: bool | None = None
            if expected_response is not None and status_passed:
                try:
                    actual_json = json.loads(response_body)
                    response_match, diff = validate_contract(actual_json, expected_response)
                    if not response_match:
                        error_msg = f"Response contract violation:\n{format_response_diff(diff)}"
                except json.JSONDecodeError:
                    response_match = False
                    error_msg = "Response is not valid JSON but contract validation is required"

            passed = status_passed and (response_match is None or response_match)

            record_result(name=name, endpoint=endpoint, method=method,
                          expected_status=expected_status, actual_status=actual_status,
                          passed=passed, duration_ms=duration_ms, category=category,
                          description=description, error=error_msg,
                          response_body=response_body, response_match=response_match)

            if not status_passed:
                pytest.fail(
                    f"Test '{name}': Expected status {expected_status}, got {actual_status}. "
                    f"Response: {response_body or 'empty'}"
                )
            if response_match is False:
                pytest.fail(f"Test '{name}': Response contract violation (DST validation).\n{error_msg}")

        except requests.RequestException as e:
            duration_ms = (time.time() - start_time) * 1000
            record_result(name=name, endpoint=endpoint, method=method,
                          expected_status=expected_status, actual_status=0, passed=False,
                          duration_ms=duration_ms, category=category, description=description,
                          error=str(e))
            pytest.fail(f"Test '{name}': Request failed with error: {e}")

    except Exception as e:
        record_result(name=name, endpoint=endpoint, method=method,
                      expected_status=expected_status, actual_status=0, passed=False,
                      duration_ms=0, category=category, description=description,
                      error=f"Test error: {type(e).__name__}: {e}")
        raise

    finally:
        if cleanup_config:
            run_cleanup(cleanup_config, setup_id)


# =============================================================================
# Test Results Output
# =============================================================================


@pytest.fixture(scope="session", autouse=True)
def output_test_results(request: pytest.FixtureRequest) -> Any:
    """Output test results in JSON format after all tests complete."""
    yield

    passed_count = sum(1 for r in test_results if r["passed"])
    failed_count = len(test_results) - passed_count
    total_count = len(test_results)

    response_validated = sum(1 for r in test_results if r.get("response_match") is not None)
    response_matched = sum(1 for r in test_results if r.get("response_match") is True)

    output: dict[str, Any] = {
        "all_passed": failed_count == 0 and total_count > 0,
        "passed_count": passed_count,
        "failed_count": failed_count,
        "total_count": total_count,
        "results": test_results,
        "failures": [r for r in test_results if not r["passed"]],
    }
    if response_validated > 0:
        output["contract_validation"] = {
            "tests_with_expected_response": response_validated,
            "response_matches": response_matched,
            "response_mismatches": response_validated - response_matched,
        }

    print("\n" + "=" * 60)
    print(f"Results: {passed_count}/{total_count} passed")
    if response_validated > 0:
        print(f"Contract validation: {response_matched}/{response_validated} responses matched")
    print("=" * 60)
    print(json.dumps(output))
    sys.stdout.flush()

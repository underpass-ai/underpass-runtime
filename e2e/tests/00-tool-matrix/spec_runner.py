"""Adversarial per-tool spec engine for the E2E tool matrix.

A *spec* is an authored set of cases for one tool (specs/<family>.yaml). Unlike
the catalog `examples` — which are partly non-executable placeholders — specs
carry args that really run against the known fixture workspace plus adversarial
cases designed to surface bugs, each with an explicit expected outcome. The
engine runs every case, checks the actual result against the expectation, and
classifies it:

  pass   — actual matched the expectation
  BUG    — actual diverged from the expectation (a finding to report)
  skip   — the case needs a backend/profile that isn't provisioned (recorded,
           never a failure)

Case categories (informational, all enforced the same way):
  capability  — exercise one mode/option of the tool's input_schema
  adversarial — malformed/oversized/conflicting/edge input meant to break it
  governance  — must be denied (traversal, missing approval, invalid args)

Expectation grammar (per case `expect`):
  status: succeeded | failed | denied        # invocation status
  error_code: <code>                          # exact error code when not succeeded
  error_code_in: [<code>, ...]                # any-of
  output: {field: value}                      # exact-match output fields
  output_present: [field, ...]                # fields that must exist
  output_match: {field: "regex"}              # regex on a string field
  skip_if_error_code: [connection_failed,...] # treat these as skip, not bug
"""

from __future__ import annotations

import json
import os
import re

try:
    import yaml
except ImportError:  # pragma: no cover
    yaml = None

SPECS_DIR = os.getenv("SPECS_DIR", os.path.join(os.path.dirname(__file__), "specs"))

# Error codes that mean "the backend/dependency isn't here" — recorded as skip,
# never a bug, so the suite is honest on a partially-provisioned cluster.
DEFAULT_BACKEND_SKIP = {
    "connection_failed", "connection_refused", "dial_error", "timeout",
    "deadline_exceeded", "unavailable", "no_profile", "profile_not_found",
    "backend_unavailable",
}


def load_specs(path: str = SPECS_DIR) -> dict:
    """Load every specs/<family>.yaml into {tool_name: [case, ...]}."""
    specs: dict = {}
    if not yaml or not os.path.isdir(path):
        return specs
    for fname in sorted(os.listdir(path)):
        if not fname.endswith((".yaml", ".yml")):
            continue
        with open(os.path.join(path, fname), encoding="utf-8") as fh:
            doc = yaml.safe_load(fh) or {}
        for tool in doc.get("tools", []):
            name = tool.get("tool")
            if name:
                specs[name] = tool.get("cases", [])
    return specs


def _get(d: dict, dotted: str):
    cur = d
    for part in dotted.split("."):
        if isinstance(cur, list):
            try:
                cur = cur[int(part)]
            except (ValueError, IndexError):
                return None
        elif isinstance(cur, dict):
            cur = cur.get(part)
        else:
            return None
    return cur


def check_case(expect: dict, status: str, error_code: str, output: dict) -> tuple[str, str]:
    """Compare an invocation result against a case expectation.

    Returns (verdict, detail) where verdict is "pass", "bug", or "skip".
    """
    skip_codes = set(expect.get("skip_if_error_code", [])) | DEFAULT_BACKEND_SKIP
    want_status = expect.get("status")

    # Backend/dependency absent → skip (only when we didn't expect that failure).
    if status != "succeeded" and error_code in skip_codes and want_status != status:
        return "skip", f"backend/dep absent ({error_code})"

    # Status check.
    if want_status and status != want_status:
        return "bug", f"status: want {want_status}, got {status} ({error_code or '-'})"

    # Error-code checks (when failure/denial expected).
    want_code = expect.get("error_code")
    if want_code and error_code != want_code:
        return "bug", f"error_code: want {want_code}, got {error_code or '-'}"
    code_in = expect.get("error_code_in")
    if code_in and error_code not in code_in:
        return "bug", f"error_code: want one of {code_in}, got {error_code or '-'}"

    # Output field exact match.
    for field, want in (expect.get("output") or {}).items():
        got = _get(output, field)
        if got != want:
            return "bug", f"output.{field}: want {want!r}, got {got!r}"

    # Output presence.
    for field in expect.get("output_present", []):
        if _get(output, field) is None:
            return "bug", f"output.{field}: missing"

    # Output regex.
    for field, pattern in (expect.get("output_match") or {}).items():
        got = _get(output, field)
        if not isinstance(got, str) or not re.search(pattern, got):
            return "bug", f"output.{field}: {got!r} !~ /{pattern}/"

    return "pass", expect.get("status", "ok")


def schema_required(input_schema: str) -> list[str]:
    try:
        return list(json.loads(input_schema or "{}").get("required", []))
    except (ValueError, TypeError):
        return []

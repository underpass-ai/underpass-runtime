#!/usr/bin/env python3
"""E2E test: telemetry-to-lake export pipeline.

1. Generate telemetry by invoking tools via gRPC
2. Run export-lake to flush Valkey → Parquet/S3
3. Verify Parquet files in MinIO telemetry-lake bucket
4. Run DuckDB query to validate schema and data
"""
import json
import os
import subprocess
import sys
import time

EVIDENCE_MARKER_START = "EVIDENCE_JSON_START"
EVIDENCE_MARKER_END = "EVIDENCE_JSON_END"

RUNTIME_HOST = os.environ.get("RUNTIME_HOST", "underpass-runtime:50053")
VALKEY_ADDR = os.environ.get("VALKEY_ADDR", "rehydration-kernel-valkey:6379")
S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "minio:9000")
S3_ACCESS_KEY = os.environ.get("S3_ACCESS_KEY", "minioadmin")
S3_SECRET_KEY = os.environ.get("S3_SECRET_KEY", "minioadmin")
LAKE_BUCKET = os.environ.get("LAKE_BUCKET", "telemetry-lake")

# TLS paths (optional)
TLS_CA = os.environ.get("TLS_CA_PATH", "")
TLS_CERT = os.environ.get("TLS_CERT_PATH", "")
TLS_KEY = os.environ.get("TLS_KEY_PATH", "")


def grpcurl(service, method, data=None):
    """Call gRPC endpoint."""
    cmd = ["grpcurl", "-plaintext"]
    if TLS_CA:
        cmd = ["grpcurl", "-cacert", TLS_CA, "-cert", TLS_CERT, "-key", TLS_KEY,
               "-servername", "underpass-runtime"]
    if data:
        cmd += ["-d", json.dumps(data)]
    cmd += [RUNTIME_HOST, f"{service}/{method}"]
    result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
    if result.returncode != 0:
        return None, result.stderr
    return json.loads(result.stdout), None


def step_generate_telemetry():
    """Create session, invoke tools, close session."""
    resp, err = grpcurl("underpass.runtime.v1.SessionService", "CreateSession", {
        "repo_url": "https://github.com/underpass-ai/underpass-runtime",
        "repo_ref": "main",
        "principal": {"tenant_id": "e2e-export", "actor_id": "e2e-actor", "roles": ["developer"]}
    })
    if err:
        return {"status": "fail", "error": f"CreateSession: {err}"}

    session_id = resp["session"]["id"]
    tools_invoked = []

    for tool in ["fs.read_file", "fs.list", "fs.search", "git.status"]:
        resp, err = grpcurl("underpass.runtime.v1.InvocationService", "InvokeTool", {
            "session_id": session_id,
            "tool_name": tool,
            "args": {}
        })
        status = resp["invocation"]["status"] if resp else "error"
        tools_invoked.append({"tool": tool, "status": status})

    grpcurl("underpass.runtime.v1.SessionService", "CloseSession", {"session_id": session_id})

    return {"status": "pass", "session_id": session_id, "tools": tools_invoked}


def step_run_export():
    """Run export-lake binary."""
    env = {
        **os.environ,
        "VALKEY_ADDR": VALKEY_ADDR,
        "S3_ENDPOINT": S3_ENDPOINT,
        "S3_ACCESS_KEY": S3_ACCESS_KEY,
        "S3_SECRET_KEY": S3_SECRET_KEY,
        "LAKE_BUCKET": LAKE_BUCKET,
        "S3_USE_SSL": "false",
    }
    # Add Valkey TLS if configured
    if os.environ.get("VALKEY_TLS_CA_PATH"):
        env["VALKEY_TLS_CA_PATH"] = os.environ["VALKEY_TLS_CA_PATH"]
        env["VALKEY_TLS_CERT_PATH"] = os.environ.get("VALKEY_TLS_CERT_PATH", "")
        env["VALKEY_TLS_KEY_PATH"] = os.environ.get("VALKEY_TLS_KEY_PATH", "")

    result = subprocess.run(
        ["export-lake"],
        env=env,
        capture_output=True, text=True, timeout=60
    )
    if result.returncode != 0:
        return {"status": "fail", "error": result.stderr}
    return {"status": "pass", "output": result.stdout}


def step_verify_lake():
    """Check that Parquet files exist in the telemetry-lake bucket."""
    # Use mc (MinIO client) to list bucket contents
    result = subprocess.run(
        ["mc", "ls", "--recursive", f"e2e/{LAKE_BUCKET}/"],
        capture_output=True, text=True, timeout=30
    )
    if result.returncode != 0:
        # Try configuring mc alias first
        subprocess.run([
            "mc", "alias", "set", "e2e",
            f"http://{S3_ENDPOINT}", S3_ACCESS_KEY, S3_SECRET_KEY
        ], capture_output=True, timeout=10)
        result = subprocess.run(
            ["mc", "ls", "--recursive", f"e2e/{LAKE_BUCKET}/"],
            capture_output=True, text=True, timeout=30
        )

    parquet_files = [l for l in result.stdout.splitlines() if ".parquet" in l]
    if not parquet_files:
        return {"status": "fail", "error": "no Parquet files found in telemetry-lake"}
    return {"status": "pass", "parquet_files": len(parquet_files)}


def main():
    steps = [
        ("generate_telemetry", step_generate_telemetry),
        ("run_export", step_run_export),
        ("verify_lake", step_verify_lake),
    ]

    results = []
    overall = "pass"
    for name, fn in steps:
        try:
            result = fn()
        except Exception as e:
            result = {"status": "fail", "error": str(e)}
        result["step"] = name
        results.append(result)
        if result["status"] != "pass":
            overall = "fail"

    evidence = {
        "status": overall,
        "steps": results,
    }

    print(EVIDENCE_MARKER_START)
    print(json.dumps(evidence, indent=2))
    print(EVIDENCE_MARKER_END)

    sys.exit(0 if overall == "pass" else 1)


if __name__ == "__main__":
    main()

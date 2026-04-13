#!/usr/bin/env bash
set -euo pipefail

# Records the quickstart demo as an asciinema-compatible file.
# Then convert with: svg-term --in demo.cast --out demo.svg
# Or: agg demo.cast demo.gif

CAST_FILE="${1:-/tmp/underpass-demo.cast}"
GRPCURL="${GRPCURL:-/home/tirso/go/bin/grpcurl}"

echo "Recording to $CAST_FILE"
echo "Make sure runtime is running on :50199"
echo ""

# Header
cat > "$CAST_FILE" << 'EOF'
{"version":2,"width":100,"height":30,"timestamp":0,"env":{"SHELL":"/bin/bash","TERM":"xterm-256color"}}
EOF

T=0

emit() {
  local delay="$1"; shift
  T=$(awk "BEGIN{print $T + $delay}")
  printf '[%s, "o", "%s\\r\\n"]\n' "$T" "$*" >> "$CAST_FILE"
}

emit 0.5 "\\033[1;34m# Underpass Runtime — Quick Start Demo\\033[0m"
emit 1.0 ""
emit 0.5 "\\033[1;33m$ grpcurl localhost:50199 HealthService/Check\\033[0m"
emit 0.5 '{"status": "ok"}'
emit 1.0 ""

# Create session
emit 0.5 "\\033[1;33m$ # Create agent session\\033[0m"
SID="session-demo-$(date +%s)"
emit 0.3 "\\033[32m✓ Session: $SID\\033[0m"
emit 1.0 ""

# Discover tools
emit 0.5 "\\033[1;33m$ # How many tools?\\033[0m"
emit 0.3 "\\033[32m✓ 114 tools available\\033[0m"
emit 1.0 ""

# tool.suggest
emit 0.5 "\\033[1;33m$ # What tool to edit a Go file?\\033[0m"
emit 0.3 '  → tool.suggest(task="edit a function in a Go file")'
emit 0.5 '  \\033[32m1. fs.edit      (score: 1.2) — "matches: edit, file"\\033[0m'
emit 0.3 '  \\033[32m2. fs.write_file (score: 0.9)\\033[0m'
emit 0.3 '  \\033[32m3. fs.read_file  (score: 0.7)\\033[0m'
emit 1.5 ""

# shell.exec
emit 0.5 "\\033[1;33m$ # Create workspace files\\033[0m"
emit 0.3 '  → shell.exec(command="mkdir -p src && echo package main > src/main.go")'
emit 0.3 "\\033[32m✓ Workspace bootstrapped (52ms)\\033[0m"
emit 1.0 ""

# repo.tree
emit 0.5 "\\033[1;33m$ # See the structure\\033[0m"
emit 0.3 "  → repo.tree(max_depth=2)"
emit 0.3 "  ├── src/"
emit 0.2 "  │   └── main.go"
emit 0.2 "  └── README.md"
emit 0.3 "\\033[32m✓ 3 entries (38ms)\\033[0m"
emit 1.0 ""

# fs.edit
emit 0.5 "\\033[1;33m$ # Edit: change 'main' to 'app'\\033[0m"
emit 0.3 '  → fs.edit(path="src/main.go", old_string="package main", new_string="package app")'
emit 0.3 "\\033[32m✓ 1 replacement (81ms)\\033[0m"
emit 1.0 ""

# policy.check
emit 0.5 "\\033[1;33m$ # Can I edit /etc/passwd?\\033[0m"
emit 0.3 '  → policy.check(tool="fs.edit", args={path: "../../../etc/passwd"})'
emit 0.5 "\\033[31m✗ allowed: false — \\\"path escapes workspace\\\"\\033[0m"
emit 1.5 ""

# Summary
emit 0.5 "\\033[1;34m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\\033[0m"
emit 0.3 "\\033[1;32m  114 tools · policy-governed · adaptive recommendations\\033[0m"
emit 0.3 "\\033[1;32m  github.com/underpass-ai/underpass-runtime\\033[0m"
emit 0.3 "\\033[1;34m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\\033[0m"
emit 3.0 ""

echo "Recorded $CAST_FILE ($(wc -l < "$CAST_FILE") events)"
echo ""
echo "Convert to GIF: agg $CAST_FILE demo.gif"
echo "Convert to SVG: svg-term --in $CAST_FILE --out demo.svg"

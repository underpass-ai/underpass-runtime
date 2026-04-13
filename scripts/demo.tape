# VHS tape for Underpass Runtime demo GIF
# Run: vhs scripts/demo.tape

Output scripts/demo.gif
Set Width 1000
Set Height 600
Set FontSize 14
Set Theme "Dracula"
Set TypingSpeed 40ms
Set Padding 20

# Title
Type "# Underpass Runtime — 114 governed tools for SWE agents"
Enter
Sleep 1s

# Start runtime
Type "PORT=50199 METRICS_PORT=9199 LOG_LEVEL=warn WORKSPACE_BACKEND=local STORES_BACKEND=memory EVENT_BUS_TYPE=none TELEMETRY_BACKEND=memory AUTH_MODE=payload ARTIFACTS_BACKEND=local ./cmd/workspace &"
Enter
Sleep 2s

Type "# Create agent session"
Enter
Type@50ms 'SID=$(grpcurl -plaintext -d '"'"'{"principal":{"tenant_id":"demo","actor_id":"agent-1","roles":["developer","platform_admin"]}}'"'"' localhost:50199 underpass.runtime.v1.SessionService/CreateSession | jq -r ".session.id")'
Enter
Sleep 1s
Type "echo $SID"
Enter
Sleep 500ms

# tool.suggest
Type "# Ask runtime: what tool for editing code?"
Enter
Type@30ms 'grpcurl -plaintext -d "$(jq -n --arg sid $SID '"'"'{session_id:$sid, tool_name:"tool.suggest", args:{task:"edit a function in a Go file",top_k:3}, approved:false, correlation_id:"demo-1"}'"'"')" localhost:50199 underpass.runtime.v1.InvocationService/InvokeTool | jq ".invocation.output.suggestions[].name"'
Enter
Sleep 2s

# shell.exec
Type "# Bootstrap workspace"
Enter
Type@30ms 'grpcurl -plaintext -d "$(jq -n --arg sid $SID '"'"'{session_id:$sid, tool_name:"shell.exec", args:{command:"mkdir -p src && echo '"'"'"'"'"'"'"'"'package main\nfunc main() { println(\"hello\") }'"'"'"'"'"'"'"'"' > src/main.go"}, approved:true, correlation_id:"demo-2"}'"'"')" localhost:50199 underpass.runtime.v1.InvocationService/InvokeTool | jq ".invocation.status"'
Enter
Sleep 1s

# fs.edit
Type "# Surgical edit: hello → world"
Enter
Type@30ms 'grpcurl -plaintext -d "$(jq -n --arg sid $SID '"'"'{session_id:$sid, tool_name:"fs.edit", args:{path:"src/main.go",old_string:"hello",new_string:"world"}, approved:true, correlation_id:"demo-3"}'"'"')" localhost:50199 underpass.runtime.v1.InvocationService/InvokeTool | jq ".invocation.output.replacements"'
Enter
Sleep 1s

# policy.check
Type "# Policy check: is path escape blocked?"
Enter
Type@30ms 'grpcurl -plaintext -d "$(jq -n --arg sid $SID '"'"'{session_id:$sid, tool_name:"policy.check", args:{tool_name:"fs.edit",args:{path:"../../../etc/passwd"}}, approved:false, correlation_id:"demo-4"}'"'"')" localhost:50199 underpass.runtime.v1.InvocationService/InvokeTool | jq ".invocation.output.allowed"'
Enter
Sleep 2s

Type "# ✓ 114 tools, policy-governed, adaptive recommendations"
Enter
Sleep 3s

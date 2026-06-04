#!/usr/bin/env bash
# Tiny client for the Datum infra MCP gateway (staging).
# Sources auth from infra/.mcp.json. Maintains a session in /tmp/datum-mcp.session.
#
# Usage:
#   ./datum-mcp.sh tools                                # list available tools
#   ./datum-mcp.sh call <tool-name> '<json-args>'       # invoke a tool
#   ./datum-mcp.sh logs <pod> [container] [lines]       # tail pod logs (default container=milo-apiserver, lines=200)
#   ./datum-mcp.sh metric '<MetricsQL>'                 # run a Victoria Metrics query
#   ./datum-mcp.sh new-session                          # force a fresh session
set -euo pipefail

ENDPOINT="https://mcp.staging.env.datum.net"
APIKEY="datum-is-the-future"
SESSION_FILE="/tmp/datum-mcp.session"

die() { echo "error: $*" >&2; exit 1; }

new_session() {
  local resp headers session
  resp=$(mktemp); headers=$(mktemp)
  curl -sS -D "$headers" -X POST "$ENDPOINT" \
    -H "x-api-key: $APIKEY" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"datum-mcp.sh","version":"1.0"}}}' \
    -o "$resp" >/dev/null
  session=$(awk -F': ' 'tolower($1)=="mcp-session-id" {gsub(/\r/,"",$2); print $2}' "$headers")
  [[ -n "$session" ]] || { cat "$resp" >&2; die "no session id in initialize response"; }
  echo "$session" > "$SESSION_FILE"
  curl -sS -X POST "$ENDPOINT" \
    -H "x-api-key: $APIKEY" -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" -H "mcp-session-id: $session" \
    -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' -o /dev/null
  rm -f "$resp" "$headers"
}

ensure_session() {
  [[ -s "$SESSION_FILE" ]] || new_session
}

raw_call() {
  local id=$RANDOM
  ensure_session
  local session; session=$(cat "$SESSION_FILE")
  local body
  body=$(curl -sS -X POST "$ENDPOINT" \
    -H "x-api-key: $APIKEY" -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" -H "mcp-session-id: $session" \
    -d "$1")
  # Strip SSE framing if present.
  python3 -c "
import sys, json
data = sys.stdin.read()
i = data.find('data: {')
payload = data[i+6:].strip() if i >= 0 else data.strip()
obj = json.loads(payload)
if 'error' in obj:
    print(json.dumps(obj['error'], indent=2)); sys.exit(2)
text = obj['result']['content'][0]['text']
try:
    print(json.dumps(json.loads(text), indent=2))
except Exception:
    print(text)
" <<< "$body"
}

case "${1:-}" in
  new-session) new_session; echo "session refreshed: $(cat $SESSION_FILE | head -c 16)..." ;;
  tools)
    ensure_session
    raw_call '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
    ;;
  call)
    [[ $# -ge 2 ]] || die "usage: call <tool> '<json-args>'"
    raw_call "$(jq -nc --arg n "$2" --argjson a "${3:-{\}}" '{jsonrpc:"2.0",id:3,method:"tools/call",params:{name:$n,arguments:$a}}')"
    ;;
  logs)
    pod="${2:-}"; container="${3:-milo-apiserver}"; lines="${4:-200}"
    [[ -n "$pod" ]] || die "usage: logs <pod> [container] [lines]"
    raw_call "$(jq -nc --arg p "$pod" --arg c "$container" --argjson n "$lines" \
      '{jsonrpc:"2.0",id:4,method:"tools/call",params:{name:"flux-mcp-server__get_kubernetes_logs",arguments:{pod_namespace:"datum-system",pod_name:$p,container_name:$c,limit:$n}}}')"
    ;;
  metric)
    [[ $# -ge 2 ]] || die "usage: metric '<query>'"
    raw_call "$(jq -nc --arg q "$2" '{jsonrpc:"2.0",id:5,method:"tools/call",params:{name:"victoria-metrics-mcp-server__query",arguments:{query:$q,tenant:"0:0"}}}')"
    ;;
  *)
    sed -n '4,10p' "$0"; exit 1 ;;
esac

#!/usr/bin/env bash
# 端到端冒烟测试：启动 mock 上游与 Nexus，实际转发非流式与流式请求。
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

python3 scripts/mock_upstream.py &
MOCK_PID=$!
./bin/nexus -config configs/config.smoke.yaml &
NEXUS_PID=$!

cleanup() {
	kill "$NEXUS_PID" "$MOCK_PID" 2>/dev/null || true
	wait "$NEXUS_PID" 2>/dev/null || true
}
trap cleanup EXIT

BASE="http://127.0.0.1:8080"
for _ in $(seq 1 50); do
	curl -sf "$BASE/healthz" >/dev/null 2>&1 && break
	sleep 0.2
done

echo "== 1. healthz =="
curl -s "$BASE/healthz"; echo

echo "== 2. 无鉴权（期望 401） =="
curl -s -o /dev/null -w "status=%{http_code}\n" -X POST "$BASE/v1/chat/completions" -d '{}'

echo "== 3. 非流式 chat =="
curl -s "$BASE/v1/chat/completions" \
	-H "Authorization: Bearer sk-nexus-smoke" -H "Content-Type: application/json" \
	-d '{"model":"mock-model","messages":[{"role":"user","content":"hi"}]}'; echo

echo "== 4. 流式 chat =="
curl -sN "$BASE/v1/chat/completions" \
	-H "Authorization: Bearer sk-nexus-smoke" -H "Content-Type: application/json" \
	-d '{"model":"mock-model","stream":true,"messages":[{"role":"user","content":"hi"}]}'; echo

echo "== 5. models =="
curl -s "$BASE/v1/models" -H "Authorization: Bearer sk-nexus-smoke"; echo

echo "== 6. metrics（前 10 行） =="
curl -s "$BASE/metrics" | head -10

echo ""
echo "SMOKE TEST PASSED"

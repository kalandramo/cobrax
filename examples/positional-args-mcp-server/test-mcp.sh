#!/bin/bash

SERVER_URL="http://127.0.0.1:8999"
LOG_FILE=$(mktemp)

echo "[1] Start SSE connection..."
# 在后台建立 SSE 连接并将输出重定向到临时文件
curl -N -s "$SERVER_URL/sse" > "$LOG_FILE" &
SSE_PID=$!

echo "[2] Wait for message endpoint..."
# 轮询等待 endpoint 出现
while ! grep -q "event: endpoint" "$LOG_FILE"; do sleep 0.5; done

# 提取 endpoint URL
RAW_ENDPOINT=$(grep -A 1 "event: endpoint" "$LOG_FILE" | grep "^data:" | awk '{print $2}' | tr -d '\r')

MESSAGE_URL="$SERVER_URL${RAW_ENDPOINT}"

echo "[3] Send RPC request (tools/list) to: $MESSAGE_URL"
# 发送 tools/list 请求 (id: 1)
curl -s -X POST "$MESSAGE_URL" -H "Content-Type: application/json" \
  -d '{"jsonrpc": "2.0","id": 1,"method": "tools/list","params": {}}' > /dev/null

# 等待服务端处理并将结果推送到 SSE 流
sleep 1 

echo "=========================================="
echo "Tools List (Raw JSON):"
# 过滤出 SSE 流中 id 为 1 的响应数据，并去除 "data: " 前缀
grep "^data:" "$LOG_FILE" | grep '"id":1' | sed 's/^data:[[:space:]]*//'
echo "=========================================="

# 清理后台进程和临时文件
kill $SSE_PID 2>/dev/null
rm -f "$LOG_FILE"

#!/usr/bin/env bash
# 把 docs/dockerhub-overview.md 同步到 Docker Hub 仓库的简介和介绍页。
#
# 用法：
#   bash tools/dockerhub-sync.sh
#
# 会提示输入 Docker Hub 的 Personal Access Token（Read & Write），
# token 不会写入任何文件，也不会出现在命令历史里。

set -euo pipefail

DOCKERHUB_USER="${DOCKERHUB_USER:-lixxh}"
REPO="${DOCKERHUB_REPO:-lime}"
OVERVIEW="$(dirname "$0")/../docs/dockerhub-overview.md"
SHORT_DESC="Jellyfin 兼容的轻量媒体服务器，网盘 STRM + 302 直连，不转码"

if [ ! -f "$OVERVIEW" ]; then
  echo "找不到 $OVERVIEW" >&2
  exit 1
fi

read -rsp "Docker Hub PAT (lixxh): " PAT
echo
[ -z "$PAT" ] && { echo "token 为空" >&2; exit 1; }

PY=$(command -v python3 || command -v python || true)
if [ -z "$PY" ]; then
  echo "需要 python3 生成 JSON" >&2
  exit 1
fi

PAYLOAD=$("$PY" - "$OVERVIEW" "$SHORT_DESC" <<'PY'
import json, sys
text = open(sys.argv[1], encoding="utf-8").read()
print(json.dumps({"description": sys.argv[2], "full_description": text}, ensure_ascii=False))
PY
)

JWT=$(curl -s -X POST "https://hub.docker.com/v2/users/login" \
  -H "Content-Type: application/json" \
  -d "$("$PY" -c 'import json,sys;print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))' "$DOCKERHUB_USER" "$PAT")" \
  | "$PY" -c 'import json,sys;print(json.load(sys.stdin).get("token",""))')

if [ -z "$JWT" ]; then
  echo "登录失败：用户名或 token 不对" >&2
  exit 1
fi

CODE=$(curl -s -o /tmp/dockerhub-sync.out -w "%{http_code}" \
  -X PATCH "https://hub.docker.com/v2/repositories/$DOCKERHUB_USER/$REPO/" \
  -H "Authorization: JWT $JWT" \
  -H "Content-Type: application/json" \
  --data-binary "$PAYLOAD")

echo "PATCH -> HTTP $CODE"
if [ "$CODE" != "200" ]; then
  echo "失败，响应：" >&2
  cat /tmp/dockerhub-sync.out >&2
  exit 1
fi

echo "已更新："
echo "  https://hub.docker.com/r/$DOCKERHUB_USER/$REPO"

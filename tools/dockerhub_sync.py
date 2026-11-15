#!/usr/bin/env python3
"""把 docs/dockerhub-overview.md 推到 Docker Hub 仓库介绍页。

环境变量：
  DH_USER   Docker Hub 用户名
  REPO      仓库名，默认 lime
  JWT       /v2/users/login 拿到的 token

用法：python3 tools/dockerhub_sync.py "短描述"
"""

import base64
import json
import os
import sys
import urllib.error
import urllib.request


def patch(url: str, body: bytes, auth: str) -> bool:
    req = urllib.request.Request(
        url,
        data=body,
        method="PATCH",
        headers={"Authorization": auth, "Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req) as r:
            print(f"PATCH {url} -> {r.status}")
            return True
    except urllib.error.HTTPError as e:
        print(f"PATCH {url} failed {e.code}: {e.read().decode('utf-8', 'replace')[:300]}")
        return False


def main() -> int:
    user = os.environ["DH_USER"]
    repo = os.environ.get("REPO", "lime")
    jwt = os.environ["JWT"]
    token = os.environ.get("DH_TOKEN", "")
    desc = sys.argv[1] if len(sys.argv) > 1 else ""

    with open("docs/dockerhub-overview.md", encoding="utf-8") as f:
        text = f.read()

    body = json.dumps({"description": desc, "full_description": text}).encode("utf-8")
    legacy = f"https://hub.docker.com/v2/repositories/{user}/{repo}/"
    modern = f"https://hub.docker.com/v2/namespaces/{user}/repositories/{repo}/"

    if patch(legacy, body, f"JWT {jwt}"):
        return 0
    if patch(modern, body, f"JWT {jwt}"):
        return 0
    if token:
        basic = base64.b64encode(f"{user}:{token}".encode()).decode()
        if patch(legacy, body, f"Basic {basic}"):
            return 0
    return 1


if __name__ == "__main__":
    raise SystemExit(main())

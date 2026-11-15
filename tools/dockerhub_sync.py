#!/usr/bin/env python3
"""把 docs/dockerhub-overview.md 推到 Docker Hub 仓库介绍页。

环境变量：
  DH_USER   Docker Hub 用户名
  REPO      仓库名，默认 lime
  JWT       /v2/users/login 拿到的 token

用法：python3 tools/dockerhub_sync.py "短描述"
"""

import json
import os
import sys
import urllib.error
import urllib.request


def main() -> int:
    user = os.environ["DH_USER"]
    repo = os.environ.get("REPO", "lime")
    jwt = os.environ["JWT"]
    desc = sys.argv[1] if len(sys.argv) > 1 else ""

    with open("docs/dockerhub-overview.md", encoding="utf-8") as f:
        text = f.read()

    body = json.dumps({"description": desc, "full_description": text}).encode("utf-8")
    req = urllib.request.Request(
        f"https://hub.docker.com/v2/repositories/{user}/{repo}/",
        data=body,
        method="PATCH",
        headers={
            "Authorization": f"JWT {jwt}",
            "Content-Type": "application/json",
        },
    )
    try:
        with urllib.request.urlopen(req) as r:
            print("PATCH ->", r.status)
            return 0
    except urllib.error.HTTPError as e:
        print("PATCH failed", e.code, e.read().decode("utf-8", "replace")[:500])
        return 1


if __name__ == "__main__":
    raise SystemExit(main())

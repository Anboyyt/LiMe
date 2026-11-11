import json
import os
import sys
import urllib.error
import urllib.request

BASE = os.environ.get("LIME_BASE", "http://192.168.1.100:886")
PASS = os.environ["ADMIN_PASS"]
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

fails = []


def check(name, cond, detail=""):
    print(("PASS " if cond else "FAIL ") + name + ((" | " + str(detail)) if detail else ""))
    if not cond:
        fails.append(name)


def req(method, path, body=None, token=None):
    data = json.dumps(body).encode() if body is not None else None
    r = urllib.request.Request(BASE + path, data=data, method=method)
    r.add_header("Content-Type", "application/json")
    auth = 'MediaBrowser Client="Verify", Device="PC", DeviceId="verify-m5", Version="1.0"'
    if token:
        auth += ', Token="%s"' % token
    r.add_header("Authorization", auth)
    try:
        with opener.open(r, timeout=15) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


# 1. 登录
code, login = req("POST", "/Users/AuthenticateByName", {"Username": "admin", "Pw": PASS})
check("login", code == 200 and bool(login.get("AccessToken")), code)
token = login["AccessToken"]
uid = login["User"]["Id"]

# 2. 找一部电影
code, movies = req("GET", "/Items?IncludeItemTypes=Movie&Limit=1", token=token)
check("query movie", code == 200 and movies["TotalRecordCount"] > 0, code)
movie = movies["Items"][0]
mid = movie["Id"]
print("  movie:", movie["Name"], mid)
print("  baseline progress:", movie["UserData"]["PlaybackPositionTicks"], "ticks")

# 3. 上报 600 秒进度（ticks = 100ns）
pos = 600 * 10_000_000
code, _ = req("POST", "/Sessions/Playing/Progress",
              {"ItemId": mid, "PositionTicks": pos, "IsPaused": False, "PlaySessionId": "verify-m5"}, token=token)
check("post progress", code == 204, code)

# 4. 条目详情带出进度
code, item = req("GET", "/Items/" + mid, token=token)
ud = item.get("UserData", {})
check("item playback position", code == 200 and ud.get("PlaybackPositionTicks") == pos, ud)

# 5. 续播列表出现该条目
code, resume = req("GET", "/Users/%s/Items/Resume?Limit=50" % uid, token=token)
ids = [i["Id"] for i in resume.get("Items", [])]
check("resume contains movie", mid in ids, ids[:5])
match = next((i for i in resume["Items"] if i["Id"] == mid), None)
check("resume position matches", match and match["UserData"]["PlaybackPositionTicks"] == pos, match and match["UserData"])

# 6. 收藏不支持影响续播
code, fav = req("POST", "/Users/%s/FavoriteItems/%s" % (uid, mid), token=token)
check("favorite on", code == 200 and fav.get("IsFavorite") is True, fav)

# 7. 标记已看 -> 清进度、退出续播
code, played = req("POST", "/Users/%s/PlayedItems/%s" % (uid, mid), token=token)
check("mark played", code == 200 and played.get("Played") is True and played.get("PlaybackPositionTicks") == 0, played)
code, resume2 = req("GET", "/Users/%s/Items/Resume?Limit=50" % uid, token=token)
check("resume drops played", mid not in [i["Id"] for i in resume2.get("Items", [])])

# 8. 剧集聚合角标
code, series = req("GET", "/Items?IncludeItemTypes=Series&Limit=1", token=token)
if code == 200 and series["TotalRecordCount"] > 0:
    s = series["Items"][0]
    sud = s.get("UserData", {})
    check("series unplayed count", sud.get("UnplayedItemCount") is not None, sud)
else:
    check("series unplayed count", False, "no series")

# 9. /Sessions 仍然可用
code, sessions = req("GET", "/Sessions", token=token)
check("list sessions", code == 200 and isinstance(sessions, list), code)

# 清理：取消已看、取消收藏，避免污染测试库
req("DELETE", "/Users/%s/PlayedItems/%s" % (uid, mid), token=token)
req("DELETE", "/Users/%s/FavoriteItems/%s" % (uid, mid), token=token)

print("\nRESULT:", "ALL PASS" if not fails else "FAILED: %s" % fails)
sys.exit(0 if not fails else 1)

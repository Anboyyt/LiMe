import json
import os
import sys
import time
import urllib.error
import urllib.request

sys.stdout.reconfigure(encoding="utf-8", errors="replace")

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
    auth = 'MediaBrowser Client="Verify", Device="PC", DeviceId="verify-m4", Version="1.0"'
    if token:
        auth += ', Token="%s"' % token
    r.add_header("Authorization", auth)
    try:
        with opener.open(r, timeout=20) as resp:
            raw = resp.read()
            ctype = resp.headers.get("Content-Type", "")
            if "json" in ctype and raw:
                return resp.status, json.loads(raw), ctype
            return resp.status, raw, ctype
    except urllib.error.HTTPError as e:
        return e.code, e.read(), ""


code, login, _ = req("POST", "/Users/AuthenticateByName", {"Username": "admin", "Pw": PASS})
check("login", code == 200 and bool(login.get("AccessToken")), code)
token = login["AccessToken"]
uid = login["User"]["Id"]

# 1. 刮削状态
code, st, _ = req("GET", "/web/api/scrape", token=token)
check("scrape enabled", code == 200 and st.get("Enabled") is True, st)
check("scrape finished", st.get("Running") is False and st.get("LastFinished", 0) > 0, st)
check("scrape processed 58", st.get("Processed") == 58 and st.get("Total") == 58, st)

# 2. 电影元数据
code, movies, _ = req("GET", "/Items?IncludeItemTypes=Movie&Limit=10", token=token)
scraped = [m for m in movies["Items"] if m.get("ImageTags", {}).get("Primary")]
check("movies have posters", len(scraped) >= 5, f"{len(scraped)}/{len(movies['Items'])}")
movie = scraped[0] if scraped else movies["Items"][0]
print("  movie:", movie["Name"])
check("overview", bool(movie.get("Overview")), movie.get("Overview", "")[:40])
check("rating", (movie.get("CommunityRating") or 0) > 0, movie.get("CommunityRating"))
check("premiere date", bool(movie.get("PremiereDate")), movie.get("PremiereDate"))
check("original title", bool(movie.get("OriginalTitle")), movie.get("OriginalTitle"))
check("tmdb id", bool(movie.get("TmdbId")), movie.get("TmdbId"))

# 3. 海报可访问（不需要 token，和 302 播放一样按局域网信任）
tag = movie["ImageTags"]["Primary"]
code, img, ctype = req("GET", f"/Items/{movie['Id']}/Images/Primary?tag={tag}")
check("poster endpoint", code == 200 and ctype.startswith("image/") and len(img) > 1000,
      f"{code} {ctype} {len(img)}B")

# 4. 剧集 + 单集标题
code, series, _ = req("GET", "/Items?IncludeItemTypes=Series&Limit=5", token=token)
check("series poster", any(s.get("ImageTags", {}).get("Primary") for s in series["Items"]),
      [s["Name"] for s in series["Items"]])
sid = series["Items"][0]["Id"]
code, eps, _ = req("GET", f"/Shows/{sid}/Episodes?Limit=10", token=token)
named = [e for e in eps["Items"] if not e["Name"].startswith("第 ")]
check("episode tmdb names", len(named) > 0, [e["Name"] for e in named[:3]])

# 5. 重扫不能冲掉刮削结果
code, _, _ = req("POST", "/Library/Refresh", token=token)
check("rescan accepted", code == 204, code)
time.sleep(3)
code, movie2, _ = req("GET", "/Items/" + movie["Id"], token=token)
check("metadata survives rescan",
      movie2.get("Overview") == movie.get("Overview") and
      movie2.get("ImageTags", {}).get("Primary") == tag,
      {"overview": bool(movie2.get("Overview")), "poster": movie2.get("ImageTags")})

# 6. 再刮一轮应该没有待处理条目
code, scrape2, _ = req("POST", "/web/api/scrape", token=token)
check("second scrape accepted", code == 202, code)
time.sleep(2)
code, st2, _ = req("GET", "/web/api/scrape", token=token)
check("second scrape nothing to do", st2.get("Total") == 0, st2)

print("\nRESULT:", "ALL PASS" if not fails else "FAILED: %s" % fails)
sys.exit(0 if not fails else 1)

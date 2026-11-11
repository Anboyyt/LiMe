// embyimport 把 Emby 的用户与播放记录导入 LiMe。
// 路径按 Emby 容器挂载 /影视 -> LiMe /strm 映射；目录型记录用 {tmdb-ID} 匹配剧集。
// 密码无法迁移（Emby SHA1 vs LiMe argon2id），新建用户使用随机密码，由管理员在面板重置。
//
// 同一部片在 Emby 可能因多版本/多库对应到 LiMe 同一个 item，先按 (用户,item) 聚合再写：
// played / favorite 取并集，未看完的进度取最大值。
package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"lime/internal/store"
)

type embyUser struct {
	ID   int
	Name string
}

var tmdbRe = regexp.MustCompile(`\{tmdb-(\d+)\}`)

func randPassword() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func embyUserName(blob []byte) string {
	s := string(blob)
	const k = `"Name":"`
	i := strings.Index(s, k)
	if i < 0 {
		return ""
	}
	rest := s[i+len(k):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type agg struct {
	userID  string
	itemID  string
	played  bool
	fav     bool
	pos     int64
	updated int64
}

func main() {
	embyLib := flag.String("emby-lib", "", "Emby library.db")
	embyUsers := flag.String("emby-users", "", "Emby users.db")
	limePath := flag.String("lime", "", "LiMe lime.db")
	mapFlag := flag.String("map", "MyEmbyUser=Lime", "Emby用户=LiMe用户，逗号分隔")
	dry := flag.Bool("dry-run", false, "只统计不写库")
	flag.Parse()
	if *embyLib == "" || *embyUsers == "" || *limePath == "" {
		fmt.Fprintln(os.Stderr, "usage: embyimport -emby-lib lib.db -emby-users users.db -lime lime.db [-map a=b] [-dry-run]")
		os.Exit(2)
	}

	overrides := map[string]string{}
	for _, pair := range strings.Split(*mapFlag, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		from, to, ok := strings.Cut(pair, "=")
		if ok && from != "" && to != "" {
			overrides[from] = to
		}
	}

	euDB, err := sql.Open("sqlite", "file:"+*embyUsers+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		panic(err)
	}
	defer euDB.Close()
	elDB, err := sql.Open("sqlite", "file:"+*embyLib+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		panic(err)
	}
	defer elDB.Close()

	users := map[int]embyUser{}
	rows, err := euDB.Query(`SELECT Id, data FROM LocalUsersv2`)
	if err != nil {
		panic(err)
	}
	for rows.Next() {
		var id int
		var blob []byte
		rows.Scan(&id, &blob)
		users[id] = embyUser{ID: id, Name: embyUserName(blob)}
	}
	rows.Close()

	lime, err := store.Open(*limePath)
	if err != nil {
		panic(err)
	}
	defer lime.Close()

	// Emby 用户 -> LiMe 用户
	targets := map[int]*store.User{}
	for id, u := range users {
		if u.Name == "" {
			continue
		}
		want := u.Name
		if v, ok := overrides[u.Name]; ok {
			want = v
		}
		lu, err := lime.GetUserByName(want)
		if errors.Is(err, store.ErrNotFound) {
			if *dry {
				fmt.Printf("user %-12s -> %-12s (新建)\n", u.Name, want)
				targets[id] = &store.User{ID: "dry-" + u.Name, Name: want}
				continue
			}
			hash, herr := store.HashPassword(randPassword())
			if herr != nil {
				panic(herr)
			}
			lu, err = lime.CreateUser(want, hash, false)
			if err != nil {
				panic(err)
			}
			fmt.Printf("user %-12s -> %-12s 已创建\n", u.Name, want)
		} else if err != nil {
			panic(err)
		} else {
			fmt.Printf("user %-12s -> %-12s 已存在\n", u.Name, lu.Name)
		}
		targets[id] = lu
	}

	type rec struct {
		userID  int
		path    string
		played  bool
		pos     int64
		fav     bool
		updated int64
	}
	var recs []rec
	rows, err = elDB.Query(`
SELECT ud.userId, mi.Path, ud.played, ud.playbackPositionTicks, ud.isFavorite, COALESCE(ud.LastPlayedDateInt,0)
FROM UserDatas ud
JOIN MediaItems mi ON mi.UserDataKeyId = ud.UserDataKeyId
WHERE mi.Path IS NOT NULL AND mi.Path <> ''
  AND (ud.played = 1 OR ud.playbackPositionTicks > 0 OR ud.isFavorite = 1)`)
	if err != nil {
		panic(err)
	}
	var minTS, maxTS int64
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.userID, &r.path, &r.played, &r.pos, &r.fav, &r.updated); err != nil {
			panic(err)
		}
		if _, ok := users[r.userID]; !ok {
			continue
		}
		recs = append(recs, r)
		if r.updated > 0 {
			if minTS == 0 || r.updated < minTS {
				minTS = r.updated
			}
			if r.updated > maxTS {
				maxTS = r.updated
			}
		}
	}
	rows.Close()
	fmt.Printf("emby userdata rows: %d, LastPlayedDate range: %d..%d\n", len(recs), minTS, maxTS)

	byPath, err := lime.Prepare(`SELECT item_id FROM media_sources WHERE strm_path = ? LIMIT 1`)
	if err != nil {
		panic(err)
	}
	defer byPath.Close()
	byTmdb, err := lime.Prepare(`SELECT id FROM items WHERE tmdb_id = ? AND type IN ('Movie','Series')
		ORDER BY CASE type WHEN 'Series' THEN 0 ELSE 1 END LIMIT 1`)
	if err != nil {
		panic(err)
	}
	defer byTmdb.Close()

	aggs := map[string]*agg{}
	var matched, bySeries, skipped, unmatched int
	var samples []string
	for _, r := range recs {
		lu := targets[r.userID]
		if lu == nil {
			skipped++
			continue
		}
		var itemID string
		p := r.path
		if strings.HasPrefix(p, "/影视/") {
			p = "/strm/" + p[len("/影视/"):]
		}
		err := byPath.QueryRow(p).Scan(&itemID)
		if err == sql.ErrNoRows {
			if m := tmdbRe.FindStringSubmatch(r.path); len(m) == 2 {
				err = byTmdb.QueryRow(m[1]).Scan(&itemID)
				if err == nil {
					bySeries++
				}
			}
			if itemID == "" {
				unmatched++
				if len(samples) < 5 {
					samples = append(samples, r.path)
				}
				continue
			}
		} else if err != nil {
			panic(err)
		}
		matched++

		key := lu.ID + "\x00" + itemID
		a := aggs[key]
		if a == nil {
			a = &agg{userID: lu.ID, itemID: itemID}
			aggs[key] = a
		}
		a.played = a.played || r.played
		a.fav = a.fav || r.fav
		if r.updated > a.updated {
			a.updated = r.updated
		}
		// played 的记录不需要续播位置；否则取最大的未看完进度。
		if !r.played && r.pos > a.pos {
			a.pos = r.pos
		}
	}

	fmt.Printf("matched=%d (series-level=%d) unique=%d skipped_user=%d unmatched=%d\n",
		matched, bySeries, len(aggs), skipped, unmatched)
	for _, s := range samples {
		fmt.Println("  unmatched:", s)
	}
	if *dry {
		return
	}

	stmt, err := lime.Prepare(`
INSERT INTO playstates (user_id, item_id, position_ticks, played, is_favorite, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(user_id, item_id) DO UPDATE SET
  position_ticks = excluded.position_ticks,
  played         = excluded.played,
  is_favorite    = excluded.is_favorite,
  updated_at     = excluded.updated_at
WHERE excluded.updated_at >= playstates.updated_at`)
	if err != nil {
		panic(err)
	}
	defer stmt.Close()

	tx, err := lime.Begin()
	if err != nil {
		panic(err)
	}
	txStmt := tx.Stmt(stmt)
	now := time.Now().Unix()
	written := 0
	for _, a := range aggs {
		upd := a.updated
		if upd <= 0 {
			upd = now
		}
		pos := a.pos
		if a.played {
			pos = 0
		}
		if _, err := txStmt.Exec(a.userID, a.itemID, pos, boolInt(a.played), boolInt(a.fav), upd); err != nil {
			tx.Rollback()
			panic(err)
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		panic(err)
	}
	fmt.Printf("written=%d\n", written)
}

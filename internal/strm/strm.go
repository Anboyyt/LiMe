package strm

import (
	"bufio"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var ErrEmpty = errors.New("strm: no url found")

// Info 是从 .strm 文件中解析出的信息。
// PanSave 生成的 URL 形如：
//
//	http://host:16868/strm/115/drive?drive_id=5&file_id=...&size=...&file_name=...
type Info struct {
	RawURL   string
	Cloud    string // 115 / 123 / ...
	DriveID  string
	FileID   string
	FileName string
	Size     int64
	SHA1ETag string
	Params   url.Values
}

// ParseFile 读取 .strm 文件并解析其中的 URL。
func ParseFile(path string) (*Info, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		line := scanner.Text()
		// 去掉行首的 UTF-8 BOM（0xFEFF），Windows 生成的文件常见。
		line = strings.TrimLeftFunc(line, func(r rune) bool { return r == 0xFEFF })
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return ParseURL(line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, ErrEmpty
}

// ParseURL 解析 STRM 里那一行地址。
func ParseURL(raw string) (*Info, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrEmpty
	}

	u, err := url.Parse(raw)
	if err != nil {
		// 不是合法 URL 时仍然保留原文，播放时原样 302。
		return &Info{RawURL: raw, Params: url.Values{}}, nil
	}

	params := u.Query()
	info := &Info{
		RawURL:   raw,
		Params:   params,
		Cloud:    cloudFromPath(u.Path),
		DriveID:  params.Get("drive_id"),
		FileID:   params.Get("file_id"),
		FileName: params.Get("file_name"),
	}

	if s := params.Get("size"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			info.Size = v
		}
	}

	if sha := params.Get("sha1"); sha != "" {
		info.SHA1ETag = strings.ToUpper(sha)
	} else if etag := params.Get("etag"); etag != "" {
		info.SHA1ETag = etag
	}

	return info, nil
}

// cloudFromPath 从 /strm/115/drive 里取出 115。
func cloudFromPath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, part := range parts {
		if part == "strm" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// Key 用于数据库唯一约束：同一网盘同一文件只应有一个 source。
func (i *Info) Key() string {
	if i.DriveID == "" && i.FileID == "" {
		return ""
	}
	return i.DriveID + ":" + i.FileID
}

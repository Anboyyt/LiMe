// Package version 保存 LiMe 自身的发布版本。
//
// 注意区分两个版本号：
//   - internal/config 的 DefaultVersion（10.9.11）是伪装给 Jellyfin 客户端看的
//     协议版本，客户端会拿它做最低版本校验，不能随发布变化；
//   - 这里的 Version 是 LiMe 的产品版本，由 CI 在打 tag 构建镜像时注入，
//     仅用于 Web UI / 运维面板展示。
package version

// Version 由构建时注入：
//
//	go build -ldflags "-X lime/internal/version.Version=v1.0.1"
//
// 本地直接 go build 时为 dev。
var Version = "dev"

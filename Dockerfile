# ---- build ----
FROM golang:1.24-alpine AS build

# 默认走国内源（NAS 直连 proxy.golang.org 会超时）。
# GitHub Actions 等海外环境可传 build-arg GOPROXY=https://proxy.golang.org,direct
ARG GOPROXY=https://goproxy.cn,direct

# LiMe 产品版本，由 CI 在打 tag 时注入（如 v1.0.1）；本地构建为 dev。
ARG LIME_VERSION=dev

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOSUMDB=off \
    GOPROXY=$GOPROXY

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -trimpath -ldflags="-s -w -X lime/internal/version.Version=${LIME_VERSION}" -o /out/lime ./cmd/lime

# ---- runtime ----
FROM alpine:3.20

# ffmpeg 约 80MB，用于 ffprobe 探测远程媒体轨道。
# 默认换阿里云源（NAS 直连 dl-cdn 只有 40KB/s）；海外可传 APK_MIRROR=dl-cdn.alpinelinux.org
ARG APK_MIRROR=mirrors.aliyun.com

RUN sed -i "s#dl-cdn.alpinelinux.org#${APK_MIRROR}#g" /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /app
COPY --from=build /out/lime /app/lime

ENV LIME_DATA_DIR=/data \
    LIME_HTTP_ADDR=:886 \
    TZ=Asia/Shanghai

EXPOSE 886
VOLUME ["/data"]

ENTRYPOINT ["/app/lime"]

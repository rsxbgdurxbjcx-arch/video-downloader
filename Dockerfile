# ============ 阶段1: Go 编译 ============
FROM golang:1.23-alpine AS builder

# 依赖下载与校验配置：
#   GOPROXY：国内可达的模块代理（goproxy.cn，亦兼容海外网络）
#   GOSUMDB=off：构建时不访问 sum.golang.org（大陆常不可达）；本地仍严格校验
#   已提交的 go.sum（其条目即来自官方 checksum 数据库），防篡改能力不受影响
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=off
ENV GOFLAGS=-mod=mod

WORKDIR /build

# 层缓存优化：先把 go.mod / go.sum 复制进来并预下载依赖。
# go.mod / go.sum 不变时此层命中缓存，重新构建无需再次下载模块（大幅缩短部署时间）
COPY go.mod go.sum ./
RUN GOPROXY=https://goproxy.cn,direct GOSUMDB=off go mod download

# 复制全部源码（含 go.mod / go.sum）
COPY . .

# 依赖兜底：go mod tidy 扫描全部包并确保 go.mod / go.sum 完整一致
# （仓库已附带基于官方 checksum 数据库生成的 go.sum；绝大多数情况无需任何下载）
RUN GOPROXY=https://goproxy.cn,direct GOSUMDB=off go mod tidy

# 静态编译，CGO 禁用（modernc.org/sqlite 为纯 Go 驱动，无需 CGO）
RUN CGO_ENABLED=0 GOFLAGS=-mod=mod go build -ldflags="-s -w" -o video-downloader ./cmd/server

# ============ 阶段2: 运行时 ============
FROM python:3.12-slim

LABEL maintainer="video-downloader"
LABEL description="多平台视频下载器 (Go + yt-dlp + ffmpeg, DB + 会员 + 订单)"

WORKDIR /app

# 运行时非 root 用户
RUN groupadd -r app && useradd -r -g app -d /app -s /sbin/nologin app

# 安装系统依赖：ca-certificates、curl、ffmpeg（音视频合并，Debian 官方源）、
# libcap2-bin（setcap 赋予二进制绑定 80 端口的内核能力，供非 root app 用户运行）
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    ffmpeg \
    libcap2-bin \
    && rm -rf /var/lib/apt/lists/*

# 安装 yt-dlp 最新版（官方 pip 安装）
RUN pip install --no-cache-dir -U yt-dlp

# 复制 Go 二进制与入口脚本
COPY --from=builder /build/video-downloader /app/video-downloader
COPY docker/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# 非 root 进程也允许绑定 80（HTTP 默认端口，Cloudflare 回源 / IP 直连无需端口）
RUN setcap 'cap_net_bind_service=+ep' /app/video-downloader

# 数据目录（数据库、下载、加密 Cookie）；属主修正由 entrypoint 在启动时完成
RUN mkdir -p /app/data /app/downloads /app/cookies_store

# 环境变量（生产使用 .env / compose 注入；此处仅声明默认）
ENV APP_ENV=production
ENV APP_ADDR=:80
ENV TZ=Asia/Shanghai
ENV DATABASE_URL=./data/app.db
ENV DOWNLOAD_DIR=./downloads
ENV COOKIE_DIR=./cookies_store

EXPOSE 80

# 健康检查（固定 127.0.0.1:80；避免 APP_ADDR=:8080 拼出非法 URL）
HEALTHCHECK --interval=30s --timeout=10s --retries=3 \
    CMD curl -sf http://127.0.0.1:80/api/health || exit 1

# 启动：入口脚本修正目录属主后以 app 用户降权运行（应用进程非 root）
CMD ["/app/entrypoint.sh"]

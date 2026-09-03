#!/bin/sh
# video-downloader 容器入口：
#   1. 以 root 确保数据目录存在并修正属主（兼容 bind mount 宿主目录属主为 root 的场景）
#   2. 降权为 app 用户启动应用（生产安全要求：应用进程非 root 运行）
set -e

# 数据目录（数据库 / 下载 / 加密 Cookie），兼容宿主机挂载目录属主不一致
mkdir -p /app/data /app/downloads /app/cookies_store
chown -R app:app /app/data /app/downloads /app/cookies_store

# 降权执行（util-linux setpriv；Debian slim 自带）
if command -v setpriv >/dev/null 2>&1; then
    exec setpriv --reuid=app --regid=app --clear-groups /app/video-downloader
fi
# 兜底：su 或直接运行（最后兜底仅适用于目录已由本脚本修正的场景）
if command -v su >/dev/null 2>&1; then
    exec su -s /bin/sh app -c /app/video-downloader
fi
exec /app/video-downloader

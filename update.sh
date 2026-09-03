#!/usr/bin/env bash
#================================================================
# video-downloader 一键更新脚本（拉取最新代码 → 构建镜像 → 启动）
#
# 用法（任选其一）：
#
#   方式 A：远程 curl 更新（推荐）
#   curl -fsSL https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/update.sh | bash
#
#   方式 B：本地脚本更新（已克隆仓库）
#   cd ~/video-downloader && bash update.sh
#
# 环境变量（可选）：
#   INSTALL_DIR  安装目录，默认 $HOME/video-downloader
#   PORT         服务端口，默认 80（与 docker-compose.yml 一致）
#
# 前提：已通过 bootstrap.sh 一键部署过（存在 ~/video-downloader/.git 与 docker compose 环境）
# 数据卷（data/ downloads/ cookies_store/）挂载于安装目录，更新不会丢失任何数据。
#================================================================

set -e

REPO_URL="https://github.com/rsxbgdurxbjcx-arch/video-downloader.git"
INSTALL_DIR="${INSTALL_DIR:-$HOME/video-downloader}"
PORT="${PORT:-80}"

# ---------- 颜色 ----------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; }
step()  { echo -e "${BLUE}[STEP]${NC}  $*"; }

# ---------- 检查已部署 ----------
check_installed() {
    if [ ! -d "$INSTALL_DIR/.git" ]; then
        error "未找到已部署的仓库目录：$INSTALL_DIR"
        echo ""
        echo "请先执行一键部署："
        echo "  curl -fsSL https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/bootstrap.sh | bash"
        exit 1
    fi
}

# ---------- 拉取最新代码 ----------
pull_latest() {
    cd "$INSTALL_DIR"
    step "拉取最新代码..."
    git fetch --all -q
    git reset --hard origin/main -q 2>/dev/null || git reset --hard origin/master -q 2>/dev/null || true
    local cur
    cur=$(git rev-parse --short HEAD 2>/dev/null || echo "?")
    info "代码已更新到最新版本（commit: ${cur}）"
    # 注意：.env（含 Spug 邮件模板编码）仅保存在服务器本地，更新不会覆盖它
    info "本地 .env（密钥/Spug 邮件推送配置）保持不变"
}

# ---------- 构建镜像并启动 ----------
docker_up() {
    step "构建镜像并启动服务..."
    local DOCKER_CMD="docker"
    if [ "$(id -u)" -ne 0 ] && ! docker ps &>/dev/null; then
        DOCKER_CMD="sudo docker"
    fi
    $DOCKER_CMD compose up -d --build
    info "容器已启动"
}

# ---------- 等待服务就绪 ----------
wait_for_service() {
    step "等待服务启动..."
    local max_wait=60
    local waited=0
    while [ $waited -lt $max_wait ]; do
        if curl -sf "http://localhost:${PORT}/api/health" >/dev/null 2>&1 \
           || curl -sf "http://127.0.0.1:${PORT}/api/health" >/dev/null 2>&1; then
            info "服务已就绪！"
            echo ""
            echo "========================================"
            echo "  ✅ 更新成功！"
            echo "  🌐 访问地址: http://localhost:${PORT}"
            echo "  📋 查看日志: cd $INSTALL_DIR && docker compose logs -f"
            echo "  🛑 停止服务: cd $INSTALL_DIR && docker compose down"
            echo "========================================"
            return 0
        fi
        sleep 2
        waited=$((waited + 2))
        echo -ne "\r  等待中... ${waited}s"
    done
    echo ""
    warn "服务未在 ${max_wait}s 内就绪，请检查日志: cd $INSTALL_DIR && docker compose logs"
}

# ---------- 主流程 ----------
main() {
    echo ""
    echo "========================================"
    echo "  video-downloader 一键更新"
    echo "========================================"
    check_installed
    pull_latest
    docker_up
    wait_for_service
}

main "$@"

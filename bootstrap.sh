#!/usr/bin/env bash
#================================================================
# 多平台视频下载器 - 一键部署脚本 (Go + Docker)
#
# 用法（任选其一）：
#
#   方式 A：curl 一键安装（推荐）
#   curl -fsSL https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/bootstrap.sh | bash
#
#   方式 B：wget 一键安装
#   wget -qO- https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/bootstrap.sh | bash
#
#   方式 C：手动克隆后运行
#   git clone https://github.com/rsxbgdurxbjcx-arch/video-downloader.git
#   cd video-downloader
#   docker compose up -d --build
#
# 环境变量（可选）：
#   INSTALL_DIR  安装目录，默认 $HOME/video-downloader
#   PORT         服务端口，默认 80（HTTP 默认端口；Cloudflare 回源 / IP 直连均无需填写端口）
#   APP_ENV      默认 production（完整安全校验）；本地调试可用 development
#
# 脚本自动完成：拉取仓库 → 生成 .env（随机密钥与管理密码） → 构建镜像 → 启动服务
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

echo ""
echo "========================================"
echo "  多平台视频下载器 一键部署 (Go + Docker)"
echo "  支持：哔哩哔哩/抖音/小红书/Likee/Instagram/YouTube"
echo "========================================"
echo ""

# ---------- 检测包管理器 ----------
detect_pkg_manager() {
    if command -v apt-get &>/dev/null; then echo "apt"
    elif command -v dnf &>/dev/null; then echo "dnf"
    elif command -v yum &>/dev/null; then echo "yum"
    elif command -v apk &>/dev/null; then echo "apk"
    elif command -v pacman &>/dev/null; then echo "pacman"
    else echo "unknown"
    fi
}

pkg_install() {
    local pm
    pm=$(detect_pkg_manager)
    local pkgs=("$@")
    local SUDO=""
    [ "$(id -u)" -ne 0 ] && SUDO="sudo"
    case "$pm" in
        apt)
            $SUDO apt-get update -qq 2>/dev/null || true
            $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${pkgs[@]}"
            ;;
        dnf)  $SUDO dnf install -y -q "${pkgs[@]}" ;;
        yum)  $SUDO yum install -y -q "${pkgs[@]}" ;;
        apk)  $SUDO apk add --no-cache "${pkgs[@]}" ;;
        pacman) $SUDO pacman -S --noconfirm --needed "${pkgs[@]}" ;;
        *)
            error "未识别的包管理器，请手动安装: ${pkgs[*]}"
            exit 1
            ;;
    esac
}

# ---------- 确保 git ----------
ensure_git() {
    if command -v git &>/dev/null; then return 0; fi
    step "安装 git..."
    pkg_install git
    info "git 安装完成"
}

# ---------- 确保 docker ----------
ensure_docker() {
    if command -v docker &>/dev/null; then return 0; fi
    step "安装 Docker..."
    local pm
    pm=$(detect_pkg_manager)
    case "$pm" in
        apt)
            pkg_install ca-certificates curl gnupg lsb-release
            local SUDO=""
            [ "$(id -u)" -ne 0 ] && SUDO="sudo"
            $SUDO mkdir -p /etc/apt/keyrings
            curl -fsSL https://download.docker.com/linux/$(. /etc/os-release; echo "$ID")/gpg | $SUDO gpg --dearmor -o /etc/apt/keyrings/docker.gpg 2>/dev/null || true
            echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$(. /etc/os-release; echo "$ID") $(lsb_release -cs) stable" | $SUDO tee /etc/apt/sources.list.d/docker.list > /dev/null
            $SUDO apt-get update -qq
            $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin
            $SUDO systemctl enable --now docker
            ;;
        *)
            warn "请手动安装 Docker: https://docs.docker.com/engine/install/"
            exit 1
            ;;
    esac
    info "Docker 安装完成"
}

# ---------- 克隆或更新仓库 ----------
clone_or_update() {
    if [ -d "$INSTALL_DIR/.git" ]; then
        step "仓库已存在，拉取最新代码..."
        cd "$INSTALL_DIR"
        git fetch --all -q
        git reset --hard origin/main -q 2>/dev/null || git reset --hard origin/master -q 2>/dev/null || true
        info "代码已更新到最新版本"
    else
        step "克隆仓库到 $INSTALL_DIR ..."
        if [ -d "$INSTALL_DIR" ]; then
            warn "目录已存在但非 git 仓库，备份后重新克隆"
            mv "$INSTALL_DIR" "${INSTALL_DIR}_backup_$(date +%s)"
        fi
        git clone --depth 1 "$REPO_URL" "$INSTALL_DIR"
        cd "$INSTALL_DIR"
        info "仓库克隆完成"
    fi
}

# ---------- 准备 .env（首次部署自动生成随机密钥与管理员密码）----------
# Spug 邮件推送配置仅保存在服务器本地 .env（.gitignore 忽略，绝不进 Git 仓库）。
prepare_env() {
    local env_file="$INSTALL_DIR/.env"
    if [ -f "$env_file" ]; then
        info "检测到现有 .env，跳过生成（如需重置请删除 .env 后重跑）"
        check_spug_hint
        return 0
    fi
    step "生成 .env 配置文件..."
    cp "$INSTALL_DIR/.env.example" "$env_file"

    # 服务器一键部署默认 production（安全校验完整、邮件真实推送）；
    # 如需本地调试可传 APP_ENV=development
    sed -i "s|^APP_ENV=.*|APP_ENV=${APP_ENV:-production}|" "$env_file"

    local gen=""
    if command -v openssl &>/dev/null; then
        gen() { openssl rand -hex "${1:-32}"; }
    else
        gen() { head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-"${1:-32}"; }
    fi

    sed -i "s|^SESSION_SECRET=.*|SESSION_SECRET=$(gen 32)|" "$env_file"
    sed -i "s|^COOKIE_ENCRYPTION_KEY=.*|COOKIE_ENCRYPTION_KEY=$(gen 32)|" "$env_file"
    local admin_pw
    admin_pw=$(gen 16)
    sed -i "s|^ADMIN_INITIAL_PASSWORD=.*|ADMIN_INITIAL_PASSWORD=${admin_pw}|" "$env_file"

    # 支持通过环境变量注入初始管理员（仅写入服务器本地 .env）：
    # ADMIN_INITIAL_EMAIL=xxx@qq.com ADMIN_INITIAL_PASSWORD=自定义密码 ./bootstrap.sh
    if [ -n "${ADMIN_INITIAL_EMAIL:-}" ]; then
        sed -i "s|^ADMIN_INITIAL_EMAIL=.*|ADMIN_INITIAL_EMAIL=${ADMIN_INITIAL_EMAIL}|" "$env_file"
    fi
    if [ -n "${ADMIN_INITIAL_PASSWORD:-}" ]; then
        sed -i "s|^ADMIN_INITIAL_PASSWORD=.*|ADMIN_INITIAL_PASSWORD=${ADMIN_INITIAL_PASSWORD}|" "$env_file"
    fi

    # 支持通过环境变量注入 Spug 邮件推送配置（仅写入服务器本地 .env，不进仓库）：
    # SPUG_MAIL_TEMPLATE_CODE=zxJZVrvBgBr9lMea ./bootstrap.sh
    #   （模板编码获取：push.spug.cc → 控制台 → 验证码 → 邮件 → 选中官方模板后复制）
    if [ -n "${SPUG_MAIL_TEMPLATE_CODE:-}" ]; then
        sed -i "s|^SPUG_MAIL_ENABLED=.*|SPUG_MAIL_ENABLED=true|" "$env_file"
        sed -i "s|^SPUG_MAIL_BASE_URL=.*|SPUG_MAIL_BASE_URL=${SPUG_MAIL_BASE_URL:-https://push.spug.cc}|" "$env_file"
        sed -i "s|^SPUG_MAIL_TEMPLATE_CODE=.*|SPUG_MAIL_TEMPLATE_CODE=${SPUG_MAIL_TEMPLATE_CODE}|" "$env_file"
        info ".env 已生成（含随机 SESSION_SECRET / COOKIE_ENCRYPTION_KEY 与 Spug 邮件推送配置）"
    else
        info ".env 已生成（含随机 SESSION_SECRET / COOKIE_ENCRYPTION_KEY）"
    fi

    warn "初始管理员：ADMIN_INITIAL_EMAIL=$(grep '^ADMIN_INITIAL_EMAIL=' "$env_file" | cut -d= -f2)"
    warn "初始管理员密码：$(grep '^ADMIN_INITIAL_PASSWORD=' "$env_file" | cut -d= -f2)（请立即登录备份，并通过邮箱完成验证）"
    check_spug_hint
}

# 检查 .env 中 Spug 邮件推送配置：未启用或仍为占位符时醒目提示（验证码不会发送邮件）
check_spug_hint() {
    local env_file="$INSTALL_DIR/.env"
    [ -f "$env_file" ] || return 0
    if ! grep -q "^SPUG_MAIL_ENABLED=true" "$env_file"; then
        warn "⚠️  SPUG_MAIL_ENABLED=false：验证码不会通过邮件发送！"
        warn "    - 开发模式（APP_ENV=development）：验证码仅输出到容器日志（docker compose logs -f）"
        warn "    - 生产模式（APP_ENV=production）：注册将拒绝发送验证码"
        warn "    请编辑服务器本地 .env：SPUG_MAIL_ENABLED=true + SPUG_MAIL_TEMPLATE_CODE（Spug 邮件模板编码）"
    elif grep -q "your-spug-template-code" "$env_file"; then
        warn "⚠️  .env 中的 Spug 邮件模板编码仍为占位符（your-spug-template-code）！"
        warn "    请登录 https://push.spug.cc → 控制台 → 验证码 → 邮件，选中官方模板后复制「模板编码」"
        warn "    并编辑 .env 的 SPUG_MAIL_TEMPLATE_CODE；同时在「开发者设置 → 安全设置」把服务器出网 IP 加入 IP 白名单"
        warn "    修改完成后执行：docker compose up -d"
    fi
}

# ---------- Docker 构建并启动 ----------
docker_up() {
    step "Docker 构建并启动..."
    cd "$INSTALL_DIR"

    # 如果有 PORT 环境变量且非默认值，修改 docker-compose.yml 端口映射（默认 80）
    if [ "$PORT" != "80" ]; then
        sed -i "s/\"80:80\"/\"${PORT}:80\"/" docker-compose.yml
        sed -i "s/APP_ADDR=:80/APP_ADDR=:${PORT}/" docker-compose.yml
    fi

    # 检查是否需要 sudo
    local DOCKER_CMD="docker"
    if [ "$(id -u)" -ne 0 ] && ! docker ps &>/dev/null; then
        DOCKER_CMD="sudo docker"
    fi

    $DOCKER_CMD compose up -d --build
    info "Docker 容器已启动"
}

# ---------- 等待服务就绪 ----------
wait_for_service() {
    step "等待服务启动..."
    local max_wait=60
    local waited=0
    while [ $waited -lt $max_wait ]; do
        if curl -sf "http://localhost:${PORT}/api/health" >/dev/null 2>&1 || curl -sf "http://127.0.0.1:${PORT}/api/health" >/dev/null 2>&1; then
            info "服务已就绪！"
            echo ""
            echo "========================================"
            echo "  ✅ 部署成功！"
            echo "  🌐 访问地址: http://localhost:${PORT}"
            echo "  🌐 生产环境请配置 Nginx/Caddy 反向代理提供 HTTPS（详见 README）"
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
    ensure_git
    clone_or_update
    ensure_docker
    prepare_env
    docker_up
    wait_for_service
}

main "$@"

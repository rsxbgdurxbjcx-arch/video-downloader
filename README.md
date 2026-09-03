# 📺 Video Downloader

> 基于 **Go + yt-dlp + ffmpeg** 的多平台视频下载器，集成**邮箱注册/验证、会员套餐、订单与 Mock 支付、管理员后台**。
> 全平台统一采用最高画质选流策略（`bestvideo+bestaudio/best` + `--format-sort res,fps,tbr` + mkv 合并），支持 Cookie 加密持久化与无感链接提取。

![Go](https://img.shields.io/badge/Go-1.23-00ADD8)
![yt-dlp](https://img.shields.io/badge/yt--dlp-latest-red)
![ffmpeg](https://img.shields.io/badge/ffmpeg-7.0-static-orange)

## 功能特性

- 🎬 **全平台统一最高画质策略**：6 大平台（哔哩哔哩/抖音/小红书/Likee/Instagram/YouTube）一次调用 yt-dlp 完成选流、下载、ffmpeg 合并（mkv）
- 🚀 **原生并发分片**：`--concurrent-fragments 8` 多线程下载
- 🎯 **抖音自研解析器**：分享页 SSR 数据 + play API `ratio` 画质档直达原画；解析失败自动回退 yt-dlp
- ⚡ **实时进度**：流式解析 yt-dlp 输出（视频流/音频流分段显示）
- 🔐 **邮箱注册与验证**：仅邮箱注册；**6 位数字验证码**（密码学安全随机、哈希存储、5 分钟有效、单次使用、错误最多 5 次）；60 秒冷却 + 每小时 6 次 + IP 限流（Redis/数据库双实现）；未验证不能登录/下单/下载；验证码邮件经 **Spug 推送助手**（push.spug.cc）官方中文/英文模板发送
- 👤 **会话与用户中心**：服务端 Session（HttpOnly + SameSite=Lax + 生产 Secure）、修改密码立即注销旧会话
- 💎 **会员套餐**：免费/月/季/年套餐；后端统一权限（次数/每日额度/并发/文件大小/清晰度）；续费自动顺延
- 🧾 **订单与支付抽象**：`PaymentProvider` 接口 + Mock 支付（仅开发环境）；幂等支付事件，绝不重复开通会员；为爱发电等真实渠道预留扩展点
- 🛡️ **管理员后台**：用户/订单/套餐/任务/支付事件/审计日志；高风险操作全审计
- 🍪 **Cookie 加密存储**：AES-256-GCM 加密落盘；API 仅返回脱敏前缀；日志/错误/响应绝不出现明文
- 🧹 **自动清理**：失败清理、拉取完成后 60 秒宽限清理、10 分钟定时清理（跳过进行中任务）
- 🐳 **Docker 部署**：非 root 运行、健康检查、优雅关闭；开发模式验证码直接输出到容器日志

## 一键部署脚本（推荐）

> 仓库地址：**https://github.com/rsxbgdurxbjcx-arch/video-downloader**（脚本为仓库内 `bootstrap.sh` 的最新版）

一条命令完成 **拉取仓库 → 生成 `.env`（随机密钥与管理密码）→ 构建镜像 → 启动服务**：

```bash
curl -fsSL https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/bootstrap.sh | bash
```

也支持 wget：

```bash
wget -qO- https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/bootstrap.sh | bash
```

### 脚本自动完成

| 步骤 | 说明 |
|------|------|
| ① 拉取仓库 | 安装 git（如缺失）→ 克隆 `video-downloader` 到 `~/video-downloader`（已存在则拉取最新代码） |
| ② 准备配置 | 首次部署自动执行 `cp .env.example .env`，并**生成随机 `SESSION_SECRET` / `COOKIE_ENCRYPTION_KEY` 与管理密码**（不会保留默认值） |
| ③ 构建镜像 | 安装 Docker（如缺失）→ `docker compose up -d --build`（多阶段构建，含 yt-dlp/ffmpeg） |
| ④ 启动与自检 | 等待服务启动 → 自动探测 `http://localhost:${PORT}/api/health` → 打印访问地址与管理密码 |

### 部署成功后的信息

- **访问地址**：`http://服务器IP`（默认端口 80，**无需填写端口**；配置 Cloudflare 后可直接 `http(s)://你的域名` 访问，见「Cloudflare 代理」）；
- **邮件推送**：脚本生成的 `.env` 已**预填 Spug 推送助手邮件验证码示例**（`SPUG_MAIL_ENABLED=true` + 占位符）；**请立即编辑 `~/video-downloader/.env`，把 `SPUG_MAIL_TEMPLATE_CODE` 改为你在 Spug 控制台选定的官方模板编码**（脚本也会输出该提醒），详见「接入 Spug 推送助手邮件验证码」；
- **初始管理员**：脚本打印 `ADMIN_INITIAL_EMAIL`（默认 `admin@example.com`）与随机密码；**请立即登录并修改密码**，并在首次启动后完成邮箱验证；
- **数据持久化**：`./data`（SQLite）、`./downloads`（下载文件）、`./cookies_store`（加密 Cookie）挂载于安装目录，容器重启/升级不丢失。

### 自定义参数

```bash
# 指定安装目录与端口（默认 80；改为其他端口时域名/IP 访问需带端口）
INSTALL_DIR=/opt/video-downloader PORT=80 \
  curl -fsSL https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/bootstrap.sh | bash
```

> ⚠️ 生产环境注意：脚本生成 `.env` 后，请按下方「接入 Spug 推送助手邮件验证码」编辑 `~/video-downloader/.env`（未配置 Spug 邮件推送时，生产环境无法发送验证码邮件，未验证用户无法登录/下单/下载；开发模式则验证码仅输出到容器日志）。管理员邮箱务必改为您自己的邮箱。

## 服务器本地 .env 接入 Spug 推送助手邮件验证码（完整、可直接执行）

> 🔐 **模板编码（调用凭证）只保存在服务器本地 `.env`**（已被 `.gitignore` 忽略，绝不进入 Git 仓库）。
> 仓库中不存在任何真实凭据。

### 第一步：Spug 控制台获取模板编码（官方文档：https://push.spug.cc/guide/mail）

1. 打开 **https://push.spug.cc** → 右上角登录（统一账户中心 user.spug.cc，支持手机号 + 密码）；
2. 进入 **控制台 → 验证码 → 邮件**：平台提供中文、英文两套**官方邮件模板**（内容不可修改），选中中文模板（`验证码`）或英文模板（`Verification Code`），复制右侧的**模板编码**（16 位字母数字组合）——这就是调用凭证；
3. 进入 **控制台 → 开发者设置 → 安全设置**：把**本项目服务器的出网 IP** 加入 **IP 白名单**（未加入时接口返回 `403 IP安全策略拒绝`）；
4. 确认账户 **邮件余额 > 0**（控制台可查看；每封消耗 1 点）；⚠️ 连续错误调用会触发 **IP 限流**，不要枚举模板编码。

### 第二步：在服务器上生成/编辑本地 .env

```bash
cd ~/video-downloader                      # 项目目录（未部署先执行上方一键部署脚本）

# 首次部署：从模板创建 .env
[ -f .env ] || cp .env.example .env

# 随机密钥（新部署必须；一行一条，依次执行）
sed -i "s/^SESSION_SECRET=.*/SESSION_SECRET=$(openssl rand -hex 32)/" .env
sed -i "s/^COOKIE_ENCRYPTION_KEY=.*/COOKIE_ENCRYPTION_KEY=$(openssl rand -hex 32)/" .env

# 生产模式（可选但推荐）
sed -i 's/^APP_ENV=.*/APP_ENV=production/' .env

# ⭐ Spug 邮件推送（把 your-spug-template-code 换成第一步复制的模板编码）
sed -i 's/^SPUG_MAIL_ENABLED=.*/SPUG_MAIL_ENABLED=true/' .env
sed -i 's/^SPUG_MAIL_BASE_URL=.*/SPUG_MAIL_BASE_URL=https:\/\/push.spug.cc/' .env
sed -i 's/^SPUG_MAIL_TEMPLATE_CODE=.*/SPUG_MAIL_TEMPLATE_CODE=your-spug-template-code/' .env

# 初始管理员（邮箱必须能收信；密码 ≥8 位含字母数字，不能是 change-me）
sed -i 's/^ADMIN_INITIAL_EMAIL=.*/ADMIN_INITIAL_EMAIL=your-email@qq.com/' .env
sed -i 's/^ADMIN_INITIAL_PASSWORD=.*/ADMIN_INITIAL_PASSWORD=你的管理员密码/' .env

# 确认配置生效
grep -E "SPUG_MAIL_ENABLED|SPUG_MAIL_BASE_URL|SPUG_MAIL_TEMPLATE_CODE|APP_ENV" .env
```

> 也可用环境变量一次性注入（仅写入服务器本地 `.env`，不进仓库）：
> ```bash
> SPUG_MAIL_TEMPLATE_CODE=your-spug-template-code \
> ADMIN_INITIAL_EMAIL=your-email@qq.com ADMIN_INITIAL_PASSWORD=你的管理员密码 \
>   bash bootstrap.sh
> ```

### 第三步：构建并启动

```bash
docker compose up -d --build
docker compose logs --tail 30 video-downloader
# 启动日志应出现：📧 邮件推送已启用: spug mail api=https://push.spug.cc（官方模板验证码；模板编码为调用凭证，不落日志）
```

### 第四步：验证邮件推送

1. 登录管理后台 → **「邮件」标签页**：每次验证码/测试邮件的发送结果与服务器返回错误一目了然；
2. 或 登录后 → 用户中心 → 「发送测试邮件」（返回精确错误原因，如 IP 白名单/余额不足；测试邮件正文为随机测试验证码）；
3. 也可在 Spug 控制台 **记录 / 调试日志** 查看 request_id 与最终投递状态（`/console/records`、`/console/logs`）。

> ✅ 升级/更新不会影响本地 `.env`（`update.sh` 的 `git reset --hard` 不触碰被忽略的文件），Spug 配置一次配置、长期有效。

## 一键更新脚本

> 仓库地址：**https://github.com/rsxbgdurxbjcx-arch/video-downloader**（脚本为仓库内 `update.sh` 的最新版）

一条命令完成 **拉取最新代码 → 构建镜像 → 启动/重启服务**：

```bash
curl -fsSL https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/update.sh | bash
```

或在已克隆的仓库内直接运行：

```bash
cd ~/video-downloader && bash update.sh
```

### 脚本自动完成

| 步骤 | 说明 |
|------|------|
| ① 检查部署 | 校验 `~/video-downloader` 已存在（未部署会提示先执行一键部署脚本） |
| ② 拉取最新代码 | `git fetch --all` + `git reset --hard origin/main`（提交记录自动更新到最新） |
| ③ 构建镜像 | `docker compose up -d --build`（有改动才重编译，增量构建） |
| ④ 启动与自检 | 等待服务启动 → 探测 `http://localhost:${PORT}/api/health` → 打印访问地址 |

### 更新安全说明

- **数据不丢失**：`./data`（SQLite 数据库）、`./downloads`（下载文件）、`./cookies_store`（加密 Cookie）挂载于安装目录，更新仅替换应用镜像，用户/邮箱验证状态/会员/订单/任务数据全部保留；
- **`.env` 保留**：更新不会覆盖已有的 `.env`（密钥、Spug 邮件推送、管理员配置维持不变）；
- **数据库迁移自动执行**：新版本含新迁移时，服务启动会自动应用（`schema_migrations` 记录版本）。

### 自定义参数

```bash
# 指定安装目录与端口（默认 80）
INSTALL_DIR=/opt/video-downloader PORT=80 \
  curl -fsSL https://raw.githubusercontent.com/rsxbgdurxbjcx-arch/video-downloader/main/update.sh | bash
```

> 💡 重新执行「一键部署脚本」（`bootstrap.sh`）同样会自动完成更新（其内置"仓库已存在则拉取最新代码"逻辑），两者可以互换使用。

## 快速开始（Docker 生产）

```bash
# 1. 准备环境变量
cp .env.example .env
# 2. 生成随机密钥（必须替换！）
#    生产环境使用默认管理员密码 change-me 时拒绝启动
openssl rand -hex 32   # SESSION_SECRET
openssl rand -hex 32   # COOKIE_ENCRYPTION_KEY
sed -i "s/SESSION_SECRET=.*/SESSION_SECRET=$(openssl rand -hex 32)/" .env
sed -i "s/COOKIE_ENCRYPTION_KEY=.*/COOKIE_ENCRYPTION_KEY=$(openssl rand -hex 32)/" .env
# 3. 配置 Spug 邮件推送（生产必须；未配置时生产环境拒绝发送验证邮件）
vim .env    # SPUG_MAIL_ENABLED=true SPUG_MAIL_TEMPLATE_CODE=<Spug 模板编码>（见「接入 Spug 推送助手邮件验证码」）
# 4. 启动（默认端口 80：域名 / IP 访问均无需填写端口）
docker compose up -d --build
```

访问 `http://服务器IP`（默认端口 80）或 `http(s)://你的域名`（配置 Cloudflare 后，见「Cloudflare 代理」；如通过 Nginx/Caddy 终结 HTTPS，见下方反向代理示例）。

> 存储说明：数据库/下载/Cookie 使用 Docker **命名卷**（`video-data` / `video-downloads` / `video-cookies`），重建容器不丢失；旧版本使用 `./data` 等目录（bind mount），升级后如需迁移旧数据，请执行：
> `docker run --rm -v $(pwd)/data:/src -v video-downloader_video-data:/dst alpine sh -c 'cp -a /src/. /dst/'`

## Cloudflare 代理（域名访问，无需端口）

服务默认监听 **80**（Cloudflare 边缘节点默认支持 80/443，可直接回源）：

1. 在 Cloudflare 添加 DNS **A 记录**：`video.example.com → 服务器 IP`，并开启 **代理（橙色云朵）**；
2. Cloudflare → **SSL/TLS → Overview**：选择「灵活（Flexible）」（源站无证书）或「完全（Full）」（源站有证书即可）；边缘侧自动为用户提供 HTTPS，用户访问 `https://video.example.com` **无需填写端口**；
3. 浏览器直接访问：`https://video.example.com`（域名）或 `http://服务器IP`（直连，80 无需端口）；
4. 服务端已自动识别 Cloudflare 真实客户端 IP（`CF-Connecting-IP`）：登录限流 / 邮件限流 / 审计均按真实 IP 计数，不会误把 Cloudflare 地址当作客户端；
5. 通过 Cloudflare 访问时 Cookie 自动带 `Secure`（识别 `X-Forwarded-Proto: https`），功能不受影响；
6. 建议在 Cloudflare **SSL/TLS → Edge Certificates** 开启「Always Use HTTPS」强制跳转，回源保持 HTTP:80。

> ⚠️ 安全提示：开启 Cloudflare 代理后请勿泄露源站 IP；如不使用 CF，可换用 Nginx/Caddy 反代（见下）。

### Nginx 反向代理（HTTPS 示例）

```nginx
server {
    listen 443 ssl;
    server_name video.example.com;
    ssl_certificate     /etc/letsencrypt/live/video.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/video.example.com/privkey.pem;

    client_max_body_size 20m;
    location / {
        proxy_pass http://127.0.0.1:80;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_buffering off;              # 大文件推送需禁用缓冲
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

### Caddy 反向代理（自动 HTTPS）

```
video.example.com {
    reverse_proxy 127.0.0.1:80
}
```

## 开发环境（本地）

```bash
export APP_ENV=development
# 本机 go run 默认绑定 80（需 root）；普通用户本地开发可换用 8080：
export APP_ADDR=:8080
export DATABASE_URL=./data/app.db
export COOKIE_ENCRYPTION_KEY=$(openssl rand -hex 32)
export SESSION_SECRET=$(openssl rand -hex 32)
# 不配置 SPUG_MAIL_ENABLED 时：验证码仅输出到服务控制台（不发送真实邮件）
go run ./cmd/server
```

- 开发环境验证码也会输出到服务控制台（生产环境绝不会输出验证码）；
- 开发环境可调用 `POST /api/orders/{order_no}/simulate-pay` 完成 Mock 支付（生产环境该接口 404）；
- 未配置 Spug 邮件推送时，开发环境注册仍可完成（控制台输出验证码），**生产环境则拒绝**（不静默绕过邮箱验证）。

## Spug 邮件推送配置说明

> 🔐 **模板编码（调用凭证）等敏感配置只保存在服务器本地 `.env`**（`.gitignore` 已忽略，不进 Git 仓库）；完整接入步骤见上方「接入 Spug 推送助手邮件验证码」。

| 变量 | 说明 |
|------|------|
| `SPUG_MAIL_ENABLED` | 是否启用 Spug 邮件推送（生产必须 true；默认 false） |
| `SPUG_MAIL_BASE_URL` | Spug 推送助手 API 地址（默认 `https://push.spug.cc`，一般无需修改） |
| `SPUG_MAIL_TEMPLATE_CODE` | Spug 官方邮件模板编码（**调用凭证**；push.spug.cc → 控制台 → 验证码 → 邮件 复制） |
| `EMAIL_VERIFICATION_EXPIRE_MINUTES` | 验证码有效期（默认 **5** 分钟，作为 Spug 请求 `minute` 参数） |
| `EMAIL_SEND_COOLDOWN_SECONDS` | 同邮箱发送冷却（默认 **60** 秒） |
| `EMAIL_HOURLY_LIMIT` | 同邮箱每小时最多发送次数（默认 **6**） |
| `EMAIL_IP_HOURLY_LIMIT` | 同 IP 每小时最多发送次数（默认 **30**，防止更换邮箱刷邮件） |
| `VERIFY_CODE_MAX_ATTEMPTS` | 验证码错误最大尝试次数（默认 **5**，超限立即作废） |
| `REDIS_URL` | 可选（如 `redis://127.0.0.1:6379`）；配置后邮件限流使用 Redis 原子计数，未配置或不可达时自动降级为数据库统计 |

模板编码绝不写入日志；验证码绝不写入日志；验证码通过 POST body 提交（不走 URL），`APP_BASE_URL` 不再要求 HTTPS（裸 IP/HTTP 服务器也可正常部署）。
发送链路（官方文档 https://push.spug.cc/guide/mail）：`POST https://push.spug.cc/mail/<TEMPLATE_CODE>`，参数 `to`（收件邮箱）/`scene`（≤12 字符验证场景）/`code`（4-6 位验证码）/`minute`（有效分钟数）；本项目 `code` 为 6 位数字、`minute` 取自验证码有效期配置。

**收不到验证码排查（重要）**：
1. **邮箱必须先注册**：忘记密码/重发接口对「未注册邮箱」出于防枚举**故意不发送**（浏览器仍提示已发送）。用「用户中心 → 邮箱验证/测试邮件」先确认账号存在；
2. **Spug 控制台侧**：控制台 → 记录/调试日志 查看 `request_id` 与最终投递状态（`status=2 成功` / `3 失败` + `reason`）；
3. **失败原因对照**：`IP安全策略拒绝` → 服务器出网 IP 不在白名单（控制台 → 开发者设置 → 安全设置 添加）；`模板编码无效` → 检查 `SPUG_MAIL_TEMPLATE_CODE`；`邮件余额不足` → 控制台充值；`发送频率超限` → 稍后再试；`收件地址已退订或无效` → 换邮箱；
4. 服务日志诊断：`docker compose logs -f video-downloader` 中 `[auth] forgot-password: ...未发送验证码（防枚举）` 表示邮箱未注册；`[auth] 验证邮件发送失败` 表示 Spug 接口出错（用「用户中心 → 发送测试邮件」获取精确错误）；
5. 接口返回成功但未收到邮件：先查**垃圾邮件箱**与企业邮箱拦截策略；再于 Spug 控制台「记录」查看投递状态（`状态=成功` 仅代表通道已受理）。


## 邮箱注册/验证流程（6 位数字验证码）

1. 注册页仅填写 **邮箱 + 密码 + 确认密码**（无用户名/手机号/OAuth，无跳过验证入口）；
2. 邮箱去除首尾空格并转小写，格式校验，密码 ≥8 位且含字母数字；
3. 账户创建为 `pending`，生成 **6 位数字验证码**（密码学安全随机数），**SHA-256 哈希**入库（5 分钟有效、单次使用）；
4. 将邮件中的验证码输入页面 → 事务内激活（`email_verified_at + status=active`）→ **立即删除**全部未用验证码；
5. 验证码错误最多允许 **5 次**（超过上限立即作废，需重新发送）；
6. 重发验证码受 **60 秒冷却（邮箱）**、**每小时 6 次（邮箱）**、**每小时 30 次（IP）** 三重限流；重复注册不会重复发信；已验证才可登录 / 下单 / 下载；注册接口对「邮箱已存在」返回与成功一致的通用提示（防枚举）。

## 支付说明（本阶段）

- **不接入任何真实支付平台**（无虎皮椒/蓝兔/易支付/码支付/支付宝/微信/爱发电 SDK，无支付密钥）；
- `PAYMENT_PROVIDER=mock` **仅限开发环境**；生产环境配置 `PAYMENT_ENABLED=true + mock` 会拒绝启动；
- 订单创建只接收 `plan_id`，金额/时长/权益全部来自数据库（订单表整数分，禁止 float）；
- 支付事件（`payment_events`）唯一约束 + 条件更新 + 会员记录订单唯一索引，**重复回调绝不重复开通**；
- 未来接爱发电：实现 `internal/payment.Provider` 接口并在 `payment.Manager.Register` 注册即可；业务处理复用 `OrderService.ProcessPaidEvent`（用户绑定需自行设计可靠凭证，禁止以昵称/头像绑定）。

## API 一览（节选）

| 接口 | 说明 |
|------|------|
| `POST /api/auth/register` | 邮箱注册（返回通用提示） |
| `POST /api/auth/verify-email` | 邮箱验证（`{"email": "...", "code": "123456"}`） |
| `POST /api/auth/resend-verification` | 重发验证码（三重限流） |
| `GET /api/auth/verification-status?email=` | 验证状态（不泄露未注册邮箱） |
| `POST /api/auth/login` / `POST /api/auth/logout` / `POST /api/auth/logout-all` | 登录/退出（统一失败信息防枚举） |
| `GET /api/auth/me` | 当前用户（无任何哈希/令牌字段） |
| `POST /api/auth/change-password` | 修改密码（注销全部旧会话） |
| `GET /api/membership` | 会员状态与当前权益、剩余额度 |
| `POST /api/email/test` | 发送测试邮件（登录+已验证；诊断 Spug 邮件推送配置，返回具体错误原因） |
| `GET /api/plans` | 套餐列表（金额以分计） |
| `POST /api/orders` / `GET /api/orders` / `GET /api/orders/{no}` | 订单创建 / 列表 / 详情 |
| `POST /api/orders/{no}/close` | 关闭 pending 订单 |
| `POST /api/orders/{no}/simulate-pay` | Mock 支付（**仅开发环境**） |
| `POST /api/download` | 创建下载任务（登录 + 验证 + 会员额度） |
| `GET /api/status/{id}` / `GET /api/file/{id}` / `DELETE /api/task/{id}` / `GET /api/tasks` | 任务查询 / 拉取 / 取消 / 列表（属主隔离） |
| `GET /api/cookies` / `GET/POST/DELETE /api/cookie/{platform}` | Cookie 管理（**仅返回脱敏**） |
| `/api/admin/*` | 管理员后台（用户/订单/套餐/任务/支付事件/审计） |

## 服务未就绪排查

服务构建成功但健康检查失败时，先看日志定位（容器退出原因会打印在最后一行）：

```bash
docker compose logs --tail 50 video-downloader
docker compose ps                          # 查看容器状态（running / unhealthy / restarting）
```

常见原因与处置：

| 原因 | 特征日志 | 处置 |
|------|---------|------|
| 数据目录权限 | `数据库初始化失败: unable to open database file` 或 `初始化目录失败` | 本版本已修复：入口脚本启动时自动修正目录属主；**务必重新构建镜像**（旧镜像无此逻辑） |
| 生产密钥未替换 | `生产环境必须设置 SESSION_SECRET...` 或 `生产环境必须修改 ADMIN_INITIAL_PASSWORD` | 执行 `sed -i "s/^SESSION_SECRET=.*/SESSION_SECRET=$(openssl rand -hex 32)/" .env` 与 `COOKIE_ENCRYPTION_KEY` 同法，并修改 `ADMIN_INITIAL_PASSWORD` |
| Mock 支付误配置 | `生产环境禁止启用 PAYMENT_PROVIDER=mock` | 设置 `PAYMENT_ENABLED=false` |
| 邮件没收到 | 启动日志 `📧 邮件推送未启用...` 或 `[auth] 验证邮件发送失败` 或注册后无邮件 | ① 用「用户中心 → 发送测试邮件」拿到具体错误原因；② 检查 .env：`SPUG_MAIL_ENABLED=true`、`SPUG_MAIL_TEMPLATE_CODE` 为真实模板编码（非占位符）、`APP_ENV=production`；③ 错误为 `IP安全策略拒绝` → 控制台「开发者设置 → 安全设置」把服务器出网 IP 加入白名单；④ 错误为 `邮件余额不足` → 控制台充值；⑤ 开发模式验证码只输出到容器日志（`docker compose logs -f video-downloader`） |
| 依赖下载失败 | `dial tcp ... goproxy.cn` | 检查服务器出网；或设置 `GOPROXY=官方可达代理` 后重新构建 |

> 一键部署脚本（bootstrap.sh）会自动生成随机密钥与管理密码，手动部署请务必按「快速开始」替换 `.env` 中的占位值。

## 数据库

- 默认 SQLite（`DATABASE_URL=./data/app.db`，纯 Go 驱动 `modernc.org/sqlite`，无 CGO）；
- 迁移文件位于 `internal/database/migrations/`（启动自动执行，`schema_migrations` 记录版本）；
- 表：`users` / `email_verification_tokens` / `email_send_records` / `sessions` / `membership_plans` / `user_memberships` / `orders` / `payment_events` / `download_tasks` / `audit_logs`；
- 数据访问层仅依赖 `database/sql` + 参数化 SQL，未来可迁移 PostgreSQL（替换 driver + 微调迁移即可）。

### 数据库备份

```bash
# 在线备份（named volume 方式）
docker compose exec video-downloader sh -c 'cp /app/data/app.db /tmp/app.db.bak' \
 && docker compose cp video-downloader:/tmp/app.db.bak ./backup-app-$(date +%F).db

# 或整个卷备份/恢复
docker run --rm -v video-downloader_video-data:/data -v $(pwd):/backup alpine \
  tar czf /backup/video-data-$(date +%F).tar.gz -C /data .
```

## 安全设计要点

- 密码 bcrypt 哈希；Session/验证码/CSRF 均密码学随机；验证码仅存哈希；
- 平台 Cookie：AES-256-GCM 加密（密钥来自 `COOKIE_ENCRYPTION_KEY`），API 只回脱敏前缀；
- SSRF 防护：平台域名白名单 + DNS 解析校验 + 禁止私网/环回/元数据地址；下载短链每次重定向均校验；
- 路径安全：任务目录为 32 位随机 hex，禁止穿越；
- 防护：CSRF（Double-Submit + SameSite）、CORS 白名单、登录/注册/邮件/全局限流、请求体上限、安全响应头、panic 恢复；
- 审计：管理员高危操作全部落 `audit_logs`（脱敏字段）；
- 邮件：验证码/模板编码绝不写入日志与前端；验证码错误 5 次作废；Spug 模板编码仅环境变量；
- 生产环境：默认管理员密码 → 拒绝启动；Mock 支付 → 拒绝启动；Spug 邮件推送未配置 → 拒绝发信；未配置邮件推送时未验证用户无法登录；
- 所有外部 HTTP 请求均设置超时；日志不输出 Cookie、Authorization、令牌与敏感查询参数。

## 质量检查

```bash
go vet ./...          # 静态检查
gofmt -l .            # 应为空输出
```

> 首次拉取依赖：`go mod tidy` 即可（仓库已附带 `go.sum`，其条目来自官方 checksum 数据库）。
> 若 `sum.golang.org` 无法访问（大陆网络常见），请执行：
> ```bash
> export GOPROXY=https://goproxy.cn,direct
> export GOSUMDB=off    # 关闭远程校验；本地仍会校验 go.sum，防篡改能力不受影响
> go mod tidy
> ```
> Docker 镜像的构建阶段已内置上述配置，一键部署无需处理。

## 部署升级

```bash
git pull && docker compose up -d --build   # 升级（数据卷持久化，见 docker-compose.yml）
docker compose exec video-downloader sh -c '
  pip install --no-cache-dir -U yt-dlp &&   # 更新 yt-dlp
  yt-dlp --version'
```

## 项目结构

```
cmd/server/                  # 入口：配置 → DB 迁移 → 服务组装 → 优雅启停
  static/                    # 前端（embed）
internal/
  config/                    # 环境变量配置 + 生产安全校验
  database/                  # SQLite + 迁移执行器（embed SQL）
  models/                    # 领域模型
  repository/                # 数据访问层（参数化 SQL）
  auth/                      # 注册/验证/登录/会话/密码/令牌
  middleware/                # 认证/管理员/限流/CSRF/CORS/安全头
  handlers/                  # HTTP 处理器 + 路由
  services/                  # 会员权益 / 订单服务
  email/                     # Spug 推送助手邮件发送 + 邮件限流（Redis/DB）
  redisx/                    # 轻量 Redis 客户端（RESP2，纯标准库）
  payment/                   # PaymentProvider 抽象 + Mock/Manual
  downloader/                # 下载引擎（yt-dlp/抖音解析/进度/Cookie 加密）
migrations/ → internal/database/migrations/
bootstrap.sh                 # 一键部署脚本（拉取仓库→生成 .env→构建镜像→启动）
update.sh                    # 一键更新脚本（拉取最新代码→构建镜像→启动）
```

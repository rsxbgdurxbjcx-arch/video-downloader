// video-downloader 前端逻辑（多视图 SPA）
// 视图：下载（保留原有体验）/ 认证 / 用户中心 / 套餐 / 任务 / 管理员后台
// 安全约定：CSRF token（double-submit）通过 jsreadCookie 读取并放入 X-CSRF-Token 头。

const $ = (id) => document.getElementById(id);

// ============ 全局状态 ============
const state = {
    pollTimer: null,
    currentTaskId: null,
    autoDownloaded: false,
    taskActive: false,
    savedCookies: {},      // { platform: { name, mask } }
    platforms: [],
    editLock: null,
    currentUser: null,     // /api/auth/me 结果
    adminTab: "users",
    verifyEmail: "",       // 待验证邮箱（注册后记住，用于重发与提交验证码）
};

const PLATFORM_ORDER = ["bilibili", "douyin", "xiaohongshu", "likee", "instagram", "youtube"];
const PLATFORM_NAMES = {
    bilibili: "哔哩哔哩", douyin: "抖音", xiaohongshu: "小红书",
    likee: "Likee", instagram: "Instagram", youtube: "YouTube"
};

function getPlatformName(key) { return PLATFORM_NAMES[key] || key; }

// ============ 通用请求（CSRF + JSON）============
function csrfToken() {
    const m = document.cookie.match(/(?:^|;\s*)vd_csrf=([^;]+)/);
    return m ? decodeURIComponent(m[1]) : "";
}

async function api(path, options = {}) {
    const opts = { ...options, headers: { ...(options.headers || {}) } };
    if (opts.body && typeof opts.body === "string" && !opts.headers["Content-Type"]) {
        opts.headers["Content-Type"] = "application/json";
    }
    if (opts.method && opts.method !== "GET") {
        opts.headers["X-CSRF-Token"] = csrfToken();
    }
    if (opts.method === undefined && opts.body) opts.method = "POST";
    const resp = await fetch(path, opts);
    let data = null;
    try { data = await resp.json(); } catch (e) { /* 非 JSON */ }
    return { ok: resp.ok, status: resp.status, data };
}

// ============ 导航与视图切换 ============
function renderNav() {
    const nav = $("nav");
    nav.innerHTML = "";
    if (!state.currentUser) {
        const btn = document.createElement("button");
        btn.className = "nav-link nav-link-btn";
        btn.textContent = "注册/登录";
        btn.addEventListener("click", () => showView("auth"));
        nav.appendChild(btn);
        return;
    }
    const items = [
        ["下载", "download"], ["我的任务", "tasks"], ["用户中心", "user"],
        ["会员套餐", "plans"],
    ];
    if (state.currentUser.is_admin) items.push(["管理后台", "admin"]);
    for (const [label, view] of items) {
        const b = document.createElement("button");
        b.className = "nav-link nav-link-btn";
        b.textContent = label;
        b.addEventListener("click", () => showView(view));
        nav.appendChild(b);
    }
    const out = document.createElement("button");
    out.className = "nav-link nav-link-btn nav-link-out";
    out.textContent = "退出";
    out.addEventListener("click", logout);
    nav.appendChild(out);
}

function showView(name) {
    const containers = {
        download: ["inputPanel", "progressPanel"],
        auth: ["view-auth"],
        user: ["view-user"], plans: ["view-plans"],
        tasks: ["view-tasks"], admin: ["view-admin"],
    };
    for (const [view, ids] of Object.entries(containers)) {
        for (const cid of ids) {
            const el = $(cid);
            if (!el) continue;
            if (view === "download") {
                // 下载视图：任务进行中显示进度面板，否则显示输入面板
                const showProgress = state.taskActive && cid === "progressPanel";
                const showInput = !state.taskActive && cid === "inputPanel";
                el.hidden = !(name === "download" && (showProgress || showInput));
            } else {
                el.hidden = view !== name;
            }
        }
    }
    if (name === "user") loadUserCenter();
    if (name === "plans") loadPlans();
    if (name === "tasks") loadTaskList();
    if (name === "admin") loadAdmin("users");
}

// ============ 认证 ============
async function refreshMe() {
    const { ok, data } = await api("/api/auth/me");
    state.currentUser = ok ? data : null;
    renderNav();
    return state.currentUser;
}

function switchAuthTab(tab) {
    $("tabLogin").classList.toggle("active", tab === "login");
    $("tabRegister").classList.toggle("active", tab === "register");
    $("loginForm").hidden = tab !== "login";
    $("registerForm").hidden = tab !== "register";
    $("verifyCodeForm").hidden = true;
    $("verifyResult").hidden = true;
    $("pwdResetForm").hidden = true;
}

async function doLogin() {
    const email = $("loginEmail").value.trim();
    const password = $("loginPassword").value;
    if (!email || !password) { $("loginMsg").textContent = "请输入邮箱和密码"; return; }
    const { ok, data } = await api("/api/auth/login", {
        method: "POST", body: JSON.stringify({ email, password }),
    });
    if (!ok) {
        $("loginMsg").textContent = (data && data.detail) || "登录失败";
        return;
    }
    $("loginMsg").textContent = "登录成功";
    await refreshMe();
    showView("download");
    refreshCookies();
}

// ============ 忘记密码 / 重置密码（邮箱 + 验证码 + 新密码） ============
function showPwdReset() {
    $("loginForm").hidden = true;
    $("registerForm").hidden = true;
    $("verifyCodeForm").hidden = true;
    $("verifyResult").hidden = true;
    $("pwdResetForm").hidden = false;
    $("pwdResetTip").textContent = "请输入注册邮箱，点击发送重置验证码";
    $("pwdResetMsg").textContent = "";
    $("resetEmail").focus();
}

function backToLogin() {
    $("pwdResetForm").hidden = true;
    $("loginForm").hidden = false;
    $("pwdResetMsg").textContent = "";
}

async function sendResetCode() {
    const email = $("resetEmail").value.trim();
    if (!email) { $("pwdResetMsg").textContent = "请输入邮箱"; return; }
    const { ok, data } = await api("/api/auth/forgot-password", {
        method: "POST", body: JSON.stringify({ email }),
    });
    if (ok) {
        $("pwdResetMsg").textContent = "重置验证码已发送，请查收邮箱（5 分钟内有效）";
        startCd($("sendResetCodeBtn"), $("resetCountdown"), data && data.retry_after);
    } else if (data && data.retry_after) {
        // 后端限流：按服务器剩余秒数展示冷却
        $("pwdResetMsg").textContent = "发送过于频繁，请稍后再试";
        startCd($("sendResetCodeBtn"), $("resetCountdown"), data.retry_after);
    } else {
        $("pwdResetMsg").textContent = (data && data.detail) || "发送失败，请稍后再试";
    }
}

async function doResetPassword() {
    const email = $("resetEmail").value.trim();
    const code = $("resetCode").value.trim();
    const newPw = $("resetPw").value;
    const confirm = $("resetPwConfirm").value;
    if (!email || !newPw || !confirm) { $("pwdResetMsg").textContent = "请完整填写邮箱、验证码与新密码"; return; }
    if (newPw !== confirm) { $("pwdResetMsg").textContent = "两次输入的新密码不一致"; return; }
    const { ok, data } = await api("/api/auth/reset-password", {
        method: "POST", body: JSON.stringify({ email, code, new_password: newPw }),
    });
    if (ok) {
        $("loginEmail").value = email;
        backToLogin();
        $("loginMsg").textContent = "密码已重置，请使用新密码登录";
        return;
    }
    $("pwdResetMsg").textContent = (data && data.detail) || "重置失败";
}

async function doRegister() {
    const email = $("regEmail").value.trim();
    const password = $("regPassword").value;
    const confirm = $("regConfirm").value;
    if (!email || !password || !confirm) { $("registerMsg").textContent = "请完整填写邮箱、密码与确认密码"; return; }
    const { ok, data } = await api("/api/auth/register", {
        method: "POST", body: JSON.stringify({ email, password, confirm_password: confirm }),
    });
    if (!ok) {
        $("registerMsg").textContent = (data && data.detail) || "注册失败";
        return;
    }
    state.verifyEmail = email;
    $("registerMsg").textContent = (data && data.message) || "注册成功";
    showVerifyCode(email, (data && data.message) || "注册成功，验证码已发送至您的邮箱",
        data && data.retry_after);
}

// ============ 邮箱验证码（6 位数字） ============
// 重发倒计时：后端计时（retry_after 由服务器下发），前端基于绝对时间戳展示；
// 切换后台/锁屏导致定时器暂停，恢复前台（visibilitychange）时立即重算，始终准确。
let cd = null; // { btn, el, deadline, iv }

function stopCd() {
    if (cd) {
        clearInterval(cd.iv);
        cd.btn.disabled = false;
        cd.el.textContent = "";
        cd = null;
    }
}

function cdTick() {
    if (!cd) return;
    const left = Math.ceil((cd.deadline - Date.now()) / 1000);
    if (left <= 0) { stopCd(); return; }
    cd.el.textContent = `（${left} 秒后可重新发送）`;
}

function startCd(btn, el, seconds) {
    stopCd();
    const secs = Math.floor(Number(seconds) || 0);
    if (secs <= 0) return; // 未提供/已过期：不启动（保持可点击）
    btn.disabled = true;
    cd = { btn, el, deadline: Date.now() + secs * 1000, iv: null };
    cdTick();
    cd.iv = setInterval(cdTick, 1000);
}

// 切回前台立即重算（后台时浏览器会暂停 setInterval，这里兜底保证准确性）
document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") cdTick();
});

function showVerifyCode(email, tip, retryAfter) {
    $("loginForm").hidden = true;
    $("registerForm").hidden = true;
    $("verifyResult").hidden = true;
    $("verifyCodeForm").hidden = false;
    $("verifyCodeTip").textContent = (tip || "验证码已发送至您的邮箱") + "：" + email;
    $("verifyEmail").value = email || "";
    $("verifyCode").value = "";
    $("verifyMsg").textContent = "";
    $("verifyCode").focus();
    startCd($("resendCodeBtn"), $("resendCountdown"), retryAfter);
}

// 登录页进入：手动输入注册邮箱 + 验证码（管理员自举/未验证用户均可使用）
function showVerifyEntry() {
    showVerifyCode(state.verifyEmail, "请输入注册邮箱与您收到的验证码", null);
}

async function submitVerifyCode() {
    const email = ($("verifyEmail").value || "").trim() || state.verifyEmail || $("regEmail").value.trim();
    const code = $("verifyCode").value.trim();
    if (!email) { $("verifyMsg").textContent = "请输入注册邮箱"; return; }
    if (!/^\d{6}$/.test(code)) { $("verifyMsg").textContent = "请输入6位数验证码"; return; }
    const { ok, data } = await api("/api/auth/verify-email", {
        method: "POST", body: JSON.stringify({ email, code }),
    });
    if (ok) {
        stopCd();
        $("verifyCodeForm").hidden = true;
        $("verifyResultMsg").textContent = "✅ " + ((data && data.message) || "邮箱验证成功，现在可以使用邮箱登录");
        $("verifyResult").hidden = false;
        if (data && data.email) {
            $("loginEmail").value = data.email;
            state.verifyEmail = data.email;
        }
        return;
    }
    $("verifyMsg").textContent = (data && data.detail) || "验证码无效或已过期";
}

async function resendCode() {
    const email = ($("verifyEmail").value || "").trim() || state.verifyEmail || $("regEmail").value.trim();
    if (!email) { $("verifyMsg").textContent = "请输入注册邮箱后再重发"; return; }
    const { ok, data } = await api("/api/auth/resend-verification", {
        method: "POST", body: JSON.stringify({ email }),
    });
    if (ok) {
        $("verifyMsg").textContent = "验证码已重新发送，请输入最新的验证码";
        $("verifyCode").value = "";
        startCd($("resendCodeBtn"), $("resendCountdown"), data && data.retry_after);
    } else if (data && data.retry_after) {
        $("verifyMsg").textContent = "发送过于频繁，请稍后再试";
        startCd($("resendCodeBtn"), $("resendCountdown"), data.retry_after);
    } else {
        $("verifyMsg").textContent = (data && data.detail) || "重发失败：请稍后再试";
    }
}

async function logout() {
    await api("/api/auth/logout", { method: "POST", body: "{}" });
    state.currentUser = null;
    $("loginMsg").textContent = "";
    switchAuthTab("login");
    renderNav();
    showView("download");
}

// ============ 用户中心 ============
async function loadUserCenter() {
    const { ok, data } = await api("/api/auth/me");
    if (!ok) { showView("auth"); return; }
    const mem = (await api("/api/membership")).data || {};
    const q = mem.quota || {};
    $("userInfo").innerHTML = `
        <div class="info-row"><span>邮箱</span><b>${escapeHtml(data.email)}</b></div>
        <div class="info-row"><span>邮箱验证</span><b>${data.verified ? "✅ 已验证" : "⏳ 未验证（验证后可开通会员与使用下载）"}</b></div>
        <div class="info-row"><span>会员</span><b>${escapeHtml(mem.plan_name || "免费用户")}${mem.expires_at ? "（到期 " + fmtTime(mem.expires_at) + "）" : ""}</b></div>
        <div class="info-row"><span>下载额度（总）</span><b>${q.used_total ?? 0} / ${mem.download_limit === -1 ? "∞" : mem.download_limit}</b></div>
        <div class="info-row"><span>今日使用</span><b>${q.used_today ?? 0}${mem.daily_download_limit === -1 ? "" : " / " + mem.daily_download_limit}</b></div>
        <div class="info-row"><span>进行中任务</span><b>${q.active ?? 0} / ${mem.max_concurrent ?? 0}</b></div>
        <div class="info-row"><span>注册时间</span><b>${fmtTime(data.created_at)}</b></div>
        <div class="info-row"><span>最近登录</span><b>${data.last_login_at ? fmtTime(data.last_login_at) : "—"}</b></div>`;
    // 未验证邮箱：显示「邮箱验证」面板（用户中心内完成验证）
    const vs = $("verifySection");
    if (data.verified) {
        vs.hidden = true;
    } else {
        vs.hidden = false;
        $("verifyTip").textContent = "验证码将发送至 " + data.email + "，验证后即可使用会员与下载功能。";
    }
    loadOrders();
}

// ============ 用户中心：邮箱验证（发送验证码 → 输入 → 完成） ============
async function sendCenterVerify() {
    if (!state.currentUser) { $("centerVerifyMsg").textContent = "请先登录"; return; }
    const { ok, data } = await api("/api/auth/resend-verification", {
        method: "POST", body: JSON.stringify({ email: state.currentUser.email }),
    });
    if (ok) {
        $("centerVerifyMsg").textContent = "验证码已发送至您的邮箱，请查收（5 分钟内有效）";
        startCd($("sendCenterVerifyBtn"), $("centerVerifyCountdown"), data && data.retry_after);
    } else if (data && data.retry_after) {
        $("centerVerifyMsg").textContent = "发送过于频繁，请稍后再试";
        startCd($("sendCenterVerifyBtn"), $("centerVerifyCountdown"), data.retry_after);
    } else {
        $("centerVerifyMsg").textContent = (data && data.detail) || "发送失败，请稍后再试";
    }
}

async function submitCenterVerify() {
    if (!state.currentUser) { $("centerVerifyMsg").textContent = "请先登录"; return; }
    const code = $("centerVerifyCode").value.trim();
    if (!/^\d{6}$/.test(code)) { $("centerVerifyMsg").textContent = "请输入6位数验证码"; return; }
    const { ok, data } = await api("/api/auth/verify-email", {
        method: "POST", body: JSON.stringify({ email: state.currentUser.email, code }),
    });
    if (ok) {
        $("centerVerifyMsg").textContent = "✅ 邮箱验证成功！";
        $("centerVerifyCode").value = "";
        stopCd();
        await refreshMe();
        loadUserCenter();
        return;
    }
    $("centerVerifyMsg").textContent = (data && data.detail) || "验证码无效或已过期";
}

// 修改密码：邮箱验证码 + 新密码（无需旧密码）
async function sendChangeCode() {
    if (!state.currentUser) { $("userMsg").textContent = "请先登录"; return; }
    const { ok, data } = await api("/api/auth/forgot-password", {
        method: "POST", body: JSON.stringify({ email: state.currentUser.email }),
    });
    if (ok) {
        $("userMsg").textContent = "验证码已发送至当前邮箱，请查收（5 分钟内有效）";
        startCd($("sendChangeCodeBtn"), $("changeCodeCountdown"), data && data.retry_after);
    } else if (data && data.retry_after) {
        $("userMsg").textContent = "发送过于频繁，请稍后再试";
        startCd($("sendChangeCodeBtn"), $("changeCodeCountdown"), data.retry_after);
    } else {
        $("userMsg").textContent = (data && data.detail) || "发送失败，请稍后再试";
    }
}

async function changePassword() {
    const code = $("changeCode").value.trim();
    const newPw = $("newPw").value;
    if (!code || !newPw) { $("userMsg").textContent = "请填写验证码与新密码"; return; }
    const { ok, data } = await api("/api/auth/change-password", {
        method: "POST", body: JSON.stringify({ code, new_password: newPw }),
    });
    $("userMsg").textContent = ok ? "密码已修改，请重新登录" : ((data && data.detail) || "修改失败");
    if (ok) {
        state.currentUser = null;
        renderNav();
        switchAuthTab("login");
    }
}

async function sendTestEmail() {
    const btn = $("emailTestBtn");
    const msg = $("emailTestMsg");
    if (btn.disabled) return;
    btn.disabled = true;
    msg.textContent = "发送中...";
    const { ok, data } = await api("/api/email/test", { method: "POST", body: "{}" });
    msg.textContent = ok
        ? "✅ " + ((data && data.message) || "测试邮件已发送，请检查收件箱")
        : "❌ " + ((data && data.detail) || "发送失败");
    setTimeout(() => { btn.disabled = false; }, 2000);
}

async function loadOrders() {
    const { ok, data } = await api("/api/orders");
    const wrap = $("orderList");
    if (!ok || !data || !data.orders || data.orders.length === 0) {
        wrap.innerHTML = '<p class="field-hint">暂无订单</p>';
        return;
    }
    wrap.innerHTML = data.orders.map((o) => `
        <div class="order-row">
            <div><b>${escapeHtml(o.subject)}</b><span class="order-no">${escapeHtml(o.order_no)}</span></div>
            <div class="order-actions">
                <span class="order-status s-${o.status}">${o.status}</span>
                <span class="order-amount">￥${o.amount}</span>
                ${o.status === "pending" ? `<button class="text-btn" data-close="${o.order_no}">关闭</button>` : ""}
                ${o.status === "pending" ? `<button class="text-btn" data-pay="${o.order_no}">去支付</button>` : ""}
            </div>
        </div>`).join("");
    wrap.querySelectorAll("[data-close]").forEach((b) =>
        b.addEventListener("click", async () => {
            await api("/api/orders/" + b.dataset.close + "/close", { method: "POST", body: "{}" });
            loadOrders();
        }));
    wrap.querySelectorAll("[data-pay]").forEach((b) =>
        b.addEventListener("click", () => simulatePay(b.dataset.pay)));
}

// ============ 会员套餐 ============
async function loadPlans() {
    const { ok, data } = await api("/api/plans");
    const wrap = $("planList");
    if (!ok || !data || !data.plans || data.plans.length === 0) {
        wrap.innerHTML = '<p class="field-hint">暂无套餐</p>';
        return;
    }
    wrap.innerHTML = data.plans.map((p) => `
        <div class="plan-card">
            <div class="plan-name">${escapeHtml(p.name)}</div>
            <div class="plan-desc">${escapeHtml(p.description || "")}</div>
            <div class="plan-price">￥${p.price}<span>/ ${p.duration_days} 天</span></div>
            <div class="plan-benefits">
                下载：${p.download_limit === -1 ? "不限" : p.download_limit} 次 /
                每日：${p.daily_download_limit === -1 ? "不限" : p.daily_download_limit} 次 /
                并发：${p.max_concurrent_tasks} /
                画质：${(p.allowed_quality || []).join("、") || "720p"}
            </div>
            <button class="btn-primary plan-buy" data-plan="${p.id}">购买</button>
        </div>`).join("");
    wrap.querySelectorAll(".plan-buy").forEach((b) =>
        b.addEventListener("click", () => buyPlan(b.dataset.plan)));
}

async function buyPlan(planId) {
    if (!state.currentUser) { showView("auth"); return; }
    const { ok, data } = await api("/api/orders", {
        method: "POST", body: JSON.stringify({ plan_id: parseInt(planId, 10) }),
    });
    if (!ok) {
        $("planMsg").textContent = (data && data.detail) || "下单失败";
        return;
    }
    $("planMsg").textContent = "订单已创建：" + data.order_no;
    simulatePay(data.order_no);
}

// ============ 模拟支付（仅开发环境；生产环境后端会返回 404）============
async function simulatePay(orderNo) {
    const { ok, data } = await api("/api/orders/" + orderNo + "/simulate-pay", { method: "POST", body: "{}" });
    if (ok && data) {
        alert((data.notice || "模拟支付成功") + "\n订单：" + orderNo + "\n状态：" + (data.order && data.order.status));
        loadUserCenter();
        loadPlans();
    } else {
        alert("模拟支付不可用（仅开发环境）或订单状态异常：" + ((data && data.detail) || ""));
    }
}

// ============ 下载流程（保留原有逻辑 + 登录校验）============
async function startDownload() {
    if (!state.currentUser) {
        // 顶部提示弹窗：未登录直接下载 → 提示后引导登录
        showToast("请登录后重试", "error");
        showView("auth");
        $("loginMsg").textContent = "";
        return;
    }
    const url = $("url").value.trim();
    const cookie = $("cookie").value.trim();
    if (!url) { showToast("请输入视频链接", "error"); return; }
    setButtonLoading(true);
    hideMessage();
    const { ok, data } = await api("/api/download", {
        method: "POST", body: JSON.stringify({ url, cookie }),
    });
    setButtonLoading(false);
    if (!ok) {
        const code = data && data.code;
        if (code === "quota_exceeded") {
            // 下载额度耗尽：顶部弹窗提示 + 自动弹出会员套餐页面
            showToast("下载额度已用尽，请开通会员", "error");
            showView("plans");
            loadPlans();
            return;
        }
        if (code === "concurrency_exceeded") {
            showToast("同时进行的下载任务数已达上限，请稍后再试", "error");
            return;
        }
        if (data && data.detail && data.detail.includes("验证")) {
            showToast("账户尚未完成邮箱验证，请在用户中心完成验证后重试", "error");
            return;
        }
        showToast((data && data.detail) || "提交失败", "error");
        return;
    }
    state.currentTaskId = data.task_id;
    state.autoDownloaded = false;
    state.taskActive = true;
    $("inputPanel").hidden = true;
    $("progressPanel").hidden = false;
    $("actionsArea").style.display = "none";
    resetProgressDisplay(data.platform);
    startPolling();
}

function setButtonLoading(loading) {
    const btn = $("startBtn");
    btn.disabled = loading;
    btn.querySelector(".btn-text").textContent = loading ? "处理中..." : "开始下载";
}

function resetProgressDisplay(platformName) {
    $("progressBar").style.width = "0%";
    $("progressPercent").textContent = "0%";
    $("title").textContent = "—";
    $("platform").textContent = platformName || "—";
    $("videoFmt").textContent = "—";
    $("audioFmt").textContent = "—";
    $("stage").textContent = "排队中...";
}

function startPolling() {
    if (state.pollTimer) clearInterval(state.pollTimer);
    state.pollTimer = setInterval(pollStatus, 1500);
}

async function pollStatus() {
    if (!state.currentTaskId) return;
    const { ok, data } = await api("/api/status/" + state.currentTaskId);
    if (!ok) return;
    const pct = Math.round(data.progress || 0);
    $("progressBar").style.width = pct + "%";
    $("progressPercent").textContent = pct + "%";
    $("stage").textContent = data.stage || "—";
    if (data.title) $("title").textContent = data.title;
    if (data.platform) $("platform").textContent = data.platform;
    if (data.video_format) {
        const vf = data.video_format;
        $("videoFmt").textContent = `${vf.resolution} @ ${vf.fps || "?"}fps`;
    }
    if (data.audio_format) {
        const af = data.audio_format;
        $("audioFmt").textContent = `${af.abr || "?"}kbps`;
    }
    if (data.status === "completed") {
        clearInterval(state.pollTimer);
        state.pollTimer = null;
        state.taskActive = false;
        const sizeText = data.filesize ? `（${(data.filesize / 1048576).toFixed(1)} MB）` : "";
        $("stage").textContent = `下载完成${sizeText}，等待浏览器拉取`;
        showMessage("下载完成", "success");
        const link = $("downloadLink");
        link.href = `/api/file/${state.currentTaskId}`;
        $("actionsArea").style.display = "flex";
        if (!state.autoDownloaded) {
            state.autoDownloaded = true;
            setTimeout(() => link.click(), 500);
        }
    } else if (data.status === "failed") {
        clearInterval(state.pollTimer);
        state.pollTimer = null;
        state.taskActive = false;
        showMessage("下载失败：" + (data.error || "未知错误"), "error");
        $("actionsArea").style.display = "none";
    }
}

function resetUI() {
    if (state.taskActive) {
        if (!confirm("当前任务仍在下载中，确定取消并新建任务吗？")) return;
    }
    if (state.pollTimer) { clearInterval(state.pollTimer); state.pollTimer = null; }
    if (state.currentTaskId) {
        api("/api/task/" + state.currentTaskId, { method: "DELETE" }).catch(() => {});
    }
    state.currentTaskId = null;
    state.autoDownloaded = false;
    state.taskActive = false;
    $("url").value = "";
    $("cookie").value = "";
    state.editLock = null;
    $("cookieManual").style.display = "none";
    setCookieStatus("");
    $("inputPanel").hidden = false;
    $("progressPanel").hidden = true;
    $("actionsArea").style.display = "none";
    $("downloadLink").removeAttribute("href");
    hideMessage();
    setButtonLoading(false);
    refreshCookies();
}

// ============ 任务列表 ============
async function loadTaskList() {
    const { ok, data } = await api("/api/tasks");
    const wrap = $("taskList");
    if (!ok || !data || !data.tasks || data.tasks.length === 0) {
        wrap.innerHTML = '<p class="field-hint">暂无任务</p>';
        return;
    }
    wrap.innerHTML = data.tasks.map((t) => `
        <div class="task-row">
            <div class="task-title">${escapeHtml(t.title || "(未解析)")}</div>
            <div class="task-meta">${escapeHtml(t.platform || "")} · ${t.status} · ${Math.round(t.progress || 0)}% · ${fmtTime(t.created_at)}</div>
            <div class="task-actions">
                ${t.status === "completed" && t.filesize > 0 ? `<a class="text-btn" href="/api/file/${t.task_id}">下载文件</a>` : ""}
                ${t.status !== "completed" ? `<button class="text-btn text-btn-danger" data-del="${t.task_id}">取消</button>` : ""}
            </div>
        </div>`).join("");
    wrap.querySelectorAll("[data-del]").forEach((b) =>
        b.addEventListener("click", async () => {
            await api("/api/task/" + b.dataset.del, { method: "DELETE" });
            loadTaskList();
        }));
}

// ============ Cookie 管理（脱敏展示；只增不读明文）============
async function refreshCookies() {
    if (!state.currentUser) return;
    const { ok, data } = await api("/api/cookies");
    if (!ok) return;
    state.savedCookies = {};
    for (const [key, info] of Object.entries(data)) {
        if (info.has_cookie) {
            state.savedCookies[key] = { name: info.name || getPlatformName(key), mask: info.mask || "****" };
        }
    }
    renderChips();
}

async function loadPlatforms() {
    const { ok, data } = await api("/api/platforms");
    state.platforms = ok ? (data.platforms || []).map((p) => ({ key: p.key, name: p.name }))
        : PLATFORM_ORDER.map((k) => ({ key: k, name: getPlatformName(k) }));
    state.platforms.sort((a, b) => PLATFORM_ORDER.indexOf(a.key) - PLATFORM_ORDER.indexOf(b.key));
    const sel = $("cookiePlatformSelect");
    sel.innerHTML = "";
    for (const p of state.platforms) {
        const opt = document.createElement("option");
        opt.value = p.key;
        opt.textContent = p.name;
        sel.appendChild(opt);
    }
}

async function handleCookieInput() {
    const val = $("cookie").value.trim();
    if (!val) {
        if (state.editLock) setCookieStatus("");
        state.editLock = null;
        $("cookieManual").style.display = "none";
        return;
    }
    if (state.editLock) { await saveCookie(state.editLock, val, "已更新"); return; }
    const { ok, data } = await api("/api/cookie/detect", {
        method: "POST", body: JSON.stringify({ cookie: val }),
    });
    if (!ok || !data.ok || !data.platform) {
        $("cookieManual").style.display = "flex";
        setCookieStatus("未识别所属平台，请手动选择后保存", "");
        return;
    }
    await saveCookie(data.platform, val, "已识别并保存");
    $("cookie").value = "";
    $("cookieManual").style.display = "none";
}

async function saveCookie(platform, cookie, verb) {
    const { ok } = await api("/api/cookie/" + platform, {
        method: "POST", body: JSON.stringify({ cookie }),
    });
    if (!ok) { setCookieStatus("保存失败", "error"); return; }
    state.savedCookies[platform] = { name: getPlatformName(platform), mask: cookie.slice(0, 2) + "****" };
    renderChips();
    setCookieStatus(`${verb}：${getPlatformName(platform)}`, "saved");
}

async function saveCookieManually() {
    const val = $("cookie").value.trim();
    if (!val) return;
    await saveCookie($("cookiePlatformSelect").value, val, "已保存");
    $("cookie").value = "";
    $("cookieManual").style.display = "none";
}

function renderChips() {
    const wrap = $("cookieChips");
    const keys = PLATFORM_ORDER.filter((k) => state.savedCookies[k])
        .concat(Object.keys(state.savedCookies).filter((k) => !PLATFORM_ORDER.includes(k)));
    wrap.innerHTML = "";
    for (const key of keys) {
        const info = state.savedCookies[key];
        const chip = document.createElement("div");
        chip.className = "cookie-chip";
        const check = document.createElement("input");
        check.type = "checkbox";
        check.className = "chip-check";
        check.dataset.platform = key;
        check.addEventListener("change", syncCheckAllState);
        chip.appendChild(check);
        const name = document.createElement("span");
        name.className = "chip-name";
        name.textContent = info.name + " · " + info.mask;
        name.title = "点击重新粘贴覆盖";
        name.addEventListener("click", () => enterEditMode(key));
        chip.appendChild(name);
        const del = document.createElement("button");
        del.type = "button";
        del.className = "chip-del";
        del.textContent = "✕";
        del.title = "删除该平台 Cookie";
        del.addEventListener("click", (e) => { e.stopPropagation(); deleteCookie(key); });
        chip.appendChild(del);
        wrap.appendChild(chip);
    }
    $("cookieSaved").style.display = keys.length > 0 ? "block" : "none";
    if (keys.length === 0) $("checkAll").checked = false;
}

function enterEditMode(key) {
    state.editLock = key;
    $("cookie").value = "";
    $("cookie").placeholder = "粘贴新的 Cookie 覆盖 " + getPlatformName(key) + " 的旧值";
    $("cookieManual").style.display = "none";
    setCookieStatus(`正在编辑：${getPlatformName(key)}（粘贴新值后自动覆盖）`, "");
    $("cookie").focus();
}

async function deleteCookie(key) {
    const name = getPlatformName(key);
    if (!confirm(`确定删除 ${name} 的 Cookie？`)) return;
    await api("/api/cookie/" + key, { method: "DELETE" });
    delete state.savedCookies[key];
    if (state.editLock === key) { state.editLock = null; $("cookie").value = ""; }
    renderChips();
    setCookieStatus(`已删除：${name}`, "");
}

function toggleCheckAll() {
    const checked = $("checkAll").checked;
    document.querySelectorAll(".chip-check").forEach((c) => { c.checked = checked; });
}

function syncCheckAllState() {
    const checks = document.querySelectorAll(".chip-check");
    $("checkAll").checked = checks.length > 0 && [...checks].every((c) => c.checked);
}

async function deleteSelectedCookies() {
    const selected = [...document.querySelectorAll(".chip-check:checked")].map((c) => c.dataset.platform);
    if (selected.length === 0) { setCookieStatus("请先勾选要删除的平台", ""); return; }
    const allChecked = $("checkAll").checked;
    if (!confirm(allChecked ? "确定删除所有已保存的 Cookie？" : `确定删除选中的 ${selected.length} 个平台 Cookie？`)) return;
    await api("/api/cookies/delete", { method: "POST", body: JSON.stringify({ platforms: selected }) });
    for (const key of selected) delete state.savedCookies[key];
    renderChips();
    setCookieStatus("已删除", "");
}

function setCookieStatus(text, cls) {
    const el = $("cookieStatus");
    el.textContent = text;
    el.className = "cookie-status" + (cls ? " " + cls : "");
}

// ============ 管理员后台 ============
async function loadAdmin(tab) {
    state.adminTab = tab;
    document.querySelectorAll(".admin-tab").forEach((b) =>
        b.classList.toggle("active", b.dataset.atab === tab));
    const c = $("admin-content");
    const { ok, data } = await api("/api/admin/" + tab);
    if (!ok) {
        c.innerHTML = '<p class="field-hint">' + escapeHtml((data && data.detail) || "无权访问") + "</p>";
        return;
    }
    if (tab === "users") { c.innerHTML = renderAdminUsers(data.users); }
    else if (tab === "orders") { c.innerHTML = renderAdminOrders(data.orders); }
    else if (tab === "plans") { c.innerHTML = renderAdminPlans(data.plans); }
    else if (tab === "tasks") { c.innerHTML = renderAdminTasks(data.tasks); }
    else if (tab === "payments") { c.innerHTML = renderAdminPayments(data.events); }
    else if (tab === "audit") { c.innerHTML = renderAdminAudit(data.logs); }
    else if (tab === "email") { c.innerHTML = renderAdminEmailLogs(data.logs); }
    bindAdminActions();
}

// 邮件发送日志（诊断「收不到验证码」：展示每次发送的结果与服务器错误）
function renderAdminEmailLogs(logs) {
    if (!logs || !logs.length) return '<p class="field-hint">暂无发送记录（先注册/重发一次验证码再查看）</p>';
    return `<table class="admin-table"><thead><tr><th>时间</th><th>收件人指纹</th><th>用途</th><th>结果</th><th>服务器返回（脱敏）</th></tr></thead><tbody>` +
        logs.map((l) => `
        <tr><td>${fmtTime(l.created_at)}</td><td>${escapeHtml(l.email_hash || "—")}</td><td>${escapeHtml(l.purpose)}</td>
        <td>${l.ok ? "✅ 成功" : "❌ 失败"}</td><td>${escapeHtml(l.err_msg || "—")}</td></tr>`).join("") +
        "</tbody></table>";
}

function renderAdminUsers(users) {
    if (!users || !users.length) return '<p class="field-hint">暂无数据</p>';
    return `<table class="admin-table"><thead><tr><th>ID</th><th>邮箱</th><th>验证</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody>` +
        users.map((u) => `
        <tr>
            <td>${u.id}</td><td>${escapeHtml(u.email)}</td>
            <td>${u.verified ? "✅" : "❌"}</td><td>${u.role}</td><td>${u.status}</td>
            <td>
                ${u.status !== "disabled" ? `<button class="text-btn" data-act="disable" data-id="${u.id}">禁用</button>` : `<button class="text-btn" data-act="enable" data-id="${u.id}">解禁</button>`}
                ${!u.verified ? `<button class="text-btn" data-act="verify" data-id="${u.id}">[dev]标记验证</button>` : ""}
            </td>
        </tr>`).join("") + "</tbody></table>";
}

function renderAdminOrders(orders) {
    if (!orders || !orders.length) return '<p class="field-hint">暂无数据</p>';
    return `<table class="admin-table"><thead><tr><th>订单号</th><th>金额</th><th>状态</th><th>渠道</th><th>操作</th></tr></thead><tbody>` +
        orders.map((o) => `
        <tr><td>${escapeHtml(o.order_no)}</td><td>￥${o.amount}</td><td>${o.status}</td><td>${o.provider}</td>
        <td>${o.status === "pending" ? `<button class="text-btn" data-act="markpaid" data-no="${o.order_no}">标记已支付</button>` : ""}
            ${o.status === "paid" ? `<button class="text-btn" data-act="refund" data-no="${o.order_no}">标记退款</button>` : ""}</td></tr>`).join("") +
        "</tbody></table>";
}

function renderAdminPlans(plans) {
    if (!plans || !plans.length) return '<p class="field-hint">暂无数据</p>';
    return `<table class="admin-table"><thead><tr><th>ID</th><th>名称</th><th>价格(分)</th><th>时长</th><th>状态</th><th>操作</th></tr></thead><tbody>` +
        plans.map((p) => `
        <tr><td>${p.id}</td><td>${escapeHtml(p.name)}</td><td>${p.price_cents}</td><td>${p.duration_days}天</td>
        <td>${p.enabled ? "启用" : "停用"}</td>
        <td><button class="text-btn" data-act="toggle" data-id="${p.id}" data-on="${p.enabled}">${p.enabled ? "停用" : "启用"}</button></td></tr>`).join("") +
        "</tbody></table>" +
        `<div class="panel-sub"><h3>新建套餐</h3>
         <div class="plan-form">
           <input id="np-name" class="field-input" placeholder="名称">
           <input id="np-price" class="field-input" placeholder="价格(分)" type="number">
           <input id="np-days" class="field-input" placeholder="时长(天)" type="number">
           <input id="np-download" class="field-input" placeholder="下载次数(-1不限)" type="number">
           <input id="np-daily" class="field-input" placeholder="每日次数(-1不限)" type="number">
           <input id="np-concurrent" class="field-input" placeholder="并发数" type="number">
           <input id="np-quality" class="field-input" placeholder="画质 如 1080p,4k">
           <button class="btn-secondary" id="np-create">创建</button>
         </div></div>`;
}

function renderAdminTasks(tasks) {
    if (!tasks || !tasks.length) return '<p class="field-hint">暂无数据</p>';
    return `<table class="admin-table"><thead><tr><th>任务</th><th>用户</th><th>平台</th><th>状态</th><th>文件名</th></tr></thead><tbody>` +
        tasks.map((t) => `
        <tr><td>${escapeHtml(t.title || t.task_id)}</td><td>${t.user_id}</td><td>${escapeHtml(t.platform || "")}</td>
        <td>${t.status}</td><td>${escapeHtml(t.filename || "—")}</td></tr>`).join("") + "</tbody></table>";
}

function renderAdminPayments(events) {
    if (!events || !events.length) return '<p class="field-hint">暂无数据</p>';
    return `<table class="admin-table"><thead><tr><th>ID</th><th>渠道</th><th>订单</th><th>已处理</th><th>载荷(脱敏)</th></tr></thead><tbody>` +
        events.map((e) => `
        <tr><td>${e.id}</td><td>${e.provider}</td><td>${escapeHtml(e.order_no)}</td><td>${e.processed ? "✅" : "❌"}</td>
        <td>${escapeHtml(e.payload || "—")}</td></tr>`).join("") + "</tbody></table>";
}

function renderAdminAudit(logs) {
    if (!logs || !logs.length) return '<p class="field-hint">暂无数据</p>';
    return `<table class="admin-table"><thead><tr><th>ID</th><th>操作</th><th>详情</th><th>时间</th></tr></thead><tbody>` +
        logs.map((l) => `
        <tr><td>${l.id}</td><td>${escapeHtml(l.action)}</td><td>${escapeHtml(l.detail || "")}</td><td>${fmtTime(l.created_at)}</td></tr>`).join("") +
        "</tbody></table>";
}

async function bindAdminActions() {
    const c = $("admin-content");
    c.querySelectorAll("[data-act]").forEach((b) => b.addEventListener("click", async () => {
        const act = b.dataset.act;
        if (act === "disable" || act === "enable") {
            await api("/api/admin/users/" + b.dataset.id + "/status", {
                method: "POST", body: JSON.stringify({ status: act === "disable" ? "disabled" : "active" }),
            });
        } else if (act === "verify") {
            await api("/api/admin/users/" + b.dataset.id + "/verify", { method: "POST", body: "{}" });
        } else if (act === "markpaid") {
            await api("/api/admin/orders/" + b.dataset.no + "/mark-paid", { method: "POST", body: "{}" });
        } else if (act === "refund") {
            await api("/api/admin/orders/" + b.dataset.no + "/refund", { method: "POST", body: "{}" });
        } else if (act === "toggle") {
            await api("/api/admin/plans/" + b.dataset.id + "/toggle", {
                method: "POST", body: JSON.stringify({ enabled: b.dataset.on !== "true" }),
            });
        }
        loadAdmin(state.adminTab);
    }));
    const createBtn = $("np-create");
    if (createBtn) {
        createBtn.addEventListener("click", async () => {
            const body = {
                name: $("np-name").value.trim(),
                price_cents: parseInt($("np-price").value || "0", 10),
                duration_days: parseInt($("np-days").value || "0", 10),
                download_limit: parseInt($("np-download").value || "-1", 10),
                daily_download_limit: parseInt($("np-daily").value || "-1", 10),
                max_concurrent_tasks: parseInt($("np-concurrent").value || "1", 10),
                allowed_quality: $("np-quality").value.trim(),
            };
            await api("/api/admin/plans", { method: "POST", body: JSON.stringify(body) });
            loadAdmin("plans");
        });
    }
}

// ============ 工具 ============
function showMessage(msg, type) {
    const el = $("statusMsg");
    el.textContent = msg;
    el.className = "message show " + (type || "");
}

function hideMessage() { $("statusMsg").className = "message"; }

// 页面顶部提示弹窗（toast）：info（默认）/ error / success；3 秒后自动消失
let toastTimer = null;
function showToast(msg, type) {
    const el = $("toast");
    if (!el) return;
    if (toastTimer) clearTimeout(toastTimer);
    el.textContent = msg;
    el.className = "toast show" + (type === "error" ? " error" : type === "success" ? " success" : "");
    el.hidden = false;
    toastTimer = setTimeout(() => {
        el.className = "toast";
        el.hidden = true;
    }, 3000);
}

function debounce(fn, delay) {
    let timer;
    return (...args) => {
        clearTimeout(timer);
        timer = setTimeout(() => fn(...args), delay);
    };
}

function escapeHtml(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, (ch) => ({
        "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[ch]));
}

function fmtTime(ts) {
    if (!ts) return "—";
    return new Date(ts * 1000).toLocaleString();
}

// ============ 事件绑定 ============
function bindEvents() {
    $("startBtn").addEventListener("click", startDownload);
    $("resetBtn").addEventListener("click", resetUI);
    $("url").addEventListener("keydown", (e) => { if (e.key === "Enter") startDownload(); });
    $("cookie").addEventListener("input", debounce(handleCookieInput, 800));
    $("cookieSaveBtn").addEventListener("click", saveCookieManually);
    $("checkAll").addEventListener("change", toggleCheckAll);
    $("deleteSelectedBtn").addEventListener("click", deleteSelectedCookies);

    $("tabLogin").addEventListener("click", () => switchAuthTab("login"));
    $("tabRegister").addEventListener("click", () => switchAuthTab("register"));
    $("loginBtn").addEventListener("click", doLogin);
    $("registerBtn").addEventListener("click", doRegister);
    $("loginPassword").addEventListener("keydown", (e) => { if (e.key === "Enter") doLogin(); });
    $("verifyCodeBtn").addEventListener("click", submitVerifyCode);
    $("verifyCode").addEventListener("keydown", (e) => { if (e.key === "Enter") submitVerifyCode(); });
    $("resendCodeBtn").addEventListener("click", resendCode);
    $("gotoVerifyBtn").addEventListener("click", showVerifyEntry);
    $("forgotPwdBtn").addEventListener("click", showPwdReset);
    $("sendResetCodeBtn").addEventListener("click", sendResetCode);
    $("resetPwdBtn").addEventListener("click", doResetPassword);
    $("backToLoginBtn").addEventListener("click", backToLogin);
    $("gotoLoginBtn").addEventListener("click", () => switchAuthTab("login"));
    $("changePwBtn").addEventListener("click", changePassword);
    $("sendChangeCodeBtn").addEventListener("click", sendChangeCode);
    $("sendCenterVerifyBtn").addEventListener("click", sendCenterVerify);
    $("centerVerifyBtn").addEventListener("click", submitCenterVerify);
    $("emailTestBtn").addEventListener("click", sendTestEmail);

    document.querySelectorAll(".admin-tab").forEach((b) =>
        b.addEventListener("click", () => loadAdmin(b.dataset.atab)));
}

// ============ 初始化 ============
async function init() {
    bindEvents();
    await loadPlatforms();
    await refreshMe();
    showView("download");
    if (state.currentUser) refreshCookies();
}

init();

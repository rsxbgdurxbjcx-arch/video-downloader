// Package email 通过 Spug 推送助手（push.spug.cc）发送邮件验证码。
// 开发环境支持将验证码输出到控制台；生产环境必须配置 SPUG_MAIL_*。
// 发送方式（官方文档 https://push.spug.cc/guide/mail）：
//
//	POST https://push.spug.cc/mail/<TEMPLATE_CODE>
//	{"to":"user@example.com","scene":"登录验证","code":"153146","minute":"5"}
//
// 模板编码（TEMPLATE_CODE）是调用凭证，禁止写入日志；验证码本体同样禁止写入日志。
package email

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"video-downloader/internal/config"
)

// mailAPITimeout 单次发送超时。
const mailAPITimeout = 15 * time.Second

// Sender Spug 推送助手邮件发送器。
type Sender struct {
	cfg    *config.Config
	client *http.Client
}

// NewSender 创建邮件发送器。
func NewSender(cfg *config.Config) *Sender {
	return &Sender{cfg: cfg, client: &http.Client{Timeout: mailAPITimeout}}
}

// Enabled Spug 邮件推送是否已配置（SPUG_MAIL_ENABLED=true 且已设置模板编码）。
func (s *Sender) Enabled() bool {
	return s.cfg.MailEnabled && s.cfg.MailAPIBase != "" && s.cfg.MailTemplateCode != ""
}

// SendVerifyCode 使用 Spug 官方模板向 to 发送验证码邮件。
// scene 为验证场景（≤12 字符，不得包含链接/域名）；code 为 4-6 位数字或字母；
// expireMinutes 为验证码有效期（分钟，支持中文数字；本项目使用数字）。
// 注意：任何日志输出都不得包含验证码与模板编码。
func (s *Sender) SendVerifyCode(to, scene, code string, expireMinutes int) error {
	return s.send(to, scene, code, expireMinutes)
}

// SendTest 发送测试邮件（验证 Spug 配置、模板编码、IP 白名单与邮件余额是否就绪）。
// 测试邮件走与真实验证码相同的官方模板与链路，正文携带随机 6 位测试验证码。
func (s *Sender) SendTest(to string) error {
	code, err := GenerateTestCode()
	if err != nil {
		return err
	}
	return s.send(to, "邮件推送测试", code, 5)
}

// send 调用 POST /mail/<TEMPLATE_CODE>（官方文档请求参数：to/scene/code/minute）。
func (s *Sender) send(to, scene, code string, expireMinutes int) error {
	if !s.Enabled() {
		if s.cfg.IsDevelopment() {
			log.Printf("[email][dev] Spug 邮件推送未配置，验证码仅输出到服务控制台（未发送真实邮件）")
			return nil
		}
		return fmt.Errorf("Spug 邮件推送未配置（SPUG_MAIL_ENABLED=false 或缺少模板编码）")
	}
	if to == "" || code == "" {
		return fmt.Errorf("收件人与验证码不能为空")
	}
	if expireMinutes <= 0 {
		expireMinutes = 5
	}

	payload, err := json.Marshal(map[string]string{
		"to":     to,
		"scene":  scene,
		"code":   code,
		"minute": strconv.Itoa(expireMinutes),
	})
	if err != nil {
		return fmt.Errorf("构造 Spug 请求失败: %w", err)
	}

	apiURL := strings.TrimRight(s.cfg.MailAPIBase, "/") + "/mail/" + s.cfg.MailTemplateCode
	ctx, cancel := context.WithTimeout(context.Background(), mailAPITimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("构造 Spug 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "video-downloader-mail-spug")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("Spug 邮件接口连接失败: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return fmt.Errorf("Spug 邮件接口响应读取失败: %w", err)
	}

	// 响应格式（官方文档）：{"code":200,"msg":"请求成功","request_id":"..."}
	var out struct {
		Code      int    `json:"code"`
		Msg       string `json:"msg"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return fmt.Errorf("Spug 邮件接口响应解析失败: %w", err)
	}
	if out.Code != 200 {
		msg := strings.TrimSpace(out.Msg)
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("Spug 邮件发送被拒绝: %s", msg)
	}
	return nil
}

// GenerateTestCode 生成 6 位随机数字测试验证码（仅测试邮件使用）。
func GenerateTestCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	n := uint32(0)
	for _, by := range b {
		n = n<<8 | uint32(by)
	}
	return fmt.Sprintf("%06d", n%1000000), nil
}

// Package redisx 提供极简 Redis 客户端（纯标准库实现，RESP2 协议）。
// 仅实现本系统邮件限流所需的少量命令（PING / SET NX EX / INCR / EXPIRE / GET / DEL），
// 每次操作建立短连接，简单可靠；不引入第三方依赖（便于无外网/受限环境构建）。
// 连接失败或未配置时由调用方决定降级策略（见 internal/email.RateLimiter）。
package redisx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrUnavailable 表示 Redis 不可达或响应异常（调用方可据此降级）。
var ErrUnavailable = errors.New("redis 不可用")

// Client Redis 客户端。
type Client struct {
	Addr    string // host:port
	User    string // 可选
	Pass    string // 可选
	DB      int    // 可选，默认 0
	Timeout time.Duration
}

// ParseURL 解析 redis://[user:pass@]host:port[/db] 形式的 URL。
func ParseURL(raw string) (*Client, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty redis url")
	}
	if !strings.Contains(raw, "://") {
		raw = "redis://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("解析 REDIS_URL 失败: %w", err)
	}
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return nil, fmt.Errorf("不支持的 REDIS_URL scheme=%q（仅 redis://）", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("REDIS_URL 缺少 host:port")
	}
	c := &Client{
		Addr:    u.Host,
		Timeout: 3 * time.Second,
	}
	if u.User != nil {
		c.User = u.User.Username()
		if p, ok := u.User.Password(); ok {
			c.Pass = p
		}
	}
	if p := strings.TrimPrefix(u.Path, "/"); p != "" {
		db, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("REDIS_URL 数据库编号无效: %q", p)
		}
		if db < 0 || db > 15 {
			return nil, fmt.Errorf("REDIS_URL 数据库编号超出范围: %d", db)
		}
		c.DB = db
	}
	return c, nil
}

// dial 建立连接并完成 AUTH / SELECT。
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	r := bufio.NewReader(conn)
	if c.Pass != "" {
		args := []string{"AUTH", c.Pass}
		if c.User != "" {
			args = []string{"AUTH", c.User, c.Pass}
		}
		if err := writeCmd(conn, args...); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := readReply(r); err != nil {
			conn.Close()
			return nil, err
		}
	}
	if c.DB > 0 {
		if err := writeCmd(conn, "SELECT", strconv.Itoa(c.DB)); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := readReply(r); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// Do 执行一条命令并返回响应。simple string 返回 "+OK" 的文本部分（如 "OK"）；
// 整数返回十进制字符串；bulk string 返回字符串内容；nil 返回空字符串。
// 出错（连接失败/ERR 回复）返回错误。
func (c *Client) Do(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("空命令")
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := writeCmd(conn, args...); err != nil {
		return "", err
	}
	reply, err := readReply(bufio.NewReader(conn))
	if err != nil {
		return "", err
	}
	if reply.nil {
		return "", nil
	}
	return reply.str, nil
}

// Ping 连通性检查。
func (c *Client) Ping(ctx context.Context) error {
	v, err := c.Do(ctx, "PING")
	if err != nil {
		return err
	}
	if !strings.EqualFold(v, "PONG") && v != "OK" {
		return fmt.Errorf("%w: 非预期 PING 响应 %q", ErrUnavailable, v)
	}
	return nil
}

// -------- RESP 编解码 --------

func writeCmd(w net.Conn, args ...string) error {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, a := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(a)))
		b.WriteString("\r\n")
		b.WriteString(a)
		b.WriteString("\r\n")
	}
	_ = w.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err := w.Write([]byte(b.String()))
	return err
}

type reply struct {
	str string
	nil bool
}

func readReply(r *bufio.Reader) (*reply, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("空响应")
	}
	switch line[0] {
	case '+':
		return &reply{str: line[1:]}, nil
	case '-':
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, line[1:])
	case ':':
		return &reply{str: line[1:]}, nil
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return &reply{nil: true}, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return &reply{str: string(buf[:n])}, nil
	case '*':
		// 数组回复：本实现只用于兼容（解析数量即可，不深入取值）
		if _, err := strconv.Atoi(line[1:]); err != nil {
			return nil, err
		}
		return &reply{str: ""}, nil
	default:
		return nil, fmt.Errorf("未知 RESP 回复: %q", line)
	}
}

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("%w: 读取失败: %v", ErrUnavailable, err)
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	return line, nil
}

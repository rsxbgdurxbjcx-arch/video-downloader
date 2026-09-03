// Package auth 实现注册、邮箱验证、登录、会话等认证业务。
// 密码使用 bcrypt 哈希（自带盐与成本因子）；令牌一律使用密码学安全随机数，
// 数据库中只保存 SHA-256 哈希。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ErrWeakPassword 密码强度不足。
var ErrWeakPassword = errors.New("密码至少 8 位且必须同时包含字母和数字")

// password 复杂度检查
var (
	pwHasLetter = regexp.MustCompile(`[a-zA-Z]`)
	pwHasDigit  = regexp.MustCompile(`[0-9]`)
)

// emailRe 邮箱格式校验（RFC 5322 简版）。
var emailRe = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)+$`)

// HashPassword 生成 bcrypt 哈希。
func HashPassword(pw string) (string, error) {
	if !ValidatePassword(pw) {
		return "", ErrWeakPassword
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// CheckPassword 校验密码与哈希是否匹配。
func CheckPassword(hash, pw string) bool {
	if hash == "" || pw == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// ValidatePassword 校验密码复杂度。
func ValidatePassword(pw string) bool {
	return len(pw) >= 8 && pwHasLetter.MatchString(pw) && pwHasDigit.MatchString(pw)
}

// NormalizeEmail 去除首尾空格并转为小写。
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail 校验邮箱格式与长度。
func ValidateEmail(email string) error {
	email = NormalizeEmail(email)
	if email == "" {
		return errors.New("邮箱不能为空")
	}
	if len(email) > 254 {
		return errors.New("邮箱长度超出限制")
	}
	if !emailRe.MatchString(email) {
		return errors.New("邮箱格式无效")
	}
	return nil
}

// HashEmail 邮箱哈希（用于邮件发送频控，不保存明文）。
func HashEmail(email string) string {
	return HashTokenData(NormalizeEmail(email))
}

// HashTokenData 任意数据 SHA-256 十六进制哈希。
func HashTokenData(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// RandomToken 生成 32 字节密码学安全随机数的 hex 字符串（64 字符）。
func RandomToken() (string, error) {
	return RandomBytesHex(32)
}

// RandomBytesHex 生成 n 字节随机数的 hex 字符串。
func RandomBytesHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateVerifyCode 生成 6 位数字验证码（密码学安全随机，取值范围 000000-999999）。
// 数据库只保存其 SHA-256 哈希；验证码本体只出现在邮件正文与（开发环境）控制台。
func GenerateVerifyCode() (string, error) {
	b := make([]byte, 4) // 4 字节随机数足以覆盖 0-999999 且避免截断偏差
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	n := uint32(0)
	for _, by := range b {
		n = n<<8 | uint32(by)
	}
	return fmt.Sprintf("%06d", n%1000000), nil
}

// VerifyCodeRe 6 位数字验证码格式。
var VerifyCodeRe = regexp.MustCompile(`^[0-9]{6}$`)

// ValidateVerifyCode 校验验证码格式（6 位数字）。
func ValidateVerifyCode(code string) bool {
	return VerifyCodeRe.MatchString(code)
}

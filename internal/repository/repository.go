// Package repository 提供基于 database/sql 的数据访问层。
// 全部 SQL 参数化；仅依赖 database/sql 接口，便于未来迁移到 PostgreSQL。
package repository

import (
	"database/sql"
	"strconv"
	"time"
)

// Store 聚合全部仓储。
type Store struct {
	DB *sql.DB
}

// NewStore 创建 Store。
func NewStore(db *sql.DB) *Store { return &Store{DB: db} }

// unixTime 扫描 SQLite 的 INTEGER Unix 秒为 time.Time。
type unixTime struct {
	t *time.Time
}

func (u *unixTime) Scan(src any) error {
	if src == nil {
		u.t = nil
		return nil
	}
	var n int64
	switch v := src.(type) {
	case int64:
		n = v
	case []byte:
		parsed, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return err
		}
		n = parsed
	case int:
		n = int64(v)
	default:
		return &scanTypeError{src: src}
	}
	tt := time.Unix(n, 0).UTC()
	u.t = &tt
	return nil
}

// nullUnixTime 扫描可空的 Unix 秒。
type nullUnixTime struct {
	t     *time.Time
	valid bool
}

func (n *nullUnixTime) Scan(src any) error {
	if src == nil {
		n.t, n.valid = nil, false
		return nil
	}
	var u unixTime
	if err := u.Scan(src); err != nil {
		return err
	}
	n.t, n.valid = u.t, true
	return nil
}

// nullString 扫描可空字符串。
type nullString struct {
	s     string
	valid bool
}

func (n *nullString) Scan(src any) error {
	if src == nil {
		n.s, n.valid = "", false
		return nil
	}
	switch v := src.(type) {
	case string:
		n.s, n.valid = v, true
	case []byte:
		n.s, n.valid = string(v), true
	default:
		return &scanTypeError{src: src}
	}
	return nil
}

// scanTypeError 扫描类型错误。
type scanTypeError struct {
	src any
}

func (e *scanTypeError) Error() string {
	return "不支持的扫描类型"
}

// nullableInt64 将 *int64 转为 sql.NullInt64。
func nullableInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

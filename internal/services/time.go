package services

import (
	"fmt"
	"time"
)

// dbTime 是一个宽容的时间扫描器：无论驱动把 DATETIME 列返回为
// time.Time、字符串还是整数，都能正确解析，避免因驱动版本差异出错。
type dbTime struct {
	Time time.Time
}

// timeLayouts 按优先级尝试的时间格式。
var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// Scan 实现 sql.Scanner。
func (t *dbTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		t.Time = time.Time{}
		return nil
	case time.Time:
		t.Time = v.UTC()
		return nil
	case string:
		return t.parse(v)
	case []byte:
		return t.parse(string(v))
	case int64:
		t.Time = time.Unix(v, 0).UTC()
		return nil
	case float64:
		t.Time = time.Unix(int64(v), 0).UTC()
		return nil
	default:
		return fmt.Errorf("无法把 %T 转换为时间", src)
	}
}

func (t *dbTime) parse(s string) error {
	if s == "" {
		t.Time = time.Time{}
		return nil
	}
	for _, layout := range timeLayouts {
		if ts, err := time.Parse(layout, s); err == nil {
			t.Time = ts.UTC()
			return nil
		}
	}
	return fmt.Errorf("无法解析时间字符串 %q", s)
}

// nullTime 表示可为空的时间列。
type nullTime struct {
	Time  time.Time
	Valid bool
}

// Scan 实现 sql.Scanner。
func (n *nullTime) Scan(src any) error {
	if src == nil {
		n.Time, n.Valid = time.Time{}, false
		return nil
	}
	var t dbTime
	if err := t.Scan(src); err != nil {
		return err
	}
	n.Time, n.Valid = t.Time, true
	return nil
}

// Ptr 返回 *time.Time，无效时返回 nil。
func (n nullTime) Ptr() *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}

// nowUTC 返回当前 UTC 时间，作为所有写入的统一时间源。
func nowUTC() time.Time { return time.Now().UTC() }

// startOfDay 返回给定时间所在自然日（UTC+8）的零点，结果仍为 UTC。
func startOfDay(t time.Time) time.Time {
	local := t.In(displayZone)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, displayZone).UTC()
}

// startOfMonth 返回给定时间所在自然月（UTC+8）的第一天零点。
func startOfMonth(t time.Time) time.Time {
	local := t.In(displayZone)
	y, m, _ := local.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, displayZone).UTC()
}

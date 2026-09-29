package services

import (
	"database/sql"
	"fmt"
)

// rowsAffected 读取语句影响的行数；驱动报错时如实返回，而不是静默当成 0 行。
//
// 直接写 `n, _ := res.RowsAffected()` 会把「驱动查不出行数」伪装成
// 「0 行受影响」，而调用方紧接着就会报出「记录不存在」——
// 一个与真实故障毫无关系的结论。排查时会沿着完全错误的方向走很远，
// 因此这里必须把错误原样传上去。
func rowsAffected(res sql.Result) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取受影响行数失败: %w", err)
	}
	return n, nil
}

package services

import (
	"context"
	"testing"
	"time"
)

// 从未在页面上设置过时，开关必须回落到配置项（环境变量）给出的默认值。
// 若这里回落到零值 false，一个默认开放注册的站点会在升级后静默关闭注册。
func TestRegistrationStateFallsBackToDefault(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for _, fallback := range []bool{true, false} {
		st, err := store.RegistrationState(ctx, fallback)
		if err != nil {
			t.Fatalf("读取注册开关失败: %v", err)
		}
		if st.Enabled != fallback {
			t.Errorf("未设置时 Enabled = %v，期望回落到默认值 %v", st.Enabled, fallback)
		}
		if st.Explicit {
			t.Error("未设置时 Explicit 应为 false —— 取值来自环境变量给出的初始状态")
		}
	}
}

// 页面上的设置必须优先于环境变量默认值，且两个方向都要生效。
func TestRegistrationSettingOverridesDefault(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	// 场景一：环境变量默认关闭，管理员在页面上开启。
	if err := store.SetRegistrationEnabled(ctx, true); err != nil {
		t.Fatalf("写入注册开关失败: %v", err)
	}
	st, err := store.RegistrationState(ctx, false)
	if err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	}
	if !st.Enabled {
		t.Error("页面上设为开启后，不应再回落到默认的关闭")
	}
	if !st.Explicit {
		t.Error("页面上设置过之后 Explicit 应为 true")
	}

	// 场景二：环境变量默认开启，管理员在页面上关闭。
	if err := store.SetRegistrationEnabled(ctx, false); err != nil {
		t.Fatalf("写入注册开关失败: %v", err)
	}
	st, err = store.RegistrationState(ctx, true)
	if err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	}
	if st.Enabled {
		t.Error("页面上设为关闭后，不应再回落到默认的开启")
	}
	if !st.Explicit {
		t.Error("页面上设置过之后 Explicit 应为 true")
	}
}

// 反复写入不应报错（走 upsert），且最后一次写入就是最终状态 ——
// 开关只有「开」和「关」两态，没有能把它交还给环境变量的第三态。
func TestRegistrationSettingIsIdempotent(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := store.SetRegistrationEnabled(ctx, true); err != nil {
			t.Fatalf("第 %d 次写入注册开关失败: %v", i+1, err)
		}
	}

	st, err := store.RegistrationState(ctx, false)
	if err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	}
	if !st.Enabled || !st.Explicit {
		t.Errorf("重复写入后应仍为开启的显式设置，实际 Enabled=%v Explicit=%v", st.Enabled, st.Explicit)
	}

	// 再关一次：结果必须是关，而不能被环境变量默认值 true 翻回去。
	if err := store.SetRegistrationEnabled(ctx, false); err != nil {
		t.Fatalf("关闭注册开关失败: %v", err)
	}
	st, err = store.RegistrationState(ctx, true)
	if err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	}
	if st.Enabled {
		t.Error("页面上关闭后不应被环境变量的 true 翻回去")
	}
	if !st.Explicit {
		t.Error("关闭同样是一次明确的设置，Explicit 应为 true")
	}
}

// 取值被手工改坏时，既不报错也不猜测意图，而是回落到默认值并标记为「非显式」，
// 让管理员在页面上重新点一次即可修复。若这里猜成 false，
// 就等于把「数据库里有个坏值」变成「注册被静默关闭」。
func TestRegistrationStateToleratesCorruptValue(t *testing.T) {
	store, db := newTestStore(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
		SettingAllowRegistration, "yes-please", time.Now().UTC()); err != nil {
		t.Fatalf("写入坏值失败: %v", err)
	}

	st, err := store.RegistrationState(ctx, true)
	if err != nil {
		t.Fatalf("遇到坏值时不应报错: %v", err)
	}
	if !st.Enabled {
		t.Error("坏值应回落到默认值 true，而不是猜成关闭")
	}
	if st.Explicit {
		t.Error("坏值不应被当作显式设置，否则日志会报出一个不可信的取值来源")
	}
}

// 「没有设置过」与「设置为空字符串」必须可区分。
func TestGetSettingDistinguishesMissingFromEmpty(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	if _, ok, err := store.GetSetting(ctx, "not-exists"); err != nil || ok {
		t.Fatalf("不存在的键应返回 ok=false 且无错误，实际 ok=%v err=%v", ok, err)
	}

	if err := store.SetSetting(ctx, "empty", ""); err != nil {
		t.Fatalf("写入空值失败: %v", err)
	}
	v, ok, err := store.GetSetting(ctx, "empty")
	if err != nil || !ok || v != "" {
		t.Fatalf("空字符串设置应返回 (\"\", true, nil)，实际 (%q, %v, %v)", v, ok, err)
	}
}

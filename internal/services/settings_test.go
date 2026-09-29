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
			t.Error("未设置时 Explicit 应为 false，界面才会提示「跟随环境变量」")
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

// 反复写入不应报错（走 upsert），恢复默认后应回到环境变量取值。
func TestRegistrationSettingIsIdempotentAndResettable(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := store.SetRegistrationEnabled(ctx, true); err != nil {
			t.Fatalf("第 %d 次写入注册开关失败: %v", i+1, err)
		}
	}

	if err := store.ResetRegistrationSetting(ctx); err != nil {
		t.Fatalf("恢复默认设置失败: %v", err)
	}

	st, err := store.RegistrationState(ctx, false)
	if err != nil {
		t.Fatalf("读取注册开关失败: %v", err)
	}
	if st.Enabled {
		t.Error("恢复默认后应回到环境变量取值（关闭），实际仍为开启")
	}
	if st.Explicit {
		t.Error("恢复默认后 Explicit 应为 false")
	}

	// 幂等：键本就不存在时再删一次不应报错。
	if err := store.ResetRegistrationSetting(ctx); err != nil {
		t.Errorf("重复恢复默认设置不应报错: %v", err)
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
		t.Error("坏值不应被当作显式设置，否则界面会显示一个不可信的来源")
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

package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// 系统设置（settings 表）
//
// 这里存放「需要在运行期由管理员调整、且必须跨重启保留」的少量开关。
// 之所以不直接用环境变量：环境变量要改就得登服务器改配置再重启服务，
// 而这类开关的调整时机往往就是管理员在页面上看到问题的那一刻。
//
// 取值优先级约定：settings 表中的设置 > 配置项（环境变量）默认值。
// 环境变量只在「从未在页面上设置过」时生效（即首次部署的初始状态）；
// 管理员一旦在页面上做过选择，那个选择就是唯一的取值来源，不会再被环境变量翻回去。
// 这不会带来额外的权限提升 —— 能进入用户管理页的管理员本来就能直接建号。
// ---------------------------------------------------------------------------

// SettingAllowRegistration 是「是否开放自助注册」在 settings 表中的键名。
const SettingAllowRegistration = "allow_registration"

// GetSetting 读取一项系统设置。键不存在时返回 ("", false, nil) ——
// 「没有设置过」与「设置为空字符串」是两回事，调用方需要能区分，
// 因此这里不用 error 表达「不存在」。
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取设置 %s 失败: %w", key, err)
	}
	return value, true, nil
}

// SetSetting 写入或覆盖一项系统设置。
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, nowUTC())
	if err != nil {
		return fmt.Errorf("保存设置 %s 失败: %w", key, err)
	}
	return nil
}

// RegistrationState 描述「是否开放自助注册」的当前状态及其来源。
type RegistrationState struct {
	// Enabled 是最终生效的取值。
	Enabled bool

	// Explicit 为 true 表示取值来自管理员在页面上的设置；
	// 为 false 表示尚未在页面上设置过，取值跟随配置项默认值（首次部署的初始状态）。
	// 界面只呈现 Enabled 这一个结果，Explicit 供启动日志说明取值来源。
	Explicit bool
}

// RegistrationState 读取自助注册开关。
//
// fallback 是配置项（环境变量）给出的默认值，仅在页面上从未设置过时使用。
func (s *Store) RegistrationState(ctx context.Context, fallback bool) (RegistrationState, error) {
	raw, ok, err := s.GetSetting(ctx, SettingAllowRegistration)
	if err != nil {
		return RegistrationState{Enabled: fallback}, err
	}
	if !ok {
		return RegistrationState{Enabled: fallback}, nil
	}

	enabled, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		// 取值被手工改坏（例如直接 UPDATE settings）时不猜测意图：
		// 猜测的代价是「把坏值当成 false 静默关掉注册」或反过来静默打开，
		// 两者都不是管理员想要的。这里回落到配置默认值，并保留 Explicit=false，
		// 让页面显示为「跟随默认值」，管理员重新点一次即可覆盖。
		if s.logger != nil {
			s.logger.Warn("settings 表中的注册开关取值无法解析，已回落到配置默认值",
				"键", SettingAllowRegistration, "值", raw, "错误", err)
		}
		return RegistrationState{Enabled: fallback}, nil
	}
	return RegistrationState{Enabled: enabled, Explicit: true}, nil
}

// SetRegistrationEnabled 持久化自助注册开关，之后不再受配置项默认值影响。
//
// 开关只有「开」和「关」两种取值，没有第三种「交还给环境变量」的状态 ——
// 写入即定论，避免页面上出现「我到底设没设过」这种说不清的情况。
func (s *Store) SetRegistrationEnabled(ctx context.Context, enabled bool) error {
	return s.SetSetting(ctx, SettingAllowRegistration, strconv.FormatBool(enabled))
}

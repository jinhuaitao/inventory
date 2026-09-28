package models

import "testing"

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		role      Role
		canWrite  bool
		canManage bool
		label     string
	}{
		{RoleAdmin, true, true, "管理员"},
		{RoleManager, true, false, "仓管员"},
		{RoleViewer, false, false, "只读用户"},
	}

	for _, tt := range tests {
		if got := tt.role.CanWrite(); got != tt.canWrite {
			t.Errorf("%s.CanWrite() = %v, 期望 %v", tt.role, got, tt.canWrite)
		}
		if got := tt.role.CanManageUsers(); got != tt.canManage {
			t.Errorf("%s.CanManageUsers() = %v, 期望 %v", tt.role, got, tt.canManage)
		}
		if got := tt.role.Label(); got != tt.label {
			t.Errorf("%s.Label() = %q, 期望 %q", tt.role, got, tt.label)
		}
		if !tt.role.Valid() {
			t.Errorf("%s 应当是合法角色", tt.role)
		}
	}

	if Role("superuser").Valid() {
		t.Error("未知角色不应通过校验")
	}
	if len(AllRoles()) != 3 {
		t.Errorf("角色总数应为 3，实际 %d", len(AllRoles()))
	}
}

func TestProductStockLevel(t *testing.T) {
	tests := []struct {
		name     string
		quantity int
		safety   int
		wantLow  bool
		wantOut  bool
		wantLvl  string
	}{
		{"库存充足", 100, 20, false, false, "ok"},
		{"恰好等于安全库存", 20, 20, true, false, "warning"},
		{"低于安全库存", 5, 20, true, false, "warning"},
		{"库存为零", 0, 20, true, true, "danger"},
		{"安全库存为零且有货", 10, 0, false, false, "ok"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Product{Quantity: tt.quantity, SafetyStock: tt.safety}
			if got := p.IsLowStock(); got != tt.wantLow {
				t.Errorf("IsLowStock() = %v, 期望 %v", got, tt.wantLow)
			}
			if got := p.IsOutOfStock(); got != tt.wantOut {
				t.Errorf("IsOutOfStock() = %v, 期望 %v", got, tt.wantOut)
			}
			if got := p.StockLevel(); got != tt.wantLvl {
				t.Errorf("StockLevel() = %q, 期望 %q", got, tt.wantLvl)
			}
		})
	}
}

func TestProductValueCalculations(t *testing.T) {
	p := &Product{Quantity: 10, CostPrice: 8.5, SalePrice: 20}

	if got := p.StockValue(); got != 85 {
		t.Errorf("StockValue() = %v, 期望 85", got)
	}
	if got := p.ExpectedRevenue(); got != 200 {
		t.Errorf("ExpectedRevenue() = %v, 期望 200", got)
	}

	// 毛利率 = (20-8.5)/20*100 = 57.5
	if got := p.GrossMarginPercent(); got < 57.4 || got > 57.6 {
		t.Errorf("GrossMarginPercent() = %v, 期望约 57.5", got)
	}

	// 未设置售价时毛利率为 0，避免除零
	noPrice := &Product{Quantity: 10, CostPrice: 5}
	if got := noPrice.GrossMarginPercent(); got != 0 {
		t.Errorf("售价为 0 时毛利率应为 0，实际 %v", got)
	}
}

func TestMovementType(t *testing.T) {
	tests := []struct {
		typ       MovementType
		label     string
		direction int
	}{
		{MovementIn, "入库", 1},
		{MovementOut, "出库", -1},
		{MovementAdjust, "盘点调整", -1},
		{MovementInit, "期初建账", 1},
	}

	for _, tt := range tests {
		if got := tt.typ.Label(); got != tt.label {
			t.Errorf("%s.Label() = %q, 期望 %q", tt.typ, got, tt.label)
		}
		if got := tt.typ.Direction(); got != tt.direction {
			t.Errorf("%s.Direction() = %d, 期望 %d", tt.typ, got, tt.direction)
		}
		if !tt.typ.Valid() {
			t.Errorf("%s 应当是合法类型", tt.typ)
		}
	}

	if MovementType("unknown").Valid() {
		t.Error("未知类型不应通过校验")
	}
	if len(AllMovementTypes()) != 4 {
		t.Errorf("流水类型总数应为 4，实际 %d", len(AllMovementTypes()))
	}
}

func TestStockMovementFormatting(t *testing.T) {
	in := &StockMovement{Quantity: 12, Delta: 12, UnitPrice: 10}
	out := &StockMovement{Quantity: 3, Delta: -3, UnitPrice: 10}

	if got := in.SignedQuantity(); got != "+12" {
		t.Errorf("入库带符号数量 = %q, 期望 +12", got)
	}
	if got := out.SignedQuantity(); got != "-3" {
		t.Errorf("出库带符号数量 = %q, 期望 -3", got)
	}
	if got := in.Amount(); got != 120 {
		t.Errorf("入库金额 = %v, 期望 120", got)
	}
	if got := out.Amount(); got != 30 {
		t.Errorf("出库金额应为绝对数量 × 单价 = 30，实际 %v", got)
	}
}

func TestUserHelpers(t *testing.T) {
	u := &User{Username: "zhangsan", FullName: "张三", Status: UserStatusActive}
	if got := u.DisplayName(); got != "张三" {
		t.Errorf("DisplayName() = %q, 期望 张三", got)
	}
	if got := u.Initials(); got != "张" {
		t.Errorf("中文姓名首字 = %q, 期望 张", got)
	}
	if !u.IsActive() {
		t.Error("active 状态应为可用")
	}

	u2 := &User{Username: "lisi", Status: UserStatusDisabled}
	if got := u2.DisplayName(); got != "lisi" {
		t.Errorf("无姓名时应回落到用户名，实际 %q", got)
	}
	if got := u2.Initials(); got != "LI" {
		t.Errorf("英文用户名首字母 = %q, 期望 LI", got)
	}
	if u2.IsActive() {
		t.Error("disabled 状态应为不可用")
	}

	empty := &User{}
	if got := empty.Initials(); got != "?" {
		t.Errorf("空用户首字母应为 ?，实际 %q", got)
	}
}

func TestTrendPointMaxValue(t *testing.T) {
	if got := (TrendPoint{In: 10, Out: 3}).MaxValue(); got != 10 {
		t.Errorf("MaxValue() = %d, 期望 10", got)
	}
	if got := (TrendPoint{In: 3, Out: 10}).MaxValue(); got != 10 {
		t.Errorf("MaxValue() = %d, 期望 10", got)
	}
	if got := (TrendPoint{}).MaxValue(); got != 0 {
		t.Errorf("空数据 MaxValue() = %d, 期望 0", got)
	}
}

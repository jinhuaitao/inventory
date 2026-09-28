// Package models 定义系统全部领域实体及其行为。
package models

import (
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// 角色与权限
// ---------------------------------------------------------------------------

// Role 表示用户在系统中的角色。
type Role string

const (
	// RoleAdmin 管理员：拥有全部权限，可管理用户。
	RoleAdmin Role = "admin"
	// RoleManager 仓管员：可管理商品、分类、供应商与出入库。
	RoleManager Role = "manager"
	// RoleViewer 只读用户：仅可查看数据。
	RoleViewer Role = "viewer"
)

// AllRoles 返回全部可选角色。
func AllRoles() []Role { return []Role{RoleAdmin, RoleManager, RoleViewer} }

// Label 返回角色的中文名称。
func (r Role) Label() string {
	switch r {
	case RoleAdmin:
		return "管理员"
	case RoleManager:
		return "仓管员"
	case RoleViewer:
		return "只读用户"
	default:
		return string(r)
	}
}

// CanWrite 表示该角色是否具备写权限。
func (r Role) CanWrite() bool { return r == RoleAdmin || r == RoleManager }

// CanManageUsers 表示该角色是否可以管理用户。
func (r Role) CanManageUsers() bool { return r == RoleAdmin }

// Valid 判断角色取值是否合法。
func (r Role) Valid() bool {
	for _, x := range AllRoles() {
		if x == r {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// 用户
// ---------------------------------------------------------------------------

// 用户状态
const (
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
)

// User 表示一个系统账号。
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	FullName     string     `json:"full_name"`
	Role         Role       `json:"role"`
	Status       string     `json:"status"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// IsActive 判断账号是否可用。
func (u *User) IsActive() bool { return u.Status == UserStatusActive }

// DisplayName 返回用于界面展示的称呼，优先使用姓名。
func (u *User) DisplayName() string {
	if strings.TrimSpace(u.FullName) != "" {
		return u.FullName
	}
	return u.Username
}

// Initials 返回用于头像展示的首字母 / 首字。
func (u *User) Initials() string {
	name := strings.TrimSpace(u.DisplayName())
	if name == "" {
		return "?"
	}
	runes := []rune(name)
	if len(runes) >= 1 && runes[0] > 127 {
		// 中文取第一个字
		return string(runes[0])
	}
	parts := strings.Fields(name)
	if len(parts) >= 2 {
		return strings.ToUpper(string(parts[0][0]) + string(parts[1][0]))
	}
	if len(runes) >= 2 {
		return strings.ToUpper(string(runes[:2]))
	}
	return strings.ToUpper(name)
}

// ---------------------------------------------------------------------------
// 分类
// ---------------------------------------------------------------------------

// Category 商品分类。
type Category struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	ProductCount int       `json:"product_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// 供应商
// ---------------------------------------------------------------------------

// Supplier 供应商。
type Supplier struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	ContactPerson string    `json:"contact_person"`
	Phone         string    `json:"phone"`
	Email         string    `json:"email"`
	Address       string    `json:"address"`
	Note          string    `json:"note"`
	ProductCount  int       `json:"product_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ---------------------------------------------------------------------------
// 商品
// ---------------------------------------------------------------------------

// 商品状态
const (
	ProductStatusActive   = "active"
	ProductStatusArchived = "archived"
)

// Product 商品 / 物料主数据。
type Product struct {
	ID           int64     `json:"id"`
	SKU          string    `json:"sku"`
	Name         string    `json:"name"`
	Barcode      string    `json:"barcode"`
	CategoryID   *int64    `json:"category_id,omitempty"`
	SupplierID   *int64    `json:"supplier_id,omitempty"`
	Unit         string    `json:"unit"`
	CostPrice    float64   `json:"cost_price"`
	SalePrice    float64   `json:"sale_price"`
	Quantity     int       `json:"quantity"`
	SafetyStock  int       `json:"safety_stock"`
	Location     string    `json:"location"`
	Description  string    `json:"description"`
	Status       string    `json:"status"`
	CategoryName string    `json:"category_name,omitempty"`
	SupplierName string    `json:"supplier_name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IsLowStock 判断库存是否低于或等于安全库存。
func (p *Product) IsLowStock() bool { return p.Quantity <= p.SafetyStock }

// IsOutOfStock 判断是否已缺货。
func (p *Product) IsOutOfStock() bool { return p.Quantity <= 0 }

// StockValue 返回该商品的库存成本总额。
func (p *Product) StockValue() float64 { return float64(p.Quantity) * p.CostPrice }

// ExpectedRevenue 返回按售价计算的库存预期销售额。
func (p *Product) ExpectedRevenue() float64 { return float64(p.Quantity) * p.SalePrice }

// GrossMarginPercent 返回毛利率百分比（0-100），售价为 0 时返回 0。
func (p *Product) GrossMarginPercent() float64 {
	if p.SalePrice <= 0 {
		return 0
	}
	return (p.SalePrice - p.CostPrice) / p.SalePrice * 100
}

// StockLevel 返回库存水位标签，用于列表着色。
func (p *Product) StockLevel() string {
	switch {
	case p.Quantity <= 0:
		return "danger"
	case p.IsLowStock():
		return "warning"
	default:
		return "ok"
	}
}

// ---------------------------------------------------------------------------
// 库存流水
// ---------------------------------------------------------------------------

// MovementType 库存变动类型。
type MovementType string

const (
	// MovementIn 入库
	MovementIn MovementType = "in"
	// MovementOut 出库
	MovementOut MovementType = "out"
	// MovementAdjust 盘点调整
	MovementAdjust MovementType = "adjust"
	// MovementInit 期初建账
	MovementInit MovementType = "init"
)

// AllMovementTypes 返回全部流水类型。
func AllMovementTypes() []MovementType {
	return []MovementType{MovementIn, MovementOut, MovementAdjust, MovementInit}
}

// Label 返回类型的中文名称。
func (t MovementType) Label() string {
	switch t {
	case MovementIn:
		return "入库"
	case MovementOut:
		return "出库"
	case MovementAdjust:
		return "盘点调整"
	case MovementInit:
		return "期初建账"
	default:
		return string(t)
	}
}

// Direction 返回 +1（增加库存）或 -1（减少库存）。
func (t MovementType) Direction() int {
	if t == MovementIn || t == MovementInit {
		return 1
	}
	return -1
}

// Valid 判断类型是否合法。
func (t MovementType) Valid() bool {
	for _, x := range AllMovementTypes() {
		if x == t {
			return true
		}
	}
	return false
}

// StockMovement 一条库存变动流水，是所有出入库操作的事实记录。
type StockMovement struct {
	ID           int64        `json:"id"`
	ProductID    int64        `json:"product_id"`
	Type         MovementType `json:"type"`
	Quantity     int          `json:"quantity"` // 变动数量（绝对值）
	Delta        int          `json:"delta"`    // 有符号变动量
	UnitPrice    float64      `json:"unit_price"`
	BeforeQty    int          `json:"before_qty"`
	AfterQty     int          `json:"after_qty"`
	RefNo        string       `json:"ref_no"`
	SupplierID   *int64       `json:"supplier_id,omitempty"`
	OperatorID   int64        `json:"operator_id"`
	Note         string       `json:"note"`
	CreatedAt    time.Time    `json:"created_at"`
	ProductName  string       `json:"product_name,omitempty"`
	ProductSKU   string       `json:"product_sku,omitempty"`
	ProductUnit  string       `json:"product_unit,omitempty"`
	OperatorName string       `json:"operator_name,omitempty"`
	SupplierName string       `json:"supplier_name,omitempty"`
}

// Amount 返回本次变动的金额。
func (m *StockMovement) Amount() float64 { return float64(m.Quantity) * m.UnitPrice }

// SignedQuantity 返回带符号的数量文本，例如 "+12" / "-3"。
func (m *StockMovement) SignedQuantity() string {
	if m.Delta >= 0 {
		return fmt.Sprintf("+%d", m.Delta)
	}
	return fmt.Sprintf("%d", m.Delta)
}

// ---------------------------------------------------------------------------
// 会话与安全问题
// ---------------------------------------------------------------------------

// Session 服务端会话记录，cookie 中只保存随机令牌。
type Session struct {
	ID        int64
	UserID    int64
	TokenHash string
	CSRFToken string
	ExpiresAt time.Time
	CreatedAt time.Time
	UserAgent string
	IP        string
}

// SecurityQuestionCount 是找回密码要求的安全问题数量。
const SecurityQuestionCount = 3

// SecurityQuestion 用户自定义的找回密码安全问题。
//
// 答案只保存 bcrypt 摘要，且校验前会先做归一化（去首尾空格 + 转小写），
// 避免用户因大小写或多余空格而无法找回密码。
type SecurityQuestion struct {
	ID         int64
	UserID     int64
	Position   int    // 第几题，从 1 开始
	Question   string // 用户自定义的问题文本
	AnswerHash string // 归一化后答案的 bcrypt 摘要
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AnswerInput 是设置或校验安全问题时的输入。
type AnswerInput struct {
	Question string
	Answer   string
}

// ---------------------------------------------------------------------------
// 统计与报表
// ---------------------------------------------------------------------------

// DashboardStats 仪表盘核心指标。
type DashboardStats struct {
	TotalProducts   int
	TotalCategories int
	TotalSuppliers  int
	TotalUsers      int
	TotalStock      int
	StockValue      float64
	LowStockCount   int
	OutOfStockCount int
	TodayIn         int
	TodayOut        int
	MonthIn         int
	MonthOut        int
	TotalMovements  int
}

// TrendPoint 出入库趋势图上的一个数据点。
type TrendPoint struct {
	Label string
	In    int
	Out   int
}

// MaxValue 返回该点上入库与出库的较大值，便于计算柱状图高度。
func (t TrendPoint) MaxValue() int {
	if t.In > t.Out {
		return t.In
	}
	return t.Out
}

// CategoryStat 分类维度的库存分布。
type CategoryStat struct {
	Name  string
	Count int
	Value float64
}

// ProductStat 商品维度的排行数据。
type ProductStat struct {
	Name     string
	SKU      string
	Quantity int
	Value    float64
}

-- 库存管理系统数据库结构
-- 所有语句均为幂等（IF NOT EXISTS），可在每次启动时安全执行。
-- 时间列统一使用 DATETIME，由 Go 侧以 UTC 写入，避免依赖 SQLite 的 CURRENT_TIMESTAMP。

-- ---------------------------------------------------------------------------
-- 用户与认证
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    email         TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    password_hash TEXT    NOT NULL,
    full_name     TEXT    NOT NULL DEFAULT '',
    role          TEXT    NOT NULL DEFAULT 'viewer',
    status        TEXT    NOT NULL DEFAULT 'active',
    last_login_at DATETIME,
    created_at    DATETIME NOT NULL,
    updated_at    DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_users_role   ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);

-- 服务端会话：浏览器只保存随机令牌，令牌以 SHA-256 摘要形式落库
CREATE TABLE IF NOT EXISTS sessions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT    NOT NULL UNIQUE,
    csrf_token TEXT    NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    user_agent TEXT    NOT NULL DEFAULT '',
    ip         TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_sessions_user    ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- 安全问题：找回密码时用于验证身份，答案只存 bcrypt 摘要
CREATE TABLE IF NOT EXISTS security_questions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,          -- 第几题，从 1 开始
    question    TEXT    NOT NULL,          -- 用户自定义的问题文本
    answer_hash TEXT    NOT NULL,          -- 归一化后答案的 bcrypt 摘要
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL,
    UNIQUE (user_id, position)
);

CREATE INDEX IF NOT EXISTS idx_security_questions_user ON security_questions(user_id);

-- 找回密码尝试记录，用于限制答题暴力破解
-- 刻意不加外键：账号不存在时也要能记录尝试，以抵御枚举与爆破
CREATE TABLE IF NOT EXISTS recovery_attempts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL DEFAULT 0,
    identifier TEXT    NOT NULL DEFAULT '',
    ip         TEXT    NOT NULL DEFAULT '',
    success    INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_recovery_attempts_user ON recovery_attempts(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_recovery_attempts_ip   ON recovery_attempts(ip, created_at);

-- 登录尝试记录，用于失败次数限制
CREATE TABLE IF NOT EXISTS login_attempts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    identifier TEXT    NOT NULL,
    ip         TEXT    NOT NULL DEFAULT '',
    success    INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_login_attempts ON login_attempts(identifier, created_at);

-- ---------------------------------------------------------------------------
-- 主数据
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS categories (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    description TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS suppliers (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    contact_person TEXT    NOT NULL DEFAULT '',
    phone          TEXT    NOT NULL DEFAULT '',
    email          TEXT    NOT NULL DEFAULT '',
    address        TEXT    NOT NULL DEFAULT '',
    note           TEXT    NOT NULL DEFAULT '',
    created_at     DATETIME NOT NULL,
    updated_at     DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS products (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    sku          TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    name         TEXT    NOT NULL,
    barcode      TEXT    NOT NULL DEFAULT '',
    category_id  INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    supplier_id  INTEGER REFERENCES suppliers(id) ON DELETE SET NULL,
    unit         TEXT    NOT NULL DEFAULT '件',
    cost_price   REAL    NOT NULL DEFAULT 0,
    sale_price   REAL    NOT NULL DEFAULT 0,
    quantity     INTEGER NOT NULL DEFAULT 0,
    safety_stock INTEGER NOT NULL DEFAULT 0,
    location     TEXT    NOT NULL DEFAULT '',
    description  TEXT    NOT NULL DEFAULT '',
    status       TEXT    NOT NULL DEFAULT 'active',
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_products_category ON products(category_id);
CREATE INDEX IF NOT EXISTS idx_products_supplier ON products(supplier_id);
CREATE INDEX IF NOT EXISTS idx_products_status   ON products(status);
CREATE INDEX IF NOT EXISTS idx_products_name     ON products(name);
CREATE INDEX IF NOT EXISTS idx_products_barcode  ON products(barcode);

-- ---------------------------------------------------------------------------
-- 库存流水
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS stock_movements (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    type        TEXT    NOT NULL,
    quantity    INTEGER NOT NULL,
    delta       INTEGER NOT NULL,
    unit_price  REAL    NOT NULL DEFAULT 0,
    before_qty  INTEGER NOT NULL DEFAULT 0,
    after_qty   INTEGER NOT NULL DEFAULT 0,
    ref_no      TEXT    NOT NULL DEFAULT '',
    supplier_id INTEGER REFERENCES suppliers(id) ON DELETE SET NULL,
    operator_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    note        TEXT    NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_movements_product  ON stock_movements(product_id);
CREATE INDEX IF NOT EXISTS idx_movements_type     ON stock_movements(type);
CREATE INDEX IF NOT EXISTS idx_movements_created  ON stock_movements(created_at);
CREATE INDEX IF NOT EXISTS idx_movements_operator ON stock_movements(operator_id);

-- ---------------------------------------------------------------------------
-- 系统设置
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT     NOT NULL,
    updated_at DATETIME NOT NULL
);

-- ---------------------------------------------------------------------------
-- 历史表清理
-- ---------------------------------------------------------------------------

-- 找回密码已改为「安全问题」方式，邮箱重置令牌表不再使用。
-- DROP ... IF EXISTS 是幂等的，旧库升级时会安全移除该表。
DROP TABLE IF EXISTS password_resets;

-- ============================================================
-- 小电器生产企业数据库系统 - 初始化脚本
-- 核心模块：产品 + BOM(产品-零件对应关系) + 批次追溯
-- ============================================================

-- 产品表
CREATE TABLE IF NOT EXISTS products (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    code        VARCHAR(50)  NOT NULL UNIQUE COMMENT '产品编码',
    name        VARCHAR(200) NOT NULL COMMENT '产品名称',
    spec        VARCHAR(500)          COMMENT '规格型号',
    unit        VARCHAR(20)  NOT NULL DEFAULT '个' COMMENT '单位',
    status      TINYINT      NOT NULL DEFAULT 1  COMMENT '状态:1启用 0停用',
    version     INT          NOT NULL DEFAULT 1  COMMENT '乐观锁版本号',
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    operator    VARCHAR(50)          COMMENT '最后操作人'
) COMMENT '产品档案';

-- 零件/物料表
CREATE TABLE IF NOT EXISTS parts (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    code        VARCHAR(50)  NOT NULL UNIQUE COMMENT '零件编码',
    name        VARCHAR(200) NOT NULL COMMENT '零件名称',
    spec        VARCHAR(500)          COMMENT '规格型号',
    unit        VARCHAR(20)  NOT NULL DEFAULT '个' COMMENT '单位',
    part_type   VARCHAR(50)          COMMENT '零件分类(电子/五金/塑料/包装/其他)',
    stock_qty   DECIMAL(12,2) NOT NULL DEFAULT 0 COMMENT '当前库存数量',
    status      TINYINT      NOT NULL DEFAULT 1  COMMENT '状态:1启用 0停用',
    version     INT          NOT NULL DEFAULT 1,
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    operator    VARCHAR(50)
) COMMENT '零件/物料档案';

-- BOM明细表 (产品与零件的对应关系)
CREATE TABLE IF NOT EXISTS bom_items (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    product_id  BIGINT       NOT NULL COMMENT '产品ID',
    part_id     BIGINT       NOT NULL COMMENT '零件ID',
    quantity    DECIMAL(10,2) NOT NULL COMMENT '单台用量',
    loss_rate   DECIMAL(5,2) NOT NULL DEFAULT 0 COMMENT '损耗率(%)',
    remark      VARCHAR(500)          COMMENT '备注',
    version     INT          NOT NULL DEFAULT 1,
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    operator    VARCHAR(50),
    FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE,
    FOREIGN KEY (part_id)    REFERENCES parts(id),
    UNIQUE KEY uk_product_part (product_id, part_id)
) COMMENT 'BOM明细-产品零件对应关系';

-- 生产批次表
CREATE TABLE IF NOT EXISTS product_batches (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    batch_no        VARCHAR(100) NOT NULL UNIQUE COMMENT '生产批次号',
    product_id      BIGINT       NOT NULL COMMENT '产品ID',
    plan_qty        INT          NOT NULL COMMENT '计划数量',
    produced_qty    INT          NOT NULL DEFAULT 0 COMMENT '已完成数量',
    status          TINYINT      NOT NULL DEFAULT 0 COMMENT '状态:0待生产 1生产中 2已完成 3已暂停',
    version         INT          NOT NULL DEFAULT 1,
    created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    operator        VARCHAR(50),
    FOREIGN KEY (product_id) REFERENCES products(id)
) COMMENT '生产批次';

-- 批次追溯记录 (某批次产品用了哪些批次的零件)
CREATE TABLE IF NOT EXISTS batch_trace (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    batch_id        BIGINT       NOT NULL COMMENT '产品批次ID',
    part_id         BIGINT       NOT NULL COMMENT '零件ID',
    part_batch_no   VARCHAR(100)          COMMENT '零件批次号/炉号',
    used_qty        DECIMAL(10,2) NOT NULL COMMENT '使用数量',
    supplier        VARCHAR(200)          COMMENT '供应商名称',
    created_at      DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    operator        VARCHAR(50),
    FOREIGN KEY (batch_id) REFERENCES product_batches(id),
    FOREIGN KEY (part_id)  REFERENCES parts(id)
) COMMENT '批次追溯记录';

-- 审计日志表 (记录所有关键表的变更)
CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    table_name  VARCHAR(100) NOT NULL COMMENT '操作表名',
    record_id   BIGINT       NOT NULL COMMENT '记录ID',
    action      VARCHAR(20)  NOT NULL COMMENT 'INSERT/UPDATE/DELETE',
    old_data    JSON                  COMMENT '变更前数据',
    new_data    JSON                  COMMENT '变更后数据',
    operator    VARCHAR(50)           COMMENT '操作人',
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_table_record (table_name, record_id),
    INDEX idx_created (created_at)
) COMMENT '审计日志-记录所有数据变更';

-- 索引
CREATE INDEX idx_products_status ON products(status);
CREATE INDEX idx_parts_status ON parts(status);
CREATE INDEX idx_parts_type ON parts(part_type);
CREATE INDEX idx_bom_product ON bom_items(product_id);
CREATE INDEX idx_bom_part ON bom_items(part_id);
CREATE INDEX idx_batches_product ON product_batches(product_id);
CREATE INDEX idx_batches_status ON product_batches(status);
CREATE INDEX idx_trace_batch ON batch_trace(batch_id);
CREATE INDEX idx_trace_part ON batch_trace(part_id);

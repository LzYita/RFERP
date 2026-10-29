package service

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"app/internal/auth"
	"app/internal/config"
	"app/internal/model"
	"app/internal/paths"
	"app/internal/repository"
	"app/internal/usecase"
)

// Service 实现 usecase.Applications。业务规则在此；库方言不在此。
type Service struct {
	repo      repository.Store
	snapshots usecase.SnapshotPort
	dsn       string
	cfg       *config.Config
}

// 断言：满足全部用例接口，防止漂移。
var (
	_ usecase.Identity   = (*Service)(nil)
	_ usecase.Catalog    = (*Service)(nil)
	_ usecase.Inventory  = (*Service)(nil)
	_ usecase.Production = (*Service)(nil)
	_ usecase.Trace      = (*Service)(nil)
	_ usecase.Audit      = (*Service)(nil)
	_ usecase.Stats      = (*Service)(nil)
	_ usecase.Backup     = (*Service)(nil)
)

func New(repo repository.Store, dsn string, backupTool string, cfg *config.Config) *Service {
	return &Service{
		repo:      repo,
		snapshots: newMySQLSnapshotPort(dsn, backupTool),
		dsn:       dsn,
		cfg:       cfg,
	}
}

// NewWithSnapshot assembles Service with an explicit SnapshotPort (SQLite / tests).
func NewWithSnapshot(repo repository.Store, snapshots usecase.SnapshotPort, cfg *config.Config) *Service {
	return &Service{
		repo:      repo,
		snapshots: snapshots,
		cfg:       cfg,
	}
}

const (
	maxLossRate     = 100.0
	quantityScale   = 100.0
	quantityEpsilon = 1e-9

	// batchStatusPending 是新建批次的唯一合法初始状态。状态字面量在 Go 侧
	// 至今没有命名常量，散落在本文件多处；这里先把「新建」这一处命名，
	// 其余待后续统一（见 docs/plans 的 Round 4 记录）。
	batchStatusPending = 0
	// batchStatusCompleted 是本文件里另一个有语义含义的状态字面量：
	// 它同时是"扣减库存"的触发点与"不再计入在产占用"的边界。
	batchStatusCompleted = 2
)

func validatePlanQty(qty int) error {
	if qty <= 0 {
		return fmt.Errorf("计划数量必须大于0")
	}
	return nil
}

func validatePositiveQuantity(label string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return fmt.Errorf("%s必须是大于0的有限数值", label)
	}
	if !representableQuantity(value) {
		return fmt.Errorf("%s最多支持两位小数", label)
	}
	return nil
}

func validateNonNegativeQuantity(label string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("%s必须是非负有限数值", label)
	}
	if !representableQuantity(value) {
		return fmt.Errorf("%s最多支持两位小数", label)
	}
	return nil
}

func representableQuantity(value float64) bool {
	scaled := value * quantityScale
	return !math.IsInf(scaled, 0) && math.Abs(scaled-math.Round(scaled)) < quantityEpsilon
}

func quantizeQuantity(value float64) float64 {
	return math.Round(value*quantityScale) / quantityScale
}

func validateBOMItem(item *model.BOMItem) error {
	if item == nil {
		return fmt.Errorf("BOM零件不能为空")
	}
	if item.UseMode != 0 && item.UseMode != 1 {
		return fmt.Errorf("BOM用量模式不合法")
	}
	if item.Replaceable != 0 && item.Replaceable != 1 {
		return fmt.Errorf("BOM可替换标记不合法")
	}
	if err := validatePositiveQuantity("BOM用量", item.Quantity); err != nil {
		return err
	}
	if math.IsNaN(item.LossRate) || math.IsInf(item.LossRate, 0) || item.LossRate < 0 || item.LossRate > maxLossRate || !representableQuantity(item.LossRate) {
		return fmt.Errorf("损耗率必须在0到%.0f之间", maxLossRate)
	}
	return nil
}

func validatePartQuantities(p *model.Part) error {
	if p == nil {
		return fmt.Errorf("零件不能为空")
	}
	if err := validateNonNegativeQuantity("库存数量", p.StockQty); err != nil {
		return err
	}
	return validateNonNegativeQuantity("预警库存", p.WarnQty)
}

// DataDir 返回当前数据（备份/导出）目录。
func (s *Service) DataDir() string {
	return paths.DataDir()
}

// SetDataDir 修改数据目录：创建目录、持久化到配置、并更新运行时路径。
func (s *Service) SetDataDir(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("目录不能为空")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	if s.cfg != nil {
		s.cfg.DataDir = dir
		if err := s.cfg.Save(); err != nil {
			return fmt.Errorf("保存配置失败: %w", err)
		}
	}
	paths.SetDataDir(dir)
	return nil
}

// ---- 辅助: 审计日志 ----

type auditEntry struct {
	TableName string
	RecordID  int64
	Action    string
	OldData   any
	NewData   any
	Operator  string
}

func (s *Service) writeAudit(e auditEntry) error {
	oldJSON, err := toJSON(e.OldData)
	if err != nil {
		return fmt.Errorf("marshal audit old data: %w", err)
	}
	newJSON, err := toJSON(e.NewData)
	if err != nil {
		return fmt.Errorf("marshal audit new data: %w", err)
	}
	if err := s.repo.CreateAuditLog(&model.AuditLog{
		TableName: e.TableName,
		RecordID:  e.RecordID,
		Action:    e.Action,
		OldData:   oldJSON,
		NewData:   newJSON,
		Operator:  &e.Operator,
	}); err != nil {
		return fmt.Errorf("write audit %s/%d: %w", e.TableName, e.RecordID, err)
	}
	return nil
}

func (s *Service) writeAuditTx(tx repository.TxOps, e auditEntry) error {
	oldJSON, err := toJSON(e.OldData)
	if err != nil {
		return fmt.Errorf("marshal audit old data: %w", err)
	}
	newJSON, err := toJSON(e.NewData)
	if err != nil {
		return fmt.Errorf("marshal audit new data: %w", err)
	}
	if err := tx.CreateAuditLog(&model.AuditLog{
		TableName: e.TableName,
		RecordID:  e.RecordID,
		Action:    e.Action,
		OldData:   oldJSON,
		NewData:   newJSON,
		Operator:  &e.Operator,
	}); err != nil {
		return fmt.Errorf("write audit %s/%d: %w", e.TableName, e.RecordID, err)
	}
	return nil
}

func toJSON(v any) (*map[string]any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---- 产品 ----

func (s *Service) CreateProduct(p *model.Product) (*model.Product, error) {
	if p == nil {
		return nil, fmt.Errorf("产品不能为空")
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	p.Version = 1
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		id, err := tx.CreateProduct(p)
		if err != nil {
			return fmt.Errorf("create product: %w", repository.TranslateError(err))
		}
		p.ID = id
		return s.writeAuditTx(tx, auditEntry{"products", id, "INSERT", nil, p, *p.Operator})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) GetProduct(id int64) (*model.Product, error) {
	return s.repo.GetProduct(id)
}

func (s *Service) ListProducts() ([]model.Product, error) {
	return s.repo.ListProducts()
}

func (s *Service) UpdateProduct(p *model.Product) (*model.Product, error) {
	if p == nil {
		return nil, fmt.Errorf("产品不能为空")
	}
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		old, err := tx.GetProductForUpdate(p.ID)
		if err != nil {
			return err
		}
		if old == nil {
			return fmt.Errorf("product not found")
		}
		affected, err := tx.UpdateProduct(p)
		if err != nil {
			return fmt.Errorf("update product: %w", err)
		}
		if affected == 0 {
			return repository.OptimisticLockError(err)
		}
		p.Version = old.Version + 1
		return s.writeAuditTx(tx, auditEntry{"products", p.ID, "UPDATE", old, p, *p.Operator})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) DeleteProduct(id int64, operator string) error {
	return s.repo.WithTx(func(tx repository.TxOps) error {
		old, err := tx.GetProductForUpdate(id)
		if err != nil {
			return err
		}
		if old == nil {
			return fmt.Errorf("product not found")
		}
		if err := tx.DeleteProduct(id); err != nil {
			return err
		}
		return s.writeAuditTx(tx, auditEntry{"products", id, "DELETE", old, nil, operator})
	})
}

// ---- 零件 ----

func (s *Service) CreatePart(p *model.Part) (*model.Part, error) {
	// 新零件一律从 0 库存起步（2026-10 起）：数量只能经「入库 / 盘点」变动，
	// 每笔都有批次号与审计。前端已去掉输入框；这里再拦一层，防止 API 直连
	// 带着库存建零件、让入库台账从第一笔起就对不上。
	if p.StockQty != 0 {
		return nil, fmt.Errorf("新零件不能带库存创建，请创建后通过「入库」添加")
	}
	if err := validatePartQuantities(p); err != nil {
		return nil, err
	}
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		id, err := tx.CreatePart(p)
		if err != nil {
			return fmt.Errorf("create part: %w", repository.TranslateError(err))
		}
		p.ID = id
		return s.writeAuditTx(tx, auditEntry{"parts", id, "INSERT", nil, p, *p.Operator})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) GetPart(id int64) (*model.Part, error) {
	return s.repo.GetPart(id)
}

func (s *Service) ListParts() ([]model.Part, error) {
	return s.repo.ListParts()
}

func (s *Service) UpdatePart(p *model.Part) (*model.Part, error) {
	if err := validatePartQuantities(p); err != nil {
		return nil, err
	}
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		old, err := tx.GetPartForUpdate(p.ID)
		if err != nil {
			return err
		}
		if old == nil {
			return fmt.Errorf("part not found")
		}
		// 库存在编辑里被冻结：请求里带的 stock_qty 一律以库中现值为准。
		// PUT 是全量更新，前端会把原值原样带回；并发期间有人入库/盘点过的话，
		// 以库里的新值为准，绝不能让主数据编辑把库存改回旧数。
		p.StockQty = old.StockQty
		affected, err := tx.UpdatePart(p)
		if err != nil {
			return fmt.Errorf("update part: %w", err)
		}
		if affected == 0 {
			return repository.OptimisticLockError(err)
		}
		p.Version = old.Version + 1
		return s.writeAuditTx(tx, auditEntry{"parts", p.ID, "UPDATE", old, p, *p.Operator})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// formatPartBatchUses 把删除零件的引用整理成一句可读的话：最多列三批，
// 每批带用途；同一批次同一用途只出现一次。完整清单不进文案，避免撑爆对话框。
func formatPartBatchUses(uses []model.PartBatchUse) string {
	reasonText := map[string]string{
		"temp":     "临时零件",
		"skip":     "跳过零件",
		"consumed": "完成扣料",
	}
	seen := make(map[string]bool, len(uses))
	parts := make([]string, 0, 3)
	for _, u := range uses {
		key := u.BatchNo + "/" + u.Reason
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(parts) == 3 {
			parts = append(parts, "等")
			break
		}
		label := reasonText[u.Reason]
		if label == "" {
			label = u.Reason
		}
		parts = append(parts, fmt.Sprintf("%s（%s）", u.BatchNo, label))
	}
	return strings.Join(parts, "、")
}

func (s *Service) DeletePart(id int64, operator string) error {
	return s.repo.WithTx(func(tx repository.TxOps) error {
		old, err := tx.GetPartForUpdate(id)
		if err != nil {
			return err
		}
		if old == nil {
			return fmt.Errorf("part not found")
		}
		// 先查 BOM 引用：被引用就拒绝，并把「哪些产品用了它」带回界面。
		// 放在 DeletePart 之前而不是靠外键报错，是因为外键只会给出
		// "FOREIGN KEY constraint failed"，用户不知道去改哪个产品的 BOM。
		usedBy, err := tx.BOMProductsForPart(id)
		if err != nil {
			return fmt.Errorf("check BOM usage: %w", err)
		}
		if len(usedBy) > 0 {
			return &usecase.PartInBOMError{PartCode: old.Code, Products: usedBy}
		}
		// 第二道：追溯记录。同样是外键会拦（batch_trace.part_id），
		// 但用户看到 FOREIGN KEY constraint failed 时不知道是哪个界面挡的。
		traces, err := tx.TracesForPart(id)
		if err != nil {
			return fmt.Errorf("check production traces: %w", err)
		}
		if len(traces) > 0 {
			return &usecase.PartInTraceError{PartCode: old.Code, Batches: traces}
		}
		// 第三道：批次里的临时零件、跳过零件，以及完成扣料的冻结记录。
		// 这三张表都有 parts 外键且不级联；不在这里拦，用户只会得到
		// FOREIGN KEY constraint failed，不知道去哪个批次里解开。
		uses, err := tx.BatchUsesForPart(id)
		if err != nil {
			return fmt.Errorf("check batch usage: %w", err)
		}
		if len(uses) > 0 {
			return fmt.Errorf("零件 %s 仍被批次使用，无法删除：%s。请先在对应批次的「零件管理」中移除临时/跳过零件；完成扣料记录不能删除，如需保留历史请改为停用",
				old.Code, formatPartBatchUses(uses))
		}
		if err := tx.DeletePart(id); err != nil {
			return err
		}
		return s.writeAuditTx(tx, auditEntry{"parts", id, "DELETE", old, nil, operator})
	})
}

func (s *Service) StockIn(in usecase.StockInInput) (string, error) {
	if err := validatePositiveQuantity("入库数量", in.Qty); err != nil {
		return "", err
	}
	// 备注跟批次走：Trim 后为空就记 NULL，别让一串空格占着备注列。
	note := strings.TrimSpace(in.Note)
	if len([]rune(note)) > 200 {
		return "", fmt.Errorf("备注不能超过200字")
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	inQty := quantizeQuantity(in.Qty)
	var batchNo string
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		old, err := tx.GetPartForUpdate(in.PartID)
		if err != nil {
			return fmt.Errorf("get part: %w", err)
		}
		if old == nil {
			return fmt.Errorf("part not found")
		}
		newStock := quantizeQuantity(old.StockQty + inQty)
		if err := tx.UpdatePartStock(in.PartID, newStock); err != nil {
			return fmt.Errorf("update stock: %w", err)
		}
		bn, err := tx.AddPartInbound(in.PartID, inQty, notePtr)
		if err != nil {
			return fmt.Errorf("record inbound batch: %w", err)
		}
		batchNo = bn
		return s.writeAuditTx(tx, auditEntry{"parts", in.PartID, "STOCK_IN", old, map[string]any{
			"old_stock": quantizeQuantity(old.StockQty),
			"in_qty":    inQty,
			"new_stock": newStock,
			"batch_no":  bn,
		}, in.Operator})
	})
	if err != nil {
		return "", err
	}
	return batchNo, nil
}

// ListPartInbounds 零件的入库批次列表（零件详情页），并标出每个批次被
// 未完成批次预定了多少——"这批还剩 40，其中 30 是别的批次等着用的"。
func (s *Service) ListPartInbounds(partID int64) ([]model.PartInbound, error) {
	rows, err := s.repo.ListPartInbounds(partID)
	if err != nil {
		return nil, err
	}
	occ, err := s.repo.InboundOccupancy(partID)
	if err != nil {
		return nil, fmt.Errorf("load inbound occupancy: %w", err)
	}
	for i := range rows {
		holders := occ[rows[i].ID]
		if len(holders) == 0 {
			continue
		}
		total := 0.0
		for _, h := range holders {
			total += h.Qty
		}
		rows[i].Occupied = quantizeQuantity(total)
		rows[i].OccupiedBy = holders
	}
	return rows, nil
}

func (s *Service) AdjustStock(in usecase.AdjustStockInput) error {
	if err := validateNonNegativeQuantity("调整后库存", in.NewQty); err != nil {
		return err
	}
	newQty := quantizeQuantity(in.NewQty)
	return s.repo.WithTx(func(tx repository.TxOps) error {
		old, err := tx.GetPartForUpdate(in.PartID)
		if err != nil {
			return fmt.Errorf("get part: %w", err)
		}
		if old == nil {
			return fmt.Errorf("part not found")
		}
		// 总量与批次剩余必须同步改：差额为负时先进先出扣批次（不记生产
		// 扣料明细——盘亏不归属任何批次，撤销生产批次也不该把它加回来）；
		// 差额为正时补一笔「盘点盈余」批次，否则次日的批次视图会凭空
		// 多出没有来源的库存。
		diff := quantizeQuantity(newQty - old.StockQty)
		if diff < 0 {
			if err := tx.ReducePartInboundFIFO(in.PartID, -diff); err != nil {
				return fmt.Errorf("reduce inbound batches: %w", err)
			}
		} else if diff > 0 {
			note := "盘点盈余"
			if _, err := tx.AddPartInbound(in.PartID, diff, &note); err != nil {
				return fmt.Errorf("record surplus inbound: %w", err)
			}
		}
		if err := tx.UpdatePartStock(in.PartID, newQty); err != nil {
			return fmt.Errorf("adjust stock: %w", err)
		}
		return s.writeAuditTx(tx, auditEntry{"parts", in.PartID, "STOCK_ADJUST", old, map[string]any{
			"old_stock": quantizeQuantity(old.StockQty),
			"new_stock": newQty,
			"diff":      diff,
			"mode":      "总量盘点",
		}, in.Operator})
	})
}

// AdjustInbounds 逐批次盘点：把若干批次的剩余改成实盘数量。
//
// 与总量盘点的关系：两者改的是同一本账（parts.stock_qty = Σ remaining），
// 逐批次盘点按各批差额之和调整总量，因此不需要再单独填总量。
// 一次提交多批是刻意的——现场就是一次数完所有批次再一起确认，
// 逐批提交会让中途失败后的账面停在"数了一半"的状态。
func (s *Service) AdjustInbounds(req usecase.AdjustInboundsInput) error {
	if len(req.Items) == 0 {
		return fmt.Errorf("没有要盘点的批次")
	}
	seen := make(map[int64]bool, len(req.Items))
	for _, item := range req.Items {
		if item.InboundID <= 0 {
			return fmt.Errorf("批次参数不合法")
		}
		if seen[item.InboundID] {
			return fmt.Errorf("同一批次出现了两次")
		}
		seen[item.InboundID] = true
		if err := validateNonNegativeQuantity("实盘数量", item.Remaining); err != nil {
			return err
		}
	}
	return s.repo.WithTx(func(tx repository.TxOps) error {
		part, err := tx.GetPartForUpdate(req.PartID)
		if err != nil {
			return fmt.Errorf("get part: %w", err)
		}
		if part == nil {
			return fmt.Errorf("part not found")
		}
		type change struct {
			inboundID int64
			batchNo   string
			oldVal    float64
			newVal    float64
		}
		changes := make([]change, 0, len(req.Items))
		var delta float64
		for _, item := range req.Items {
			batch, err := tx.GetInbound(item.InboundID)
			if err != nil {
				return fmt.Errorf("get inbound %d: %w", item.InboundID, err)
			}
			if batch == nil || batch.PartID != req.PartID {
				// 批次不存在，或不属于这个零件：拒绝而不是静默跳过，
				// 否则用户会以为"数的那批"已经记下了。
				return fmt.Errorf("批次 %d 不属于零件 %s，无法盘点", item.InboundID, part.Code)
			}
			newVal := quantizeQuantity(item.Remaining)
			changes = append(changes, change{batch.ID, batch.BatchNo, batch.Remaining, newVal})
			delta = quantizeQuantity(delta + newVal - batch.Remaining)
			if err := tx.SetInboundRemaining(batch.ID, newVal); err != nil {
				return fmt.Errorf("set inbound remaining: %w", err)
			}
		}
		newStock := quantizeQuantity(part.StockQty + delta)
		if newStock < 0 {
			return fmt.Errorf("盘后库存为负，请检查填写")
		}
		if err := tx.UpdatePartStock(req.PartID, newStock); err != nil {
			return fmt.Errorf("adjust stock: %w", err)
		}
		detail := make([]map[string]any, 0, len(changes))
		for _, c := range changes {
			detail = append(detail, map[string]any{
				"batch_no": c.batchNo,
				"old":      c.oldVal,
				"new":      c.newVal,
			})
		}
		return s.writeAuditTx(tx, auditEntry{"parts", req.PartID, "STOCK_ADJUST", part, map[string]any{
			"old_stock": quantizeQuantity(part.StockQty),
			"new_stock": newStock,
			"diff":      delta,
			"mode":      "逐批次盘点",
			"batches":   detail,
		}, req.Operator})
	})
}

// ---- BOM ----

func (s *Service) AddBOMItem(b *model.BOMItem) (*model.BOMItem, error) {
	if err := validateBOMItem(b); err != nil {
		return nil, err
	}
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		// 校验产品和零件存在，并锁定零件避免并发删除。
		prod, err := tx.GetProduct(b.ProductID)
		if err != nil {
			return err
		}
		if prod == nil {
			return fmt.Errorf("product not found")
		}
		part, err := tx.GetPartForUpdate(b.PartID)
		if err != nil {
			return err
		}
		if part == nil {
			return fmt.Errorf("part not found")
		}
		id, err := tx.CreateBOMItem(b)
		if err != nil {
			return fmt.Errorf("create bom: %w", err)
		}
		b.ID = id
		return s.writeAuditTx(tx, auditEntry{"bom_items", id, "INSERT", nil, b, *b.Operator})
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	return s.repo.GetBOMByProduct(productID)
}

// UpdateBOMItem 修改一条 BOM 明细的用量、用量模式、损耗率与「可替换」标记。
//
// 只有这四个字段可改：挂在哪个零件上与备注不在编辑范围内。
// 调用方传进来的 b 只带 id/version 与这四个值；其余字段从库里的旧行复制，
// 这样审计日志的 old/new diff 里不会出现"零件被改成自己"这类噪声。
func (s *Service) UpdateBOMItem(b *model.BOMItem) (*model.BOMItem, error) {
	if b == nil {
		return nil, fmt.Errorf("BOM零件不能为空")
	}
	if err := validateBOMItem(b); err != nil {
		return nil, err
	}
	var out *model.BOMItem
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		old, err := tx.GetBOMItemForUpdate(b.ID)
		if err != nil {
			return err
		}
		if old == nil {
			return fmt.Errorf("bom item not found")
		}
		next := *old
		next.Quantity = b.Quantity
		next.LossRate = b.LossRate
		next.UseMode = b.UseMode
		next.Replaceable = b.Replaceable
		next.Operator = b.Operator
		// 乐观锁的比对值必须来自**调用方**（它是客户端读到那一行时的版本），
		// 用库里的 old.Version 就永远相等，并发改动会被静默覆盖。
		next.Version = b.Version
		affected, err := tx.UpdateBOMItem(&next)
		if err != nil {
			return fmt.Errorf("update bom: %w", err)
		}
		if affected == 0 {
			return repository.OptimisticLockError(err)
		}
		if err := s.writeAuditTx(tx, auditEntry{"bom_items", next.ID, "UPDATE", old, &next, *b.Operator}); err != nil {
			return err
		}
		next.Version = old.Version + 1
		out = &next
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) RemoveBOMItem(id int64, operator string) error {
	return s.repo.WithTx(func(tx repository.TxOps) error {
		if err := tx.DeleteBOMItem(id); err != nil {
			return err
		}
		return s.writeAuditTx(tx, auditEntry{"bom_items", id, "DELETE", nil, nil, operator})
	})
}

// ---- 批次 ----

func (s *Service) CreateBatch(b *model.ProductBatch) (*model.ProductBatch, error) {
	if b == nil {
		return nil, fmt.Errorf("批次不能为空")
	}
	if err := validatePlanQty(b.PlanQty); err != nil {
		return nil, err
	}
	// 状态由服务端定，不接受客户端指定。
	//
	// 缺这一条时，任何有 Production.CreateBatch 权限的账号（**包括生产员，
	// 不是只有管理员**）都能 POST status=2 建出一个"已完成"批次：它不会触发
	// 任何库存扣减（扣减只发生在 UpdateBatchStatus 转到 2 的路径上），
	// consumption_recorded 停在 0，于是又永远撤销不掉
	//（批次缺少冻结的库存消耗记录，无法安全撤销）。等于凭空造出一批不耗料的
	// 完成记录，库存账实不符且无从纠正。
	//
	// 前端本来就恒发 status:0（web/src/lib/batch.ts），这里是第二道。
	b.Status = batchStatusPending
	err := s.repo.WithTx(func(tx repository.TxOps) error {
		prod, err := tx.GetProduct(b.ProductID)
		if err != nil {
			return err
		}
		if prod == nil {
			return fmt.Errorf("product not found")
		}
		id, err := tx.CreateBatch(b)
		if err != nil {
			return fmt.Errorf("create batch: %w", err)
		}
		b.ID = id
		auditData := map[string]any{
			"batch_no":     b.BatchNo,
			"product_id":   b.ProductID,
			"product_code": prod.Code,
			"product_name": prod.Name,
			"plan_qty":     b.PlanQty,
			"customer":     b.Customer,
		}
		return s.writeAuditTx(tx, auditEntry{"product_batches", id, "INSERT", nil, auditData, *b.Operator})
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (s *Service) ListBatches() ([]model.ProductBatch, error) {
	return s.repo.ListBatches()
}

func (s *Service) UpdateBatchStatus(in usecase.UpdateBatchStatusInput) error {
	id, status, operator := in.ID, in.Status, in.Operator
	// 占用来源必须在事务**外**取：SQLite 适配器是单连接，事务持有连接时
	// 再发一条查询会直接把自己等死（实测过：整条用例挂到超时）。
	// 它只用于缺料文案的补充说明，事务外的快照足够。
	var others map[int64][]usecase.ProjectionHolder
	if status == batchStatusCompleted {
		others = s.otherCommittedForHint(id)
	}
	return s.repo.WithTx(func(tx repository.TxOps) error {
		batch, err := tx.GetBatchForUpdate(id)
		if err != nil {
			return fmt.Errorf("get batch: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 4 {
			return fmt.Errorf("已撤销的批次不能更改状态")
		}
		if status < 0 || status > 3 {
			return fmt.Errorf("状态 %d 不允许通过 UpdateBatchStatus 修改；撤销批次请使用 RevokeBatch", status)
		}
		if batch.Status == 2 {
			return fmt.Errorf("已完成的批次不能更改状态；如需撤销请使用 RevokeBatch")
		}

		// 完成生产 → 自动按BOM扣减库存
		if status == 2 {
			if err := validatePlanQty(batch.PlanQty); err != nil {
				return err
			}
			bom, err := tx.GetBOMByProduct(batch.ProductID)
			if err != nil {
				return fmt.Errorf("get bom: %w", err)
			}
			for i := range bom {
				if err := validateBOMItem(&bom[i]); err != nil {
					return fmt.Errorf("invalid bom item %d: %w", bom[i].ID, err)
				}
			}
			skipParts, err := tx.GetSkippedParts(id)
			if err != nil {
				return fmt.Errorf("get skipped parts: %w", err)
			}
			skipMap := make(map[int64]bool)
			for _, pid := range skipParts {
				skipMap[pid] = true
			}
			// 同一零件可能同时来自 BOM 与临时零件：先按零件汇总再查库存、扣减。
			// 逐条扣会让两条扣减都基于同一份"扣之前"的库存，后一条把前一条的
			// 结果覆盖掉——库存扣少了，撤销时却按两条记录回退，账实不符。
			// 汇总口径也与投影一致（batchConsumption 本来就是按零件合并的）。
			type need struct {
				part    *model.Part
				qty     float64
				bomQty  float64
				tempQty float64
			}
			needs := make(map[int64]*need)
			var order []int64
			partsByID := make(map[int64]*model.Part)
			getPart := func(partID int64) (*model.Part, error) {
				if p, ok := partsByID[partID]; ok {
					return p, nil
				}
				p, err := tx.GetPartForUpdate(partID)
				if err != nil {
					return nil, err
				}
				if p != nil {
					partsByID[partID] = p
				}
				return p, nil
			}
			addNeed := func(part *model.Part, partID int64, qty float64, fromTemp bool) {
				n := needs[partID]
				if n == nil {
					n = &need{part: part}
					needs[partID] = n
					order = append(order, partID)
				}
				n.qty = quantizeQuantity(n.qty + qty)
				if fromTemp {
					n.tempQty = quantizeQuantity(n.tempQty + qty)
				} else {
					n.bomQty = quantizeQuantity(n.bomQty + qty)
				}
			}

			// 先按 BOM 算出每个零件应扣多少，并**一次性列出全部缺料**再决定是否扣减。
			//
			// 原来这里是"扣到库存为 0"（actualDeduct = min(请求, 现有)），库存不够
			// 也把批次标记成已完成、接口返回成功——账面库存归零、批次齐套，账实不符
			// 且调用方毫无察觉。唯一的痕迹是审计里 requested_deduct 与 deduct 不等，
			// 而没人会去翻审计。
			//
			// 现在缺料即拒绝完成（与创建批次时的缺料拦截同一语义，Fyne
			// internal/ui/batch.go:364 也是中止而非少扣）。一次列全是因为只报第一个
			// 会让人反复提交、反复失败；列出全部才能一次补齐。
			for _, item := range bom {
				if skipMap[item.PartID] {
					continue
				}
				part, err := getPart(item.PartID)
				if err != nil {
					return fmt.Errorf("get part %d: %w", item.PartID, err)
				}
				if part == nil {
					continue
				}
				addNeed(part, item.PartID, quantizeQuantity(bomConsume(batch.PlanQty, item)), false)
			}

			// 临时零件与本批 BOM 一起扣：投影已把它们算进占用，这里口径一致。
			tempParts, err := tx.GetTempParts(id)
			if err != nil {
				return fmt.Errorf("get temp parts: %w", err)
			}
			for _, tp := range tempParts {
				qty := quantizeQuantity(tp.Qty)
				if qty <= 0 {
					continue
				}
				part, err := getPart(tp.PartID)
				if err != nil {
					return fmt.Errorf("get part %d: %w", tp.PartID, err)
				}
				if part == nil {
					continue
				}
				addNeed(part, tp.PartID, qty, true)
			}

			shortages := make([]string, 0, len(needs))
			// 缺料时说明"库存还被哪些在产批次占着"：只报"当前 40"会让用户以为
			// 补 40 就够，而实际缺口可能出自别的批次已经把库存预定完了。
			// 取不到来源只是少一句解释，不改变"缺料即拒绝"这个结论。
			for _, partID := range order {
				n := needs[partID]
				if n.qty <= 0 || n.qty <= quantizeQuantity(n.part.StockQty) {
					continue
				}
				var line string
				switch {
				case n.bomQty > 0 && n.tempQty > 0:
					line = fmt.Sprintf("%s [%s]: BOM %.2f + 临时 %.2f，合计需要 %.2f，当前 %.2f",
						n.part.Name, n.part.Code, n.bomQty, n.tempQty, n.qty, quantizeQuantity(n.part.StockQty))
				case n.tempQty > 0:
					line = fmt.Sprintf("%s [%s]: 临时需要 %.2f，当前 %.2f",
						n.part.Name, n.part.Code, n.qty, quantizeQuantity(n.part.StockQty))
				default:
					line = fmt.Sprintf("%s [%s]: 需要 %.2f，当前 %.2f",
						n.part.Name, n.part.Code, n.qty, quantizeQuantity(n.part.StockQty))
				}
				if hs := others[partID]; len(hs) > 0 {
					line += "；另有在产批次占用 " + holderSummary(hs)
				}
				shortages = append(shortages, line)
			}
			if len(shortages) > 0 {
				return fmt.Errorf("以下零件库存不足，无法完成生产：\n%s\n请先入库补足，或调整用量/临时零件",
					strings.Join(shortages, "\n"))
			}

			for _, partID := range order {
				n := needs[partID]
				if n.qty <= 0 {
					continue
				}
				newStock := quantizeQuantity(n.part.StockQty - n.qty)
				if newStock < 0 {
					newStock = 0
				}
				if err := tx.UpdatePartStock(partID, newStock); err != nil {
					return fmt.Errorf("update stock for part %d: %w", partID, err)
				}
				// 总量减了，批次剩余也要跟着减：按本批投料记录里排好的批次
				// 顺序扣（记录投料时拖出来的先后），没排到的部分走先进先出，
				// 并逐笔记下"本批从哪笔入库扣了多少"，撤销时按原批次还回去。
				if err := tx.DrawPartInboundForBatch(partID, id, n.qty); err != nil {
					return fmt.Errorf("draw inbound batches for part %d: %w", partID, err)
				}
				// 一个零件一条消耗记录（BOM+临时已合并），撤销按此回退。
				if err := tx.CreateBatchConsumption(&model.BatchConsumption{
					BatchID:     id,
					PartID:      partID,
					ConsumedQty: n.qty,
				}); err != nil {
					return fmt.Errorf("record consumption for part %d: %w", partID, err)
				}
				detail := map[string]any{
					"old_stock":        quantizeQuantity(n.part.StockQty),
					"requested_deduct": n.qty,
					"deduct":           n.qty,
					"new_stock":        newStock,
					"batch_id":         id,
					"source":           "bom",
				}
				switch {
				case n.bomQty > 0 && n.tempQty > 0:
					detail["source"] = "bom+temp"
					detail["bom_qty"] = n.bomQty
					detail["temp_qty"] = n.tempQty
				case n.tempQty > 0:
					detail["source"] = "temp"
				}
				if err := s.writeAuditTx(tx, auditEntry{"parts", partID, "STOCK_DEDUCT", n.part, detail, operator}); err != nil {
					return err
				}
			}
			if err := tx.UpdateBatchProduced(id, batch.PlanQty); err != nil {
				return fmt.Errorf("update batch produced: %w", err)
			}
			if err := tx.MarkBatchConsumptionRecorded(id); err != nil {
				return fmt.Errorf("mark batch consumption recorded: %w", err)
			}
		}

		prod, err := tx.GetProduct(batch.ProductID)
		if err != nil {
			return fmt.Errorf("get product for batch audit: %w", err)
		}
		oldMap := map[string]any{
			"batch_no":     batch.BatchNo,
			"product_id":   batch.ProductID,
			"product_code": "",
			"product_name": "",
			"plan_qty":     batch.PlanQty,
			"status":       batch.Status,
			"customer":     nullStrSvc(batch.Customer),
		}
		if prod != nil {
			oldMap["product_code"] = prod.Code
			oldMap["product_name"] = prod.Name
		}
		affected, err := tx.UpdateBatchStatusFrom(id, status, operator, batch.Status)
		if err != nil {
			return fmt.Errorf("update batch status: %w", err)
		}
		if affected == 0 {
			return repository.ErrOptimisticLock
		}
		if err := s.writeAuditTx(tx, auditEntry{"product_batches", id, "UPDATE_STATUS", oldMap, map[string]any{"status": status}, operator}); err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) RevokeBatch(in usecase.RevokeBatchInput) error {
	id, operator := in.ID, in.Operator
	return s.repo.WithTx(func(tx repository.TxOps) error {
		batch, err := tx.GetBatchForUpdate(id)
		if err != nil {
			return fmt.Errorf("get batch: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 4 {
			return fmt.Errorf("批次已经撤销，不能重复撤销")
		}

		// 已完成批次：只按完成时冻结的实际消耗记录回退库存。
		if batch.Status == 2 {
			if batch.ConsumptionRecorded != 1 {
				return fmt.Errorf("批次缺少冻结的库存消耗记录，无法安全撤销")
			}
			consumptions, err := tx.ListBatchConsumptions(id)
			if err != nil {
				return fmt.Errorf("get batch consumptions: %w", err)
			}
			for _, consumption := range consumptions {
				if consumption.ConsumedQty <= 0 {
					continue
				}
				part, err := tx.GetPartForUpdate(consumption.PartID)
				if err != nil {
					return fmt.Errorf("get part %d: %w", consumption.PartID, err)
				}
				if part == nil {
					continue
				}
				newStock := quantizeQuantity(part.StockQty + consumption.ConsumedQty)
				if err := tx.UpdatePartStock(consumption.PartID, newStock); err != nil {
					return fmt.Errorf("update stock for part %d: %w", consumption.PartID, err)
				}
				// 批次剩余同样要还：按扣料明细还回原批次。v17 之前完成的批次
				// 没有明细，差额补一笔「批次撤销回补」，否则总量与批次剩余
				// 之和会永久对不上。
				restored, err := tx.RestorePartInboundDraws(id)
				if err != nil {
					return fmt.Errorf("restore inbound batches for part %d: %w", consumption.PartID, err)
				}
				if gap := quantizeQuantity(consumption.ConsumedQty - restored); gap > 0 {
					note := "批次撤销回补"
					if _, err := tx.AddPartInbound(consumption.PartID, gap, &note); err != nil {
						return fmt.Errorf("record restore inbound for part %d: %w", consumption.PartID, err)
					}
				}
				if err := s.writeAuditTx(tx, auditEntry{"parts", consumption.PartID, "STOCK_ADJUST", part, map[string]any{
					"old_stock": quantizeQuantity(part.StockQty),
					"new_stock": newStock,
					"diff":      quantizeQuantity(consumption.ConsumedQty),
					"batch_id":  id,
					"remark":    "批次撤销回退",
				}, operator}); err != nil {
					return err
				}
			}
		}

		affected, err := tx.UpdateBatchStatusFrom(id, 4, operator, batch.Status)
		if err != nil {
			return fmt.Errorf("update batch status: %w", err)
		}
		if affected == 0 {
			return repository.ErrOptimisticLock
		}
		if err := s.writeAuditTx(tx, auditEntry{"product_batches", id, "REVOKE", batch, map[string]any{
			"batch_no":     batch.BatchNo,
			"product_id":   batch.ProductID,
			"plan_qty":     batch.PlanQty,
			"produced_qty": batch.ProducedQty,
			"status":       4,
			"customer":     batch.Customer,
			"operator":     operator,
		}, operator}); err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) GetSkippedParts(batchID int64) ([]int64, error) {
	return s.repo.GetSkippedParts(batchID)
}

func (s *Service) ListTempParts(batchID int64) ([]model.BatchTempPart, error) {
	return s.repo.GetTempParts(batchID)
}

// AddTempPart 给批次添加（或改数量）一个临时零件。
//
// 库存检测与投影同一口径：stock − Σ(全部在产批次的 BOM+临时占用) − 新数量
// 为负就拒绝，并点名还占着库存的批次——这正是"两个批次合起来吃穿库存、
// 单看每批都够"要拦的场景。本批同零件已有的临时数量不算占用：
// 这次调用就是来改它的。检测在事务外取数（SQLite 单连接，事务内再查会
// 自锁），完成生产时事务内还会按最终库存再拒一次，所以这里的检测是
// "把不合法的填单挡在门口"，不是并发意义上的安全边界。
func (s *Service) AddTempPart(in usecase.AddTempPartInput) error {
	batchID, partID, qty := in.BatchID, in.PartID, in.Qty
	if err := validatePositiveQuantity("临时零件数量", qty); err != nil {
		return err
	}
	// 推导三件套要么都给要么都不给：半套多半是调用方写错了表单。
	derivation := in.UseMode != nil || in.Quantity != nil || in.LossRate != nil
	if derivation && (in.UseMode == nil || in.Quantity == nil || in.LossRate == nil) {
		return fmt.Errorf("使用方式、单位用量与损耗率必须同时提供")
	}
	if derivation {
		if *in.UseMode != 0 && *in.UseMode != 1 {
			return fmt.Errorf("临时零件使用方式不合法")
		}
		if err := validatePositiveQuantity("临时零件单位用量", *in.Quantity); err != nil {
			return err
		}
		lr := *in.LossRate
		if math.IsNaN(lr) || math.IsInf(lr, 0) || lr < 0 || lr > maxLossRate || !representableQuantity(lr) {
			return fmt.Errorf("损耗率必须在0到%.0f之间", maxLossRate)
		}
	}
	committed, holders, _, err := s.commitmentsAndPlans(0)
	if err != nil {
		return err
	}
	return s.repo.WithTx(func(tx repository.TxOps) error {
		batch, err := tx.GetBatchForUpdate(batchID)
		if err != nil {
			return fmt.Errorf("get batch: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 2 || batch.Status == 4 {
			return fmt.Errorf("已完成或已撤销的批次不能修改临时零件")
		}
		part, err := tx.GetPartForUpdate(partID)
		if err != nil {
			return fmt.Errorf("get part: %w", err)
		}
		if part == nil {
			return fmt.Errorf("part not found")
		}
		oldQty := 0.0
		olds, err := tx.GetTempParts(batchID)
		if err != nil {
			return fmt.Errorf("get temp parts: %w", err)
		}
		for _, o := range olds {
			if o.PartID == partID {
				oldQty = quantizeQuantity(o.Qty)
			}
		}
		need := quantizeQuantity(qty)
		after := quantizeQuantity(part.StockQty - committed[partID] + oldQty - need)
		if after < 0 {
			msg := fmt.Sprintf("%s [%s] 临时需要 %.2f，全部在产批次完成后将缺 %.2f",
				part.Name, part.Code, need, -after)
			if hs := holders[partID]; len(hs) > 0 {
				msg += "；另有在产批次占用 " + holderSummary(hs)
			}
			return fmt.Errorf("%s。请先入库补足或调小数量", msg)
		}
		tp := &model.BatchTempPart{BatchID: batchID, PartID: partID, Qty: need}
		if derivation {
			tp.UseMode, tp.Quantity, tp.LossRate = in.UseMode, in.Quantity, in.LossRate
		}
		if err := tx.AddTempPart(tp); err != nil {
			return fmt.Errorf("add temp part: %w", err)
		}
		return nil
	})
}

func (s *Service) RemoveTempPart(batchID, partID int64) error {
	return s.repo.WithTx(func(tx repository.TxOps) error {
		batch, err := tx.GetBatchForUpdate(batchID)
		if err != nil {
			return fmt.Errorf("get batch: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 2 || batch.Status == 4 {
			return fmt.Errorf("已完成或已撤销的批次不能修改临时零件")
		}
		if err := tx.RemoveTempPart(batchID, partID); err != nil {
			return fmt.Errorf("remove temp part: %w", err)
		}
		return nil
	})
}

// ---- 统计 ----

// StockDailyPoint 某天的进出库汇总
// 统计类型与用例层共享（D-013 可移植性：传输/用例类型不锁在业务包）。
type StockDailyPoint = usecase.StockDailyPoint
type PartStockStat = usecase.PartStockStat
type SupplierStockStat = usecase.SupplierStockStat
type CustomerStockStat = usecase.CustomerStockStat
type ProductStockStat = usecase.ProductStockStat
type StockStats = usecase.StockStats

// GetStockStats 统计近 days 天的进出库规律
func (s *Service) GetStockStats(days int) (*StockStats, error) {
	if days <= 0 {
		days = 30
	}
	end := time.Now()
	start := end.AddDate(0, 0, -days)

	logs, err := s.repo.ListStockLogsByDate(start, end, []string{"STOCK_IN", "STOCK_DEDUCT", "STOCK_ADJUST"})
	if err != nil {
		return nil, err
	}

	st := &StockStats{}
	dailyMap := make(map[string]*StockDailyPoint)
	partMap := make(map[string]*PartStockStat)
	supplierMap := make(map[string]*SupplierStockStat)
	customerMap := make(map[string]*CustomerStockStat)
	productMap := make(map[string]*ProductStockStat)

	key := func(code, name string) string { return code + "|" + name }

	// 出库关联批次，用于把零件消耗映射到客户/产品，并识别已撤销批次
	// （Issue #40：status=4 的批次其扣减与回退日志两端都不计入统计）
	batchIDSet := make(map[int64]bool)
	for _, l := range logs {
		if l.NewData == nil {
			continue
		}
		if l.Action != "STOCK_DEDUCT" && l.Action != "STOCK_ADJUST" {
			continue
		}
		if bid := int64(numVal((*l.NewData)["batch_id"])); bid > 0 {
			batchIDSet[bid] = true
		}
	}
	batchMap := make(map[int64]model.ProductBatch)
	revokedBatchSet := make(map[int64]bool)
	if len(batchIDSet) > 0 {
		batches, err := s.repo.ListBatches()
		if err != nil {
			return nil, fmt.Errorf("list batches for stock stats: %w", err)
		}
		for _, b := range batches {
			if batchIDSet[b.ID] {
				batchMap[b.ID] = b
				if b.Status == 4 {
					revokedBatchSet[b.ID] = true
				}
			}
		}
	}

	for _, l := range logs {
		// 已撤销批次视为未发生：其 STOCK_DEDUCT 与撤销回退 STOCK_ADJUST 一并跳过
		if l.NewData != nil && (l.Action == "STOCK_DEDUCT" || l.Action == "STOCK_ADJUST") {
			if bid := int64(numVal((*l.NewData)["batch_id"])); bid > 0 && revokedBatchSet[bid] {
				continue
			}
		}
		day := l.CreatedAt.Format("01-02")
		dp, ok := dailyMap[day]
		if !ok {
			dp = &StockDailyPoint{Date: day}
			dailyMap[day] = dp
		}

		// 零件信息在 old_data（STOCK_IN/DEDUCT 记录的是 part 对象）
		var code, name string
		var supplier string
		if l.OldData != nil {
			code, _ = (*l.OldData)["code"].(string)
			name, _ = (*l.OldData)["name"].(string)
			supplier, _ = (*l.OldData)["supplier"].(string)
		}

		var inQty, outQty float64
		switch l.Action {
		case "STOCK_IN":
			if l.NewData != nil {
				inQty = numVal((*l.NewData)["in_qty"])
			}
			dp.In += inQty
			st.TotalIn += inQty
			// 供应商入库（入库主要针对零件）
			if supplier != "" {
				sp, ok := supplierMap[supplier]
				if !ok {
					sp = &SupplierStockStat{Name: supplier}
					supplierMap[supplier] = sp
				}
				sp.In += inQty
			}
		case "STOCK_DEDUCT":
			if l.NewData != nil {
				outQty = numVal((*l.NewData)["deduct"])
			}
			dp.Out += outQty
			st.TotalOut += outQty
			// 零件出库 → 映射到客户与产品（通过批次）
			if l.NewData != nil {
				bid := int64(numVal((*l.NewData)["batch_id"]))
				if b, ok := batchMap[bid]; ok {
					cust := ""
					if b.Customer != nil {
						cust = *b.Customer
					}
					if cust != "" {
						cp, ok := customerMap[cust]
						if !ok {
							cp = &CustomerStockStat{Name: cust}
							customerMap[cust] = cp
						}
						cp.Out += outQty
					}
					pcode := ""
					pname := ""
					if b.ProductCode != nil {
						pcode = *b.ProductCode
					}
					if b.ProductName != nil {
						pname = *b.ProductName
					}
					if pcode != "" || pname != "" {
						pp, ok := productMap[key(pcode, pname)]
						if !ok {
							pp = &ProductStockStat{Code: pcode, Name: pname}
							productMap[key(pcode, pname)] = pp
						}
						pp.Out += outQty
						st.ProductOutTotal += outQty
					}
				}
			}
		case "STOCK_ADJUST":
			// 盘点调整：diff>0 视为入库，diff<0 视为出库
			if l.NewData != nil {
				diff := numVal((*l.NewData)["diff"])
				if diff > 0 {
					dp.In += diff
					st.TotalIn += diff
				} else {
					outQty = -diff
					dp.Out += outQty
					st.TotalOut += outQty
				}
			}
		}

		if code == "" && name == "" {
			continue
		}
		p, ok := partMap[key(code, name)]
		if !ok {
			p = &PartStockStat{Code: code, Name: name}
			partMap[key(code, name)] = p
		}
		if inQty > 0 {
			p.In += inQty
		}
		if outQty > 0 {
			p.Out += outQty
		}
	}

	// 补齐缺失日期（升序）
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i+1).Format("01-02")
		if _, ok := dailyMap[d]; !ok {
			dailyMap[d] = &StockDailyPoint{Date: d}
		}
	}
	for _, dp := range dailyMap {
		st.Days = append(st.Days, *dp)
	}
	sort.Slice(st.Days, func(i, j int) bool { return st.Days[i].Date < st.Days[j].Date })

	// 零件排行
	var ins, outs []PartStockStat
	for _, p := range partMap {
		if p.In > 0 {
			ins = append(ins, *p)
		}
		if p.Out > 0 {
			outs = append(outs, *p)
		}
	}
	sort.Slice(ins, func(i, j int) bool { return ins[i].In > ins[j].In })
	sort.Slice(outs, func(i, j int) bool { return outs[i].Out > outs[j].Out })
	if len(ins) > 10 {
		ins = ins[:10]
	}
	if len(outs) > 10 {
		outs = outs[:10]
	}
	st.TopIn = ins
	st.TopOut = outs

	// 供应商排行
	var supps []SupplierStockStat
	for _, sp := range supplierMap {
		supps = append(supps, *sp)
	}
	sort.Slice(supps, func(i, j int) bool { return supps[i].In > supps[j].In })
	if len(supps) > 10 {
		supps = supps[:10]
	}
	st.TopSuppliers = supps

	// 客户排行
	var custs []CustomerStockStat
	for _, cp := range customerMap {
		custs = append(custs, *cp)
	}
	sort.Slice(custs, func(i, j int) bool { return custs[i].Out > custs[j].Out })
	if len(custs) > 10 {
		custs = custs[:10]
	}
	st.TopCustomers = custs

	// 产品排行
	var prods []ProductStockStat
	for _, pp := range productMap {
		prods = append(prods, *pp)
	}
	sort.Slice(prods, func(i, j int) bool { return prods[i].Out > prods[j].Out })
	if len(prods) > 10 {
		prods = prods[:10]
	}
	st.TopProducts = prods

	return st, nil
}

func numVal(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f := 0.0
		_, _ = fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}

func (s *Service) AddSkipPart(batchID, partID int64) error {
	return s.repo.WithTx(func(tx repository.TxOps) error {
		batch, err := tx.GetBatchForUpdate(batchID)
		if err != nil {
			return fmt.Errorf("get batch: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 2 || batch.Status == 4 {
			return fmt.Errorf("已完成或已撤销的批次不能修改跳过零件")
		}
		if err := tx.AddSkipPart(batchID, partID); err != nil {
			return fmt.Errorf("add skipped part: %w", err)
		}
		return nil
	})
}

func (s *Service) RemoveSkipPart(batchID, partID int64) error {
	return s.repo.WithTx(func(tx repository.TxOps) error {
		batch, err := tx.GetBatchForUpdate(batchID)
		if err != nil {
			return fmt.Errorf("get batch: %w", err)
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 2 || batch.Status == 4 {
			return fmt.Errorf("已完成或已撤销的批次不能修改跳过零件")
		}
		if err := tx.RemoveSkipPart(batchID, partID); err != nil {
			return fmt.Errorf("remove skipped part: %w", err)
		}
		return nil
	})
}

// ---- 追溯 ----

func (s *Service) RecordTrace(t *model.BatchTrace) (*model.BatchTrace, error) {
	if t == nil {
		return nil, fmt.Errorf("投料记录不能为空")
	}
	if err := validatePositiveQuantity("投料数量", t.UsedQty); err != nil {
		return nil, err
	}
	id, err := s.repo.CreateTrace(t)
	if err != nil {
		return nil, fmt.Errorf("create trace: %w", err)
	}
	t.ID = id
	return t, nil
}

// RecordTraces 保存一批投料记录。
//
// v18 起语义是**按零件覆盖**：同一 (batch, part) 只会有一条记录，
// 再记录一次就是改，不是追加——否则追溯表里同一个零件会出现好几行
// 各说各的，用户分不清哪条是当前状态。批次分配随记录整体替换。
func (s *Service) RecordTraces(traces []*model.BatchTrace) error {
	if len(traces) == 0 {
		return nil
	}
	type key struct{ batchID, partID int64 }
	seen := make(map[key]bool, len(traces))
	for i, trace := range traces {
		if trace == nil {
			return fmt.Errorf("第%d条投料记录不能为空", i+1)
		}
		if trace.BatchID <= 0 || trace.PartID <= 0 {
			return fmt.Errorf("第%d条投料记录缺少批次或零件", i+1)
		}
		if err := validatePositiveQuantity("投料数量", trace.UsedQty); err != nil {
			return fmt.Errorf("第%d条投料记录无效: %w", i+1, err)
		}
		k := key{trace.BatchID, trace.PartID}
		if seen[k] {
			return fmt.Errorf("第%d条投料记录重复：同一批次同一零件只能有一条记录，多个批次请在同一行内调整顺序", i+1)
		}
		seen[k] = true

		var allocTotal float64
		allocSeen := make(map[int64]bool, len(trace.Allocations))
		for j, a := range trace.Allocations {
			if a.InboundID <= 0 {
				return fmt.Errorf("第%d条投料记录的第%d个批次分配缺少批次号", i+1, j+1)
			}
			if err := validatePositiveQuantity("批次分配数量", a.Qty); err != nil {
				return fmt.Errorf("第%d条投料记录的第%d个批次分配无效: %w", i+1, j+1, err)
			}
			if allocSeen[a.InboundID] {
				return fmt.Errorf("第%d条投料记录里同一个入库批次出现了两次", i+1)
			}
			allocSeen[a.InboundID] = true
			allocTotal += a.Qty
		}
		// 允许少量浮点误差；分配不足用量是合法的（余量完成时按先进先出兜底），
		// 超过用量一定是表单算错了。
		if allocTotal > trace.UsedQty+0.005 {
			return fmt.Errorf("第%d条投料记录的批次分配合计 %.2f 超过了用量 %.2f", i+1, allocTotal, trace.UsedQty)
		}
	}
	return s.repo.WithTx(func(tx repository.TxOps) error {
		for i, trace := range traces {
			// 供应商不由投料界面填写，直接取零件台账上的供应商：
			// 同一种料在不同批次里手填出两个供应商，追溯就没法当成同一件事看。
			part, err := tx.GetPartForUpdate(trace.PartID)
			if err != nil {
				return fmt.Errorf("第%d条投料记录读取零件失败: %w", i+1, err)
			}
			if part == nil {
				return fmt.Errorf("第%d条投料记录的零件不存在", i+1)
			}
			trace.Supplier = part.Supplier
			for _, a := range trace.Allocations {
				in, err := tx.GetInbound(a.InboundID)
				if err != nil {
					return fmt.Errorf("第%d条投料记录读取入库批次失败: %w", i+1, err)
				}
				if in == nil {
					return fmt.Errorf("第%d条投料记录的入库批次 #%d 不存在", i+1, a.InboundID)
				}
				if in.PartID != trace.PartID {
					return fmt.Errorf("第%d条投料记录的批次 %s 不属于该零件（请重新选择）", i+1, in.BatchNo)
				}
			}
			id, err := tx.ReplacePartTrace(trace)
			if err != nil {
				return fmt.Errorf("save trace %d: %w", i+1, err)
			}
			trace.ID = id
		}
		return nil
	})
}

// GetTraceByBatch 返回某批次的投料记录，并挂上每条记录的批次分配。
// 分配与记录分表读、在这里合：记录只有一条，分配可能有多条。
func (s *Service) GetTraceByBatch(batchID int64) ([]model.BatchTrace, error) {
	rows, err := s.repo.GetTraceByBatch(batchID)
	if err != nil {
		return nil, err
	}
	allocs, err := s.repo.ListBatchTraceAllocs(batchID)
	if err != nil {
		return nil, fmt.Errorf("load trace allocations: %w", err)
	}
	for i := range rows {
		rows[i].Allocations = allocs[rows[i].PartID]
	}
	return rows, nil
}

func (s *Service) TraceByProduct(batchNo string) ([]model.BatchTrace, error) {
	batchNo = strings.TrimSpace(batchNo)
	if batchNo == "" {
		return nil, fmt.Errorf("请输入产品批次号")
	}
	return s.repo.TraceByProduct(batchNo)
}

func (s *Service) TraceByPart(partCode string) ([]model.BatchTrace, error) {
	partCode = strings.TrimSpace(partCode)
	if partCode == "" {
		return nil, fmt.Errorf("请输入零件编码")
	}
	return s.repo.TraceByPart(partCode)
}

// ---- 审计 ----

func (s *Service) GetAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error) {
	return s.repo.ListAuditLogs(tableName, recordID)
}

func (s *Service) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) {
	return s.repo.ListRecentAuditLogs(limit)
}

// ---- 备份与导出 ----

// RestoreDatabase 见 restore.go（整库恢复：只回灌数据，保留系统表，操作前留副本）。

func (s *Service) BackupDatabase(saveDir string) (string, error) {
	return s.snapshots.Snapshot(saveDir)
}

func statusText(v int) string {
	if v == 1 {
		return "启用"
	}
	return "停用"
}

func batchStatusText(v int) string {
	switch v {
	case 0:
		return "待生产"
	case 1:
		return "生产中"
	case 2:
		return "已完成"
	case 3:
		return "已暂停"
	case 4:
		return "已撤销"
	default:
		return fmt.Sprintf("%d", v)
	}
}

func actionText(v string) string {
	switch v {
	case "INSERT":
		return "新增"
	case "UPDATE":
		return "修改"
	case "DELETE":
		return "删除"
	case "STOCK_IN":
		return "入库"
	case "STOCK_DEDUCT":
		return "出库"
	case "STOCK_ADJUST":
		return "盘点"
	case "UPDATE_STATUS":
		return "状态变更"
	default:
		return v
	}
}

func tableLabel(v string) string {
	switch v {
	case "products":
		return "产品"
	case "parts":
		return "零件"
	case "bom_items":
		return "BOM"
	case "product_batches":
		return "批次"
	case "batch_trace":
		return "批次追溯"
	case "batch_skip_parts":
		return "跳过零件"
	default:
		return v
	}
}

func auditSummary(l model.AuditLog) string {
	t := l.TableName
	a := l.Action
	if t == "product_batches" && a == "UPDATE_STATUS" {
		var batchNo string
		var status int
		if l.OldData != nil {
			batchNo, _ = (*l.OldData)["batch_no"].(string)
		}
		if l.NewData != nil {
			s, _ := (*l.NewData)["status"].(float64)
			status = int(s)
		}
		return fmt.Sprintf("批次 %s → %s", batchNo, batchStatusText(status))
	}
	if t == "product_batches" && a == "INSERT" && l.NewData != nil {
		d := *l.NewData
		bn, _ := d["batch_no"].(string)
		pn, _ := d["product_name"].(string)
		pq, _ := d["plan_qty"].(float64)
		return fmt.Sprintf("新建批次 %s [%s] 计划%.0f", bn, pn, pq)
	}
	if a == "STOCK_IN" && l.NewData != nil {
		d := *l.NewData
		q, _ := d["in_qty"].(float64)
		ns, _ := d["new_stock"].(float64)
		return fmt.Sprintf("入库 %.0f，当前 %.0f", q, ns)
	}
	if a == "STOCK_DEDUCT" && l.NewData != nil {
		d := *l.NewData
		q, _ := d["deduct"].(float64)
		os, _ := d["old_stock"].(float64)
		ns, _ := d["new_stock"].(float64)
		return fmt.Sprintf("生产消耗 %.0f，%.0f→%.0f", q, os, ns)
	}
	if a == "STOCK_ADJUST" && l.NewData != nil {
		d := *l.NewData
		os, _ := d["old_stock"].(float64)
		ns, _ := d["new_stock"].(float64)
		return fmt.Sprintf("盘点 %.0f → %.0f", os, ns)
	}
	if t == "batch_trace" && a == "INSERT" && l.NewData != nil {
		d := *l.NewData
		pbn, _ := d["part_batch_no"].(string)
		q, _ := d["used_qty"].(float64)
		if pbn != "" {
			return fmt.Sprintf("记录投料 %s，用量 %.0f", pbn, q)
		}
		return fmt.Sprintf("记录投料，用量 %.0f", q)
	}
	if a == "INSERT" && l.NewData != nil {
		d := *l.NewData
		if n, ok := d["name"].(string); ok {
			return fmt.Sprintf("新增 %s", n)
		}
		if c, ok := d["code"].(string); ok {
			return fmt.Sprintf("新增 %s", c)
		}
	}
	if a == "DELETE" && l.OldData != nil {
		d := *l.OldData
		if n, ok := d["name"].(string); ok {
			return fmt.Sprintf("删除 %s", n)
		}
		if c, ok := d["code"].(string); ok {
			return fmt.Sprintf("删除 %s", c)
		}
	}
	if a == "UPDATE" && l.NewData != nil {
		if n, ok := (*l.NewData)["name"].(string); ok {
			return fmt.Sprintf("修改 %s", n)
		}
	}
	if l.NewData != nil {
		for _, k := range []string{"code", "name", "batch_no"} {
			if v, ok := (*l.NewData)[k].(string); ok && v != "" {
				return v
			}
		}
	}
	if l.OldData != nil {
		for _, k := range []string{"code", "name", "batch_no"} {
			if v, ok := (*l.OldData)[k].(string); ok && v != "" {
				return v
			}
		}
	}
	return tableLabel(t) + "操作"
}

func createCSVOutput(path string) (io.WriteCloser, error) {
	return os.Create(path)
}

func writeCSVFile(path string, header []string, rows [][]string, create func(string) (io.WriteCloser, error)) (retErr error) {
	f, err := create(path)
	if err != nil {
		return fmt.Errorf("create csv file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close csv file: %w", closeErr))
		}
		if retErr != nil {
			if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
				retErr = errors.Join(retErr, fmt.Errorf("remove incomplete csv file: %w", removeErr))
			}
		}
	}()

	bom := []byte{0xEF, 0xBB, 0xBF}
	if n, err := f.Write(bom); err != nil {
		return fmt.Errorf("write csv BOM: %w", err)
	} else if n != len(bom) {
		return fmt.Errorf("write csv BOM: %w", io.ErrShortWrite)
	}

	w := csv.NewWriter(f)
	if err := w.Write(header); err != nil {
		return fmt.Errorf("write csv header: %w", err)
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("flush csv: %w", err)
	}
	return nil
}

func writeCSVFileAtomic(path string, header []string, rows [][]string) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary csv file: %w", err)
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close temporary csv file: %w", err)
	}
	if err := writeCSVFile(tempPath, header, rows, createCSVOutput); err != nil {
		// writeCSVFile removes its output on failure; also drop the CreateTemp
		// placeholder if opening failed before that cleanup ran.
		_ = os.Remove(tempPath)
		return err
	}
	return finalizeCSVExclusive(tempPath, path)
}

// finalizeCSVExclusive publishes tempPath as path only when path does not
// already exist. It never replaces an existing export, including when two
// exports finalize at the same time.
func finalizeCSVExclusive(tempPath, path string) error {
	if err := os.Link(tempPath, path); err == nil {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("finalize csv file: remove temporary file: %w", err)
		}
		return nil
	} else if os.IsExist(err) {
		_ = os.Remove(tempPath)
		return fmt.Errorf("finalize csv file: target already exists")
	}

	// Hard links can be unavailable on some filesystems. Fall back to an
	// exclusive create and copy, still refusing to overwrite the target.
	return finalizeCSVByCopyExclusive(tempPath, path)
}

// Close the source before removing it: Windows keeps open files locked for deletion.
func finalizeCSVByCopyExclusive(tempPath, path string) (result error) {
	defer func() {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) && result == nil {
			result = fmt.Errorf("finalize csv file: remove temporary file: %w", err)
		}
	}()
	src, err := os.Open(tempPath)
	if err != nil {
		return fmt.Errorf("finalize csv file: open temporary file: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("finalize csv file: target already exists")
		}
		return fmt.Errorf("finalize csv file: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(path)
		return fmt.Errorf("finalize csv file: copy: %w", err)
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("finalize csv file: close target: %w", err)
	}
	return nil
}

func (s *Service) ExportAuditLogCSV(startDate, endDate time.Time, filePath string) (int, error) {
	endDate = endDate.Add(24 * time.Hour)
	logs, err := s.repo.ListAuditLogsByDate(startDate, endDate)
	if err != nil {
		return 0, fmt.Errorf("query audit logs: %w", err)
	}
	rows := make([][]string, 0, len(logs))
	for _, log := range logs {
		op := ""
		if log.Operator != nil {
			op = *log.Operator
		}
		rows = append(rows, []string{
			log.CreatedAt.Format("2006-01-02 15:04:05"),
			actionText(log.Action),
			tableLabel(log.TableName),
			auditSummary(log),
			op,
		})
	}
	if err := writeCSVFileAtomic(filePath, []string{"时间", "操作", "对象", "内容摘要", "操作人"}, rows); err != nil {
		return 0, fmt.Errorf("write audit csv: %w", err)
	}
	return len(logs), nil
}

func (s *Service) ExportAllDataCSV(saveDir string) (map[string]string, string, error) {
	return s.exportAllDataCSV(saveDir, time.Now())
}

func (s *Service) exportAllDataCSV(saveDir string, at time.Time) (map[string]string, string, error) {
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create export parent: %w", err)
	}
	// Keep readable names, but only clean up a directory this call created exclusively.
	base := filepath.Join(saveDir, at.Format("20060102_150405"))
	subDir := base
	for i := 1; ; i++ {
		err := os.Mkdir(subDir, 0o755)
		if err == nil {
			break
		}
		if !os.IsExist(err) || i >= 99 {
			return nil, "", fmt.Errorf("create export dir: %w", err)
		}
		subDir = fmt.Sprintf("%s-%d", base, i+1)
	}
	files := make(map[string]string)
	// shortcut: cleanup assumes no parent replacement; use rooted handles for hostile local writers.
	failExport := func(err error) (map[string]string, string, error) {
		if cleanupErr := os.RemoveAll(subDir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove incomplete export directory: %w", cleanupErr))
		}
		return nil, "", err
	}

	writeCSV := func(name string, header []string, rows [][]string) (string, error) {
		p := filepath.Join(subDir, name)
		if err := writeCSVFileAtomic(p, header, rows); err != nil {
			return "", err
		}
		return p, nil
	}
	addCSV := func(key, name string, header []string, rows [][]string) error {
		p, err := writeCSV(name, header, rows)
		if err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		files[key] = p
		return nil
	}

	// build lookup maps
	products, err := s.repo.ListProducts()
	if err != nil {
		return failExport(fmt.Errorf("query products: %w", err))
	}
	prodMap := make(map[int64]model.Product)
	for _, p := range products {
		prodMap[p.ID] = p
	}
	parts, err := s.repo.ListParts()
	if err != nil {
		return failExport(fmt.Errorf("query parts: %w", err))
	}
	partMap := make(map[int64]model.Part)
	for _, p := range parts {
		partMap[p.ID] = p
	}
	batches, err := s.repo.ListBatches()
	if err != nil {
		return failExport(fmt.Errorf("query batches: %w", err))
	}
	batchMap := make(map[int64]model.ProductBatch)
	for _, b := range batches {
		batchMap[b.ID] = b
	}

	// products
	var prodRows [][]string
	for _, p := range products {
		st := "启用"
		if p.Status != 1 {
			st = "停用"
		}
		prodRows = append(prodRows, []string{
			p.Code, p.Name, nullStrSvc(p.Spec),
			p.Unit, st, nullStrSvc(p.Operator),
			p.CreatedAt.Format("2006-01-02"),
		})
	}
	if err := addCSV("products", "products.csv",
		[]string{"编码", "名称", "规格", "单位", "状态", "操作人", "创建日期"},
		prodRows); err != nil {
		return failExport(err)
	}

	// parts
	var partRows [][]string
	for _, p := range parts {
		st := "启用"
		if p.Status != 1 {
			st = "停用"
		}
		warn := ""
		if p.WarnQty > 0 {
			warn = fmt.Sprintf("%.2f", p.WarnQty)
		}
		partRows = append(partRows, []string{
			p.Code, p.Name, nullStrSvc(p.Spec),
			nullStrSvc(p.PartType), fmt.Sprintf("%.2f", p.StockQty), warn, st,
			nullStrSvc(p.Supplier), nullStrSvc(p.Operator),
		})
	}
	if err := addCSV("parts", "parts.csv",
		[]string{"编码", "名称", "规格", "分类", "库存", "预警库存", "状态", "供应商", "操作人"},
		partRows); err != nil {
		return failExport(err)
	}

	// bom_items
	boms, err := s.repo.ListAllBOMItems()
	if err != nil {
		return failExport(fmt.Errorf("query BOM items: %w", err))
	}
	var bomRows [][]string
	for _, b := range boms {
		p := prodMap[b.ProductID]
		part := partMap[b.PartID]
		rep := "否"
		if b.Replaceable == 1 {
			rep = "是"
		}
		mode := "每台用N个"
		qtyText := fmt.Sprintf("%.2f", b.Quantity)
		if b.UseMode == 1 {
			mode = "每M台用1个"
			qtyText = fmt.Sprintf("%.0f台", b.Quantity)
		}
		bomRows = append(bomRows, []string{
			p.Code, p.Name,
			part.Code, part.Name,
			mode, qtyText,
			fmt.Sprintf("%.1f", b.LossRate),
			rep, nullStrSvc(b.Remark),
		})
	}
	if err := addCSV("bom_items", "bom_items.csv",
		[]string{"产品编码", "产品名称", "零件编码", "零件名称", "用量模式", "用量", "损耗率(%)", "可替换", "备注"},
		bomRows); err != nil {
		return failExport(err)
	}

	// batches
	var batchRows [][]string
	for _, b := range batches {
		recorded := "否"
		if b.ConsumptionRecorded == 1 {
			recorded = "是"
		}
		batchRows = append(batchRows, []string{
			b.BatchNo,
			nullStrSvc(b.ProductCode), nullStrSvc(b.ProductName),
			fmt.Sprintf("%d", b.PlanQty), fmt.Sprintf("%d", b.ProducedQty),
			batchStatusText(b.Status),
			nullStrSvc(b.Customer), nullStrSvc(b.Operator),
			recorded,
		})
	}
	if err := addCSV("product_batches", "product_batches.csv",
		[]string{"批次号", "产品编码", "产品名称", "计划数量", "完成数量", "状态", "客户", "操作人", "消耗已冻结"},
		batchRows); err != nil {
		return failExport(err)
	}

	// batch_consumptions
	consumptions, err := s.repo.ListAllBatchConsumptions()
	if err != nil {
		return failExport(fmt.Errorf("query batch consumptions: %w", err))
	}
	var consumptionRows [][]string
	for _, c := range consumptions {
		b := batchMap[c.BatchID]
		part := partMap[c.PartID]
		consumptionRows = append(consumptionRows, []string{
			b.BatchNo,
			part.Code, part.Name,
			fmt.Sprintf("%.2f", c.ConsumedQty),
		})
	}
	if err := addCSV("batch_consumptions", "batch_consumptions.csv",
		[]string{"批次号", "零件编码", "零件名称", "消耗数量"},
		consumptionRows); err != nil {
		return failExport(err)
	}

	// batch_trace
	traces, err := s.repo.ListAllBatchTraces()
	if err != nil {
		return failExport(fmt.Errorf("query batch traces: %w", err))
	}
	var traceRows [][]string
	for _, t := range traces {
		b := batchMap[t.BatchID]
		part := partMap[t.PartID]
		traceRows = append(traceRows, []string{
			b.BatchNo,
			part.Code, part.Name,
			nullStrSvc(t.PartBatchNo),
			fmt.Sprintf("%.2f", t.UsedQty),
			nullStrSvc(t.Supplier), nullStrSvc(t.Operator),
		})
	}
	if err := addCSV("batch_trace", "batch_trace.csv",
		[]string{"批次号", "零件编码", "零件名称", "零件批次号", "使用数量", "供应商", "操作人"},
		traceRows); err != nil {
		return failExport(err)
	}

	// batch_skip_parts
	skipRows, err := s.repo.GetAllSkippedParts()
	if err != nil {
		return failExport(fmt.Errorf("query skipped parts: %w", err))
	}
	var skipCSVRows [][]string
	for _, sr := range skipRows {
		b := batchMap[sr.BatchID]
		part := partMap[sr.PartID]
		skipCSVRows = append(skipCSVRows, []string{
			b.BatchNo, part.Code, part.Name,
		})
	}
	if err := addCSV("batch_skip_parts", "batch_skip_parts.csv",
		[]string{"批次号", "零件编码", "零件名称"},
		skipCSVRows); err != nil {
		return failExport(err)
	}

	// audit_log (all)
	logs, err := s.repo.ListRecentAuditLogs(100000)
	if err != nil {
		return failExport(fmt.Errorf("query audit logs: %w", err))
	}
	var logRows [][]string
	for _, l := range logs {
		op := ""
		if l.Operator != nil {
			op = *l.Operator
		}
		logRows = append(logRows, []string{
			l.CreatedAt.Format("2006-01-02 15:04:05"),
			actionText(l.Action),
			tableLabel(l.TableName),
			auditSummary(l),
			op,
		})
	}
	if err := addCSV("audit_log", "audit_log.csv",
		[]string{"时间", "操作", "对象", "内容摘要", "操作人"},
		logRows); err != nil {
		return failExport(err)
	}

	return files, subDir, nil
}

func nullStrSvc(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// bomConsume 计算指定产品数量下某BOM项的零件消耗量
// use_mode: 0=每台产品用N个零件 -> 台数*N*(1+损耗率%)
// use_mode: 1=每M台产品用1个零件(如包装箱) -> ceil(台数/M)，再按损耗率上浮后向上取整
func bomConsume(planQty int, item model.BOMItem) float64 {
	base := float64(planQty) * item.Quantity
	if item.UseMode == 1 {
		m := item.Quantity
		if m <= 0 {
			m = 1
		}
		base = math.Ceil(float64(planQty) / m)
	}
	consume := base * (1 + item.LossRate/100)
	if item.UseMode == 1 {
		consume = math.Ceil(consume)
	}
	return consume
}

// ---- 验证BOM完整性 ----

// ClearDatabase 清空全部业务数据，保留账号、迁移记录与数据库身份。
//
// 清空前先生成一份完整副本；生成或校验失败即中止，不清空任何数据。
// 副本保留最近 safetyCopyKeep 份，可用整库恢复把库退回到清空之前。
func (s *Service) ClearDatabase() (ClearResult, error) {
	var res ClearResult
	pre, err := s.safetyCopy(prefixPreClear)
	if err != nil {
		return res, fmt.Errorf("清空前备份失败，已中止清空：%w", err)
	}
	res.PreClear = pre
	res.Cleared = append([]string(nil), businessTables...)

	if err := s.repo.WithBulkLoad(func(tx repository.TxOps) error {
		if err := deleteBusinessTables(tx); err != nil {
			return fmt.Errorf("clear database failed: %w", err)
		}
		return nil
	}); err != nil {
		return res, bulkLoadOutcome(err)
	}

	// 审计留痕放在清空之后：audit_log 本身已被清空，
	// 只有提交后再写，这一步才会留在库里。
	if err := s.writeAudit(auditEntry{
		TableName: "database",
		Action:    "CLEAR",
		NewData: map[string]any{
			"pre_clear": pre,
			"cleared":   businessTables,
			"preserved": preservedTables,
		},
		Operator: auth.OperatorName(),
	}); err != nil {
		return res, &usecase.OperationError{State: usecase.OperationApplied, Err: err}
	}
	return res, nil
}

func (s *Service) ValidateBOM(productID int64) (bool, error) {
	items, err := s.repo.GetBOMByProduct(productID)
	if err != nil {
		return false, err
	}
	return len(items) > 0, nil
}

// ---- 用户与登录 ----

func (s *Service) UserCount() (int, error) {
	return s.repo.CountUsers()
}

func (s *Service) Login(username, password string) (*model.User, error) {
	u, err := s.repo.GetUserByUsername(strings.TrimSpace(username))
	if err != nil {
		return nil, err
	}
	if u == nil || !auth.VerifyPassword(password, u.PasswordHash) {
		return nil, fmt.Errorf("用户名或密码错误")
	}
	if u.Status != 1 {
		return nil, fmt.Errorf("账号已停用，请联系管理员")
	}
	_ = s.repo.TouchUserLogin(u.ID)
	return u, nil
}

func (s *Service) CreateInitialAdmin(username, password, displayName string) (*model.User, error) {
	n, err := s.repo.CountUsers()
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, fmt.Errorf("已存在用户，无法创建初始管理员")
	}
	return s.CreateUser(usecase.CreateUserInput{
		Username:    username,
		Password:    password,
		DisplayName: displayName,
		Role:        string(auth.RoleAdmin),
	})
}

func (s *Service) CreateUser(in usecase.CreateUserInput) (*model.User, error) {
	username := strings.TrimSpace(in.Username)
	if username == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		return nil, err
	}
	role := in.Role
	if role == "" {
		role = string(auth.RoleViewer)
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	u := &model.User{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  strPtrOrNil(in.DisplayName),
		Role:         role,
		Status:       1,
	}
	id, err := s.repo.CreateUser(u)
	if err != nil {
		return nil, err
	}
	u.ID = id
	return u, nil
}

func (s *Service) ListUsers() ([]model.User, error) {
	return s.repo.ListUsers()
}

func (s *Service) UpdateUser(in usecase.UpdateUserInput) error {
	u, err := s.repo.GetUserByID(in.ID)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("用户不存在")
	}
	if u.Role == string(auth.RoleAdmin) && u.Status == 1 && (in.Role != string(auth.RoleAdmin) || in.Status != 1) {
		n, err := s.repo.CountActiveAdmins()
		if err != nil {
			return err
		}
		if n <= 1 {
			return fmt.Errorf("至少需保留一个启用状态的管理员")
		}
	}
	u.DisplayName = strPtrOrNil(in.DisplayName)
	u.Role = in.Role
	u.Status = in.Status
	return s.repo.UpdateUser(u)
}

func (s *Service) ResetPassword(in usecase.ResetPasswordInput) error {
	if err := auth.ValidatePassword(in.Password); err != nil {
		return err
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	return s.repo.UpdateUserPassword(in.ID, hash)
}

func (s *Service) ChangePassword(in usecase.ChangePasswordInput) error {
	u, err := s.repo.GetUserByID(in.ID)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("用户不存在")
	}
	if !auth.VerifyPassword(in.OldPass, u.PasswordHash) {
		return fmt.Errorf("原密码错误")
	}
	return s.ResetPassword(usecase.ResetPasswordInput{ID: in.ID, Password: in.NewPass})
}

func (s *Service) DeleteUser(id int64) error {
	u, err := s.repo.GetUserByID(id)
	if err != nil {
		return err
	}
	if u != nil && u.Role == string(auth.RoleAdmin) && u.Status == 1 {
		n, err := s.repo.CountActiveAdmins()
		if err != nil {
			return err
		}
		if n <= 1 {
			return fmt.Errorf("至少需保留一个启用状态的管理员")
		}
	}
	return s.repo.DeleteUser(id)
}

func strPtrOrNil(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

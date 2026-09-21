package service

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"app/internal/auth"
	"app/internal/config"
	"app/internal/dbbackup"
	"app/internal/model"
	"app/internal/paths"
	"app/internal/repository"
)

type Service struct {
	repo          *repository.Repository
	dsn           string
	mysqldumpPath string
	cfg           *config.Config
}

func New(repo *repository.Repository, dsn string, mysqldumpPath string, cfg *config.Config) *Service {
	if mysqldumpPath == "" {
		mysqldumpPath = "mysqldump"
	}
	return &Service{repo: repo, dsn: dsn, mysqldumpPath: mysqldumpPath, cfg: cfg}
}

const maxLossRate = 100.0

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
	return nil
}

func validateNonNegativeQuantity(label string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return fmt.Errorf("%s必须是非负有限数值", label)
	}
	return nil
}

func validateBOMItem(item *model.BOMItem) error {
	if item == nil {
		return fmt.Errorf("BOM零件不能为空")
	}
	if item.UseMode != 0 && item.UseMode != 1 {
		return fmt.Errorf("BOM用量模式不合法")
	}
	if err := validatePositiveQuantity("BOM用量", item.Quantity); err != nil {
		return err
	}
	if math.IsNaN(item.LossRate) || math.IsInf(item.LossRate, 0) || item.LossRate < 0 || item.LossRate > maxLossRate {
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

func (s *Service) writeAudit(e auditEntry) {
	oldJSON, _ := toJSON(e.OldData)
	newJSON, _ := toJSON(e.NewData)
	_ = s.repo.CreateAuditLog(&model.AuditLog{
		TableName: e.TableName,
		RecordID:  e.RecordID,
		Action:    e.Action,
		OldData:   oldJSON,
		NewData:   newJSON,
		Operator:  &e.Operator,
	})
}

func (s *Service) writeAuditTx(tx *repository.Tx, e auditEntry) error {
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
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	p.Version = 1
	id, err := s.repo.CreateProduct(p)
	if err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	p.ID = id
	s.writeAudit(auditEntry{"products", id, "INSERT", nil, p, *p.Operator})
	return p, nil
}

func (s *Service) GetProduct(id int64) (*model.Product, error) {
	return s.repo.GetProduct(id)
}

func (s *Service) ListProducts() ([]model.Product, error) {
	return s.repo.ListProducts()
}

func (s *Service) UpdateProduct(p *model.Product) (*model.Product, error) {
	old, err := s.repo.GetProduct(p.ID)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, fmt.Errorf("product not found")
	}
	affected, err := s.repo.UpdateProduct(p)
	if err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("product version conflict, please refresh and retry")
	}
	p.Version = old.Version + 1
	s.writeAudit(auditEntry{"products", p.ID, "UPDATE", old, p, *p.Operator})
	return p, nil
}

func (s *Service) DeleteProduct(id int64, operator string) error {
	old, err := s.repo.GetProduct(id)
	if err != nil {
		return err
	}
	if old == nil {
		return fmt.Errorf("product not found")
	}
	if err := s.repo.DeleteProduct(id); err != nil {
		return err
	}
	s.writeAudit(auditEntry{"products", id, "DELETE", old, nil, operator})
	return nil
}

// ---- 零件 ----

func (s *Service) CreatePart(p *model.Part) (*model.Part, error) {
	if err := validatePartQuantities(p); err != nil {
		return nil, err
	}
	id, err := s.repo.CreatePart(p)
	if err != nil {
		return nil, fmt.Errorf("create part: %w", err)
	}
	p.ID = id
	s.writeAudit(auditEntry{"parts", id, "INSERT", nil, p, *p.Operator})
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
	old, err := s.repo.GetPart(p.ID)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, fmt.Errorf("part not found")
	}
	affected, err := s.repo.UpdatePart(p)
	if err != nil {
		return nil, fmt.Errorf("update part: %w", err)
	}
	if affected == 0 {
		return nil, fmt.Errorf("part version conflict, please refresh and retry")
	}
	p.Version = old.Version + 1
	s.writeAudit(auditEntry{"parts", p.ID, "UPDATE", old, p, *p.Operator})
	return p, nil
}

func (s *Service) DeletePart(id int64, operator string) error {
	old, err := s.repo.GetPart(id)
	if err != nil {
		return err
	}
	if old == nil {
		return fmt.Errorf("part not found")
	}
	if err := s.repo.DeletePart(id); err != nil {
		return err
	}
	s.writeAudit(auditEntry{"parts", id, "DELETE", old, nil, operator})
	return nil
}

func (s *Service) StockIn(partID int64, qty float64, operator string) error {
	if err := validatePositiveQuantity("入库数量", qty); err != nil {
		return err
	}
	return s.repo.WithTx(func(tx *repository.Tx) error {
		old, err := tx.GetPartForUpdate(partID)
		if err != nil {
			return fmt.Errorf("get part: %w", err)
		}
		if old == nil {
			return fmt.Errorf("part not found")
		}
		newStock := old.StockQty + qty
		if err := tx.UpdatePartStock(partID, newStock); err != nil {
			return fmt.Errorf("update stock: %w", err)
		}
		return s.writeAuditTx(tx, auditEntry{"parts", partID, "STOCK_IN", old, map[string]any{
			"old_stock": old.StockQty,
			"in_qty":    qty,
			"new_stock": newStock,
		}, operator})
	})
}

func (s *Service) AdjustStock(partID int64, newQty float64, operator string) error {
	if err := validateNonNegativeQuantity("调整后库存", newQty); err != nil {
		return err
	}
	return s.repo.WithTx(func(tx *repository.Tx) error {
		old, err := tx.GetPartForUpdate(partID)
		if err != nil {
			return fmt.Errorf("get part: %w", err)
		}
		if old == nil {
			return fmt.Errorf("part not found")
		}
		if err := tx.UpdatePartStock(partID, newQty); err != nil {
			return fmt.Errorf("adjust stock: %w", err)
		}
		return s.writeAuditTx(tx, auditEntry{"parts", partID, "STOCK_ADJUST", old, map[string]any{
			"old_stock": old.StockQty,
			"new_stock": newQty,
			"diff":      newQty - old.StockQty,
		}, operator})
	})
}

// ---- BOM ----

func (s *Service) AddBOMItem(b *model.BOMItem) (*model.BOMItem, error) {
	if err := validateBOMItem(b); err != nil {
		return nil, err
	}
	// 校验产品和零件存在
	prod, err := s.repo.GetProduct(b.ProductID)
	if err != nil {
		return nil, err
	}
	if prod == nil {
		return nil, fmt.Errorf("product not found")
	}
	part, err := s.repo.GetPart(b.PartID)
	if err != nil {
		return nil, err
	}
	if part == nil {
		return nil, fmt.Errorf("part not found")
	}
	id, err := s.repo.CreateBOMItem(b)
	if err != nil {
		return nil, fmt.Errorf("create bom: %w", err)
	}
	b.ID = id
	s.writeAudit(auditEntry{"bom_items", id, "INSERT", nil, b, *b.Operator})
	return b, nil
}

func (s *Service) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	return s.repo.GetBOMByProduct(productID)
}

func (s *Service) RemoveBOMItem(id int64, operator string) error {
	// 先查询
	_ = operator
	if err := s.repo.DeleteBOMItem(id); err != nil {
		return err
	}
	s.writeAudit(auditEntry{"bom_items", id, "DELETE", nil, nil, operator})
	return nil
}

// ---- 批次 ----

func (s *Service) CreateBatch(b *model.ProductBatch) (*model.ProductBatch, error) {
	if b == nil {
		return nil, fmt.Errorf("批次不能为空")
	}
	if err := validatePlanQty(b.PlanQty); err != nil {
		return nil, err
	}
	prod, err := s.repo.GetProduct(b.ProductID)
	if err != nil {
		return nil, err
	}
	if prod == nil {
		return nil, fmt.Errorf("product not found")
	}
	id, err := s.repo.CreateBatch(b)
	if err != nil {
		return nil, fmt.Errorf("create batch: %w", err)
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
	s.writeAudit(auditEntry{"product_batches", id, "INSERT", nil, auditData, *b.Operator})
	return b, nil
}

func (s *Service) ListBatches() ([]model.ProductBatch, error) {
	return s.repo.ListBatches()
}

func (s *Service) UpdateBatchStatus(id int64, status int, operator string) error {
	return s.repo.WithTx(func(tx *repository.Tx) error {
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
			for _, item := range bom {
				if skipMap[item.PartID] {
					continue
				}
				part, err := tx.GetPartForUpdate(item.PartID)
				if err != nil {
					return fmt.Errorf("get part %d: %w", item.PartID, err)
				}
				if part == nil {
					continue
				}
				requestedDeduct := bomConsume(batch.PlanQty, item)
				actualDeduct := requestedDeduct
				if actualDeduct > part.StockQty {
					actualDeduct = part.StockQty
				}
				newStock := part.StockQty - actualDeduct
				if newStock < 0 {
					newStock = 0
				}
				if err := tx.UpdatePartStock(item.PartID, newStock); err != nil {
					return fmt.Errorf("update stock for part %d: %w", item.PartID, err)
				}
				if err := tx.CreateBatchConsumption(&model.BatchConsumption{
					BatchID:     id,
					PartID:      item.PartID,
					ConsumedQty: actualDeduct,
				}); err != nil {
					return fmt.Errorf("record consumption for part %d: %w", item.PartID, err)
				}
				if err := s.writeAuditTx(tx, auditEntry{"parts", item.PartID, "STOCK_DEDUCT", part, map[string]any{
					"old_stock":        part.StockQty,
					"requested_deduct": requestedDeduct,
					"deduct":           actualDeduct,
					"new_stock":        newStock,
					"batch_id":         id,
				}, operator}); err != nil {
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
			return fmt.Errorf("batch state changed, please refresh and retry")
		}
		if err := s.writeAuditTx(tx, auditEntry{"product_batches", id, "UPDATE_STATUS", oldMap, map[string]any{"status": status}, operator}); err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) RevokeBatch(id int64, operator string) error {
	return s.repo.WithTx(func(tx *repository.Tx) error {
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
				newStock := part.StockQty + consumption.ConsumedQty
				if err := tx.UpdatePartStock(consumption.PartID, newStock); err != nil {
					return fmt.Errorf("update stock for part %d: %w", consumption.PartID, err)
				}
				if err := s.writeAuditTx(tx, auditEntry{"parts", consumption.PartID, "STOCK_ADJUST", part, map[string]any{
					"old_stock": part.StockQty,
					"new_stock": newStock,
					"diff":      consumption.ConsumedQty,
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
			return fmt.Errorf("batch state changed, please refresh and retry")
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

// ---- 统计 ----

// StockDailyPoint 某天的进出库汇总
type StockDailyPoint struct {
	Date string  // MM-DD
	In   float64 // 入库量
	Out  float64 // 出库量
}

// PartStockStat 单个零件近期的进出库统计
type PartStockStat struct {
	Code string
	Name string
	In   float64
	Out  float64
}

// SupplierStockStat 供应商入库统计（入库主要针对零件）
type SupplierStockStat struct {
	Name string
	In   float64
}

// CustomerStockStat 客户出库统计（出库关联客户，产品按批次）
type CustomerStockStat struct {
	Name string
	Out  float64
}

// ProductStockStat 产品出库统计
type ProductStockStat struct {
	Code string
	Name string
	Out  float64
}

// StockStats 近期进出库统计结果
type StockStats struct {
	Days            []StockDailyPoint
	TopIn           []PartStockStat     // 零件入库量前10
	TopOut          []PartStockStat     // 零件出库量前10
	TopSuppliers    []SupplierStockStat // 供应商入库前10
	TopCustomers    []CustomerStockStat // 客户出库前10
	TopProducts     []ProductStockStat  // 产品出库前10
	TotalIn         float64
	TotalOut        float64
	ProductOutTotal float64
}

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

	// 出库关联批次，用于把零件消耗映射到客户/产品
	batchIDSet := make(map[int64]bool)
	for _, l := range logs {
		if l.Action == "STOCK_DEDUCT" && l.NewData != nil {
			if bid, ok := (*l.NewData)["batch_id"].(float64); ok && bid > 0 {
				batchIDSet[int64(bid)] = true
			}
		}
	}
	batchMap := make(map[int64]model.ProductBatch)
	if len(batchIDSet) > 0 {
		batches, _ := s.repo.ListBatches()
		for _, b := range batches {
			if batchIDSet[b.ID] {
				batchMap[b.ID] = b
			}
		}
	}

	for _, l := range logs {
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
	return s.repo.WithTx(func(tx *repository.Tx) error {
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
	return s.repo.WithTx(func(tx *repository.Tx) error {
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

func (s *Service) GetTraceByBatch(batchID int64) ([]model.BatchTrace, error) {
	return s.repo.GetTraceByBatch(batchID)
}

func (s *Service) TraceByProduct(batchNo string) ([]model.BatchTrace, error) {
	return nil, fmt.Errorf("not implemented yet")
}

func (s *Service) TraceByPart(partCode string) ([]model.BatchTrace, error) {
	// 查某零件被哪些批次使用过 — "反向追溯"
	return nil, fmt.Errorf("not implemented yet")
}

// ---- 审计 ----

func (s *Service) GetAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error) {
	return s.repo.ListAuditLogs(tableName, recordID)
}

func (s *Service) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) {
	return s.repo.ListRecentAuditLogs(limit)
}

// ---- 备份与导出 ----

func (s *Service) RestoreDatabase(filePath string) (success, failed int, err error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, 0, fmt.Errorf("read file: %w", err)
	}

	// disable checks for smooth import
	s.repo.Exec("SET FOREIGN_KEY_CHECKS = 0")
	s.repo.Exec("SET UNIQUE_CHECKS = 0")
	defer func() {
		s.repo.Exec("SET FOREIGN_KEY_CHECKS = 1")
		s.repo.Exec("SET UNIQUE_CHECKS = 1")
	}()

	content := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(content, "\n")

	var buf strings.Builder
	inInsert := false

	execInsert := func(sql string) bool {
		if _, e := s.repo.Exec(sql); e != nil {
			failed++
			return false
		}
		success++
		return true
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if !inInsert {
			if strings.HasPrefix(strings.ToUpper(trimmed), "INSERT INTO") {
				buf.Reset()
				buf.WriteString(line)
				if strings.HasSuffix(strings.TrimRight(trimmed, " \t"), ";") {
					execInsert(buf.String())
				} else {
					inInsert = true
				}
			}
			continue
		}
		buf.WriteString("\n")
		buf.WriteString(line)
		if strings.HasSuffix(strings.TrimRight(trimmed, " \t"), ";") {
			execInsert(buf.String())
			inInsert = false
		}
	}
	return success, failed, nil
}

func (s *Service) BackupDatabase(saveDir string) (string, error) {
	return dbbackup.Backup(s.dsn, s.mysqldumpPath, saveDir)
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

func (s *Service) ExportAuditLogCSV(startDate, endDate time.Time, filePath string) (int, error) {
	endDate = endDate.Add(24 * time.Hour)
	logs, err := s.repo.ListAuditLogsByDate(startDate, endDate)
	if err != nil {
		return 0, fmt.Errorf("query audit logs: %w", err)
	}
	f, err := os.Create(filePath)
	if err != nil {
		return 0, fmt.Errorf("create csv file: %w", err)
	}
	defer f.Close()
	f.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"时间", "操作", "对象", "内容摘要", "操作人"})
	for _, log := range logs {
		op := ""
		if log.Operator != nil {
			op = *log.Operator
		}
		w.Write([]string{
			log.CreatedAt.Format("2006-01-02 15:04:05"),
			actionText(log.Action),
			tableLabel(log.TableName),
			auditSummary(log),
			op,
		})
	}
	return len(logs), nil
}

func (s *Service) ExportAllDataCSV(saveDir string) (map[string]string, string, error) {
	now := time.Now().Format("20060102_150405")
	subDir := filepath.Join(saveDir, now)
	if err := os.MkdirAll(subDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create export dir: %w", err)
	}
	files := make(map[string]string)

	writeCSV := func(name string, header []string, rows [][]string) (string, error) {
		p := filepath.Join(subDir, name)
		f, err := os.Create(p)
		if err != nil {
			return "", err
		}
		defer f.Close()
		f.Write([]byte{0xEF, 0xBB, 0xBF})
		w := csv.NewWriter(f)
		defer w.Flush()
		w.Write(header)
		for _, r := range rows {
			w.Write(r)
		}
		return p, nil
	}

	// build lookup maps
	products, _ := s.repo.ListProducts()
	prodMap := make(map[int64]model.Product)
	for _, p := range products {
		prodMap[p.ID] = p
	}
	parts, _ := s.repo.ListParts()
	partMap := make(map[int64]model.Part)
	for _, p := range parts {
		partMap[p.ID] = p
	}
	batches, _ := s.repo.ListBatches()
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
	if p, err := writeCSV("products.csv",
		[]string{"编码", "名称", "规格", "单位", "状态", "操作人", "创建日期"},
		prodRows); err == nil {
		files["products"] = p
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
	if p, err := writeCSV("parts.csv",
		[]string{"编码", "名称", "规格", "分类", "库存", "预警库存", "状态", "供应商", "操作人"},
		partRows); err == nil {
		files["parts"] = p
	}

	// bom_items
	boms, _ := s.repo.ListAllBOMItems()
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
	if p, err := writeCSV("bom_items.csv",
		[]string{"产品编码", "产品名称", "零件编码", "零件名称", "用量模式", "用量", "损耗率(%)", "可替换", "备注"},
		bomRows); err == nil {
		files["bom_items"] = p
	}

	// batches
	var batchRows [][]string
	for _, b := range batches {
		batchRows = append(batchRows, []string{
			b.BatchNo,
			nullStrSvc(b.ProductCode), nullStrSvc(b.ProductName),
			fmt.Sprintf("%d", b.PlanQty), fmt.Sprintf("%d", b.ProducedQty),
			batchStatusText(b.Status),
			nullStrSvc(b.Customer), nullStrSvc(b.Operator),
		})
	}
	if p, err := writeCSV("product_batches.csv",
		[]string{"批次号", "产品编码", "产品名称", "计划数量", "完成数量", "状态", "客户", "操作人"},
		batchRows); err == nil {
		files["product_batches"] = p
	}

	// batch_trace
	traces, _ := s.repo.ListAllBatchTraces()
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
	if p, err := writeCSV("batch_trace.csv",
		[]string{"批次号", "零件编码", "零件名称", "零件批次号", "使用数量", "供应商", "操作人"},
		traceRows); err == nil {
		files["batch_trace"] = p
	}

	// batch_skip_parts
	skipRows, _ := s.repo.GetAllSkippedParts()
	var skipCSVRows [][]string
	for _, sr := range skipRows {
		b := batchMap[sr.BatchID]
		part := partMap[sr.PartID]
		skipCSVRows = append(skipCSVRows, []string{
			b.BatchNo, part.Code, part.Name,
		})
	}
	if p, err := writeCSV("batch_skip_parts.csv",
		[]string{"批次号", "零件编码", "零件名称"},
		skipCSVRows); err == nil {
		files["batch_skip_parts"] = p
	}

	// audit_log (all)
	logs, _ := s.repo.ListRecentAuditLogs(100000)
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
	if p, err := writeCSV("audit_log.csv",
		[]string{"时间", "操作", "对象", "内容摘要", "操作人"},
		logRows); err == nil {
		files["audit_log"] = p
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

func (s *Service) ClearDatabase() error {
	stmts := []string{
		"SET FOREIGN_KEY_CHECKS = 0",
		"DELETE FROM batch_skip_parts",
		"DELETE FROM batch_trace",
		"DELETE FROM bom_items",
		"DELETE FROM product_batches",
		"DELETE FROM parts",
		"DELETE FROM products",
		"DELETE FROM audit_log",
		"SET FOREIGN_KEY_CHECKS = 1",
	}
	for _, stmt := range stmts {
		if _, err := s.repo.Exec(stmt); err != nil {
			return fmt.Errorf("clear database failed at [%s]: %w", stmt, err)
		}
	}
	return nil
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
	return s.CreateUser(username, password, displayName, string(auth.RoleAdmin))
}

func (s *Service) CreateUser(username, password, displayName, role string) (*model.User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, fmt.Errorf("用户名不能为空")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return nil, err
	}
	if role == "" {
		role = string(auth.RoleViewer)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &model.User{
		Username:     username,
		PasswordHash: hash,
		DisplayName:  strPtrOrNil(displayName),
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

func (s *Service) UpdateUser(id int64, displayName, role string, status int) error {
	u, err := s.repo.GetUserByID(id)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("用户不存在")
	}
	if u.Role == string(auth.RoleAdmin) && u.Status == 1 && (role != string(auth.RoleAdmin) || status != 1) {
		n, err := s.repo.CountActiveAdmins()
		if err != nil {
			return err
		}
		if n <= 1 {
			return fmt.Errorf("至少需保留一个启用状态的管理员")
		}
	}
	u.DisplayName = strPtrOrNil(displayName)
	u.Role = role
	u.Status = status
	return s.repo.UpdateUser(u)
}

func (s *Service) ResetPassword(id int64, password string) error {
	if err := auth.ValidatePassword(password); err != nil {
		return err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.repo.UpdateUserPassword(id, hash)
}

func (s *Service) ChangePassword(id int64, oldPw, newPw string) error {
	u, err := s.repo.GetUserByID(id)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("用户不存在")
	}
	if !auth.VerifyPassword(oldPw, u.PasswordHash) {
		return fmt.Errorf("原密码错误")
	}
	return s.ResetPassword(id, newPw)
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

package service

import (
	"fmt"
	"sort"
	"strings"

	"app/internal/model"
	"app/internal/usecase"
)

// 未完成批次的状态：待生产 / 生产中 / 已暂停。
//
// 已完成（2）的消耗已经从库存里扣过，再算一次等于重复占用；
// 已撤销（4）不会扣任何东西。两个都必须排除。
func isOpenBatchStatus(status int) bool {
	switch status {
	case 0, 1, 3:
		return true
	default:
		return false
	}
}

// batchConsumption 是"一批计划会消耗什么"的唯一算法入口。
//
// 完成生产时用的是同一函数（UpdateBatchStatus 里逐行 bomConsume），
// 所以投影与真实扣减不会因为公式分叉而给出两套答案。
// skip 是该批次标记为"跳过"的零件：完成时不扣，这里也不计入。
func batchConsumption(bom []model.BOMItem, planQty int, skip map[int64]bool) map[int64]float64 {
	out := make(map[int64]float64, len(bom))
	for _, item := range bom {
		if skip[item.PartID] {
			continue
		}
		q := quantizeQuantity(bomConsume(planQty, item))
		if q == 0 {
			continue
		}
		out[item.PartID] += q
	}
	return out
}

// openBatchPlan 是一个未完成批次及其逐零件消耗，供按批次出结论用。
type openBatchPlan struct {
	batch model.ProductBatch
	parts map[int64]float64
}

// commitments 汇总所有未完成批次对每个零件的占用，并记下来源批次。
//
// excludeBatchID 用于"完成某批时，看别的批次占了多少"——本批自己的需求
// 已经在缺料文案里单独说了，重复算进去会把数字说错。
func (s *Service) commitments(excludeBatchID int64) (map[int64]float64, map[int64][]usecase.ProjectionHolder, error) {
	committed, holders, _, err := s.commitmentsAndPlans(excludeBatchID)
	return committed, holders, err
}

func (s *Service) commitmentsAndPlans(excludeBatchID int64) (map[int64]float64, map[int64][]usecase.ProjectionHolder, []openBatchPlan, error) {
	batches, err := s.repo.ListBatches()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list batches: %w", err)
	}
	skipRows, err := s.repo.GetAllSkippedParts()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list skipped parts: %w", err)
	}
	skipByBatch := make(map[int64]map[int64]bool, len(skipRows))
	for _, r := range skipRows {
		if skipByBatch[r.BatchID] == nil {
			skipByBatch[r.BatchID] = make(map[int64]bool)
		}
		skipByBatch[r.BatchID][r.PartID] = true
	}

	bomCache := make(map[int64][]model.BOMItem)
	committed := make(map[int64]float64)
	holders := make(map[int64][]usecase.ProjectionHolder)
	var plans []openBatchPlan

	for _, b := range batches {
		if b.ID == excludeBatchID || !isOpenBatchStatus(b.Status) {
			continue
		}
		bom, ok := bomCache[b.ProductID]
		if !ok {
			bom, err = s.repo.GetBOMByProduct(b.ProductID)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("get bom for product %d: %w", b.ProductID, err)
			}
			bomCache[b.ProductID] = bom
		}
		consumption := batchConsumption(bom, b.PlanQty, skipByBatch[b.ID])
		plans = append(plans, openBatchPlan{batch: b, parts: consumption})
		for partID, qty := range consumption {
			committed[partID] += qty
			holders[partID] = append(holders[partID], usecase.ProjectionHolder{
				BatchID: b.ID,
				BatchNo: b.BatchNo,
				Qty:     qty,
			})
		}
	}
	return committed, holders, plans, nil
}

// StockProjection 计算"在产批次完成后"的库存视图。
//
// 只返回有占用的零件：没有占用的零件，投影值等于当前库存，界面按 0 处理即可，
// 没必要把一个几百行零件的列表整份传一遍。
func (s *Service) StockProjection(in usecase.StockProjectionInput) (*usecase.StockProjectionResult, error) {
	committed, holders, plans, err := s.commitmentsAndPlans(0)
	if err != nil {
		return nil, err
	}

	// 建批预览：把用户正在填的这一批也算进去。它还没有批次号，
	// 用 planned 标记，界面据此显示"计划中"而不是编一个号码。
	if in.ProductID > 0 && in.PlanQty > 0 {
		bom, err := s.repo.GetBOMByProduct(in.ProductID)
		if err != nil {
			return nil, fmt.Errorf("get bom for product %d: %w", in.ProductID, err)
		}
		for partID, qty := range batchConsumption(bom, in.PlanQty, nil) {
			committed[partID] += qty
			holders[partID] = append(holders[partID], usecase.ProjectionHolder{
				BatchNo: "计划中",
				Qty:     qty,
				Planned: true,
			})
		}
	}

	parts, err := s.repo.ListParts()
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	partsByID := make(map[int64]model.Part, len(parts))

	res := &usecase.StockProjectionResult{
		Parts:   make([]usecase.PartProjection, 0, len(committed)),
		Batches: make([]usecase.BatchReserve, 0, len(plans)),
	}
	afterByPart := make(map[int64]float64, len(committed))
	for _, p := range parts {
		partsByID[p.ID] = p
		c, ok := committed[p.ID]
		if !ok {
			continue
		}
		after := quantizeQuantity(p.StockQty - c)
		afterByPart[p.ID] = after
		hs := holders[p.ID]
		// 来源按占用量降序，界面只展示前几个；顺序稳定才不会每次刷新都在跳。
		sort.SliceStable(hs, func(i, j int) bool {
			if hs[i].Planned != hs[j].Planned {
				return hs[i].Planned // 计划中的排最前
			}
			return hs[i].Qty > hs[j].Qty
		})
		res.Parts = append(res.Parts, usecase.PartProjection{
			PartID:    p.ID,
			Stock:     quantizeQuantity(p.StockQty),
			Warn:      quantizeQuantity(p.WarnQty),
			Committed: quantizeQuantity(c),
			After:     after,
			Holders:   hs,
		})

		// 汇总只数启用中的零件：停用件不会补货，持续占提醒位只会稀释信号。
		if p.Status == 1 {
			switch {
			case after < 0:
				res.Summary.WillShort++
			case p.WarnQty > 0 && after < p.WarnQty:
				res.Summary.WillWarn++
			}
		}
	}

	// 按批次出备料结论：口径与上面的零件行完全相同（after 已含全部在产批次），
	// 只是把"最坏的那一档"归到批次上，供列表角标与工作台生产计划直接使用。
	for _, plan := range plans {
		level := "ok"
		var briefs []usecase.PartReserveBrief
		for partID := range plan.parts {
			p, ok := partsByID[partID]
			if !ok {
				continue
			}
			after := afterByPart[partID]
			switch {
			case after < 0:
				// short 覆盖 low：一批里只要有一个零件做不出来，整批就做不出来。
				level = "short"
			case p.WarnQty > 0 && after < p.WarnQty:
				if level != "short" {
					level = "low"
				}
			default:
				continue
			}
			briefs = append(briefs, usecase.PartReserveBrief{
				PartID: partID,
				Code:   p.Code,
				Name:   p.Name,
				After:  after,
				Warn:   quantizeQuantity(p.WarnQty),
			})
		}
		sort.SliceStable(briefs, func(i, j int) bool { return briefs[i].After < briefs[j].After })
		res.Batches = append(res.Batches, usecase.BatchReserve{
			BatchID: plan.batch.ID,
			Level:   level,
			Parts:   briefs,
		})
	}

	// 余量升序：负数（缺料）自然排在最前，工作台与零件页都可以直接取前几条。
	sort.SliceStable(res.Parts, func(i, j int) bool {
		if res.Parts[i].After != res.Parts[j].After {
			return res.Parts[i].After < res.Parts[j].After
		}
		return res.Parts[i].PartID < res.Parts[j].PartID
	})
	return res, nil
}

// holderSummary 把占用来源拼成一句短说明：总量 + 最多三个批次号。
// 批次多时只列前几个，避免把错误文案撑成一屏。
func holderSummary(hs []usecase.ProjectionHolder) string {
	total := 0.0
	names := make([]string, 0, 3)
	for _, h := range hs {
		total += h.Qty
		if len(names) < 3 {
			names = append(names, fmt.Sprintf("%s %.2f", h.BatchNo, h.Qty))
		}
	}
	more := ""
	if len(hs) > len(names) {
		more = fmt.Sprintf(" 等 %d 批", len(hs))
	}
	return fmt.Sprintf("%.2f（%s%s）", quantizeQuantity(total), strings.Join(names, "、"), more)
}

// otherCommittedForHint 是完成生产被拒时的补充说明：哪些在产批次占用了这个零件。
//
// 失败不改结果：拿不到占用来源只是少一句解释，不能让"缺料"这个结论丢失。
func (s *Service) otherCommittedForHint(excludeBatchID int64) map[int64][]usecase.ProjectionHolder {
	_, holders, err := s.commitments(excludeBatchID)
	if err != nil {
		return nil
	}
	return holders
}

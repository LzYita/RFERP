package service

import (
	"math"
	"testing"

	"app/internal/model"
)

// bomConsume 是界面（web/src/lib/batch.ts 的 bomConsume）预警时用的同一个公式。
// 两边必须一致，否则预警就是在骗人——界面说够、扣的时候不够，库存直接归零
// 且没有任何提示（actualDeduct 被静默夹取，service.go:565-567）。
//
// 这里逐个固定值，TS 侧 tests/batch.test.ts 用的是同一组数。
func TestBomConsumeMatchesUITable(t *testing.T) {
	cases := []struct {
		name string
		plan int
		item model.BOMItem
		want float64
	}{
		{"每台 2 个，损耗 5%", 100, model.BOMItem{Quantity: 2, LossRate: 5, UseMode: 0}, 210},
		{"每台 2 个，无损耗", 100, model.BOMItem{Quantity: 2, LossRate: 0, UseMode: 0}, 200},
		{"每台 0.5 个", 1, model.BOMItem{Quantity: 0.5, LossRate: 0, UseMode: 0}, 0.5},
		{"每M台 1 个：100台/10，损耗 5% 上取整", 100, model.BOMItem{Quantity: 10, LossRate: 5, UseMode: 1}, 11},
		{"每M台 1 个：不进位", 100, model.BOMItem{Quantity: 10, LossRate: 0, UseMode: 1}, 10},
		{"每M台 1 个：101 台进位到 11", 101, model.BOMItem{Quantity: 10, LossRate: 0, UseMode: 1}, 11},
		{"use_mode=1 且用量为 0 → 退化为每 1 台", 100, model.BOMItem{Quantity: 0, LossRate: 0, UseMode: 1}, 100},
		{"use_mode=1 且用量为负 → 同样退化", 100, model.BOMItem{Quantity: -5, LossRate: 0, UseMode: 1}, 100},
		{"损耗率 100% 翻倍", 100, model.BOMItem{Quantity: 1, LossRate: 100, UseMode: 0}, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bomConsume(c.plan, c.item)
			if math.Abs(got-c.want) > 1e-9 {
				t.Errorf("bomConsume(%d) = %v, want %v", c.plan, got, c.want)
			}
		})
	}
}

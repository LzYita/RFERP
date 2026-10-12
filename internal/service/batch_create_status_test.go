package service

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"app/internal/model"
)

// 回归测试：CreateBatch 曾直接接受客户端传来的 status。
//
// 任何有 Production.CreateBatch 权限的账号都能 POST status=2 建出一个
// 「已完成」批次，而扣减库存只发生在 UpdateBatchStatus 转到 2 的路径上，
// 于是这条批次不耗任何料；又因为 consumption_recorded 停在 0，它还永远
// 撤销不掉（批次缺少冻结的库存消耗记录，无法安全撤销）。
//
// **影响范围不只是管理员**：ModuleBatch 的写权限包含生产员。
func TestCreateBatchForcesPendingStatus(t *testing.T) {
	// 逐个伪造值都要试：status=4 尤其危险（已撤销且不可撤销），
	// status=1 会让批次凭空处于生产中而不扣任何料。
	for _, forged := range []int{1, 2, 3, 4, -1, 99} {
		t.Run(batchStatusLabel(forged), func(t *testing.T) {
			s, db := newMockService(t)

			db.ExpectBegin()
			db.ExpectQuery("SELECT .* FROM products").
				WillReturnRows(productRows(time.Now()))
			db.ExpectExec("INSERT INTO product_batches").
				WillReturnResult(sqlmock.NewResult(1, 1))
			db.ExpectExec("INSERT INTO .*audit.*").
				WillReturnResult(sqlmock.NewResult(1, 1))
			db.ExpectCommit()

			op := "tester"
			got, err := s.CreateBatch(&model.ProductBatch{
				BatchNo:   "B-1",
				ProductID: 1,
				PlanQty:   10,
				Status:    forged, // 客户端伪造
				Operator:  &op,
			})
			if err != nil {
				t.Fatalf("CreateBatch: %v", err)
			}
			if got.Status != batchStatusPending {
				t.Errorf("返回值 status = %d（客户端传入 %d），应为 %d",
					got.Status, forged, batchStatusPending)
			}
		})
	}
}

// 未指定状态（零值）时行为不变。
func TestCreateBatchKeepsPendingByDefault(t *testing.T) {
	s, db := newMockService(t)
	db.ExpectBegin()
	db.ExpectQuery("SELECT .* FROM products").WillReturnRows(productRows(time.Now()))
	db.ExpectExec("INSERT INTO product_batches").WillReturnResult(sqlmock.NewResult(1, 1))
	db.ExpectExec("INSERT INTO .*audit.*").WillReturnResult(sqlmock.NewResult(1, 1))
	db.ExpectCommit()

	op := "tester"
	got, err := s.CreateBatch(&model.ProductBatch{
		BatchNo: "B-1", ProductID: 1, PlanQty: 1, Operator: &op,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != batchStatusPending {
		t.Errorf("status = %d，want %d", got.Status, batchStatusPending)
	}
}

// 这次改动不能把计划数量的校验挤掉，也不能放宽产品存在性检查。
func TestCreateBatchKeepsOtherValidation(t *testing.T) {
	s, _ := newMockService(t)
	op := "tester"
	for _, qty := range []int{0, -1} {
		if _, err := s.CreateBatch(&model.ProductBatch{
			BatchNo: "B-1", ProductID: 1, PlanQty: qty, Operator: &op,
		}); err == nil {
			t.Errorf("计划数量 %d 被接受，应拒绝", qty)
		}
	}
}

func batchStatusLabel(s int) string {
	switch s {
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
		return "非法值"
	}
}

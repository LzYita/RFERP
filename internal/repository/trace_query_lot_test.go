package repository

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"app/internal/migrate"
	"app/internal/model"
)

// 本文件刻意只往测试库里插最小数据；part_inbounds 的业务语义由
// trace_alloc 相关测试覆盖，这里只关心"批次号能被反查到"。

// 按零件入库批次号反查生产批次：经分配命中的、旧文本命中的都要找到，
// 同一记录双重命中（分配 + 旧文本）只出现一次。
func TestTraceByPartLot(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			var db *sqlx.DB
			var store Store
			var err error
			if engine == "sqlite" {
				db, err = OpenSQLite(filepath.Join(t.TempDir(), "lots.db"))
				if err != nil {
					t.Fatal(err)
				}
				_, err = migrate.RunSQLite(db, migrate.SQLiteOptions{})
				store = NewSQLite(db)
			} else {
				dsn := os.Getenv("RFERP_TEST_DSN")
				if dsn == "" {
					t.Skip("RFERP_TEST_DSN not set")
				}
				db, err = sqlx.Connect("mysql", dsn)
				if err != nil {
					t.Fatal(err)
				}
				_, err = migrate.Run(db, migrate.Options{DSN: dsn, BackupDir: t.TempDir(), Backup: func(string, string, string) (string, error) { return "test-backup", nil }})
				store = New(db)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err != nil {
				t.Fatal(err)
			}
			var bids, pids []int64
			err = store.WithTx(func(tx TxOps) error {
				id, e := tx.CreatePart(&model.Part{Code: "LOT-P", Name: "LOT-P", Unit: "个", Status: 1})
				if e != nil {
					return e
				}
				pids = append(pids, id)
				for _, no := range []string{"LOT-B1", "LOT-B2"} {
					pid, e := tx.CreateProduct(&model.Product{Code: no, Name: no, Unit: "台", Status: 1})
					if e != nil {
						return e
					}
					id, e := tx.CreateBatch(&model.ProductBatch{BatchNo: no, ProductID: pid, PlanQty: 3})
					if e != nil {
						return e
					}
					bids = append(bids, id)
				}
				// 直接插行拿 id：SQLite 是单写连接，事务里再用 db 查会死锁。
				res, e := tx.Exec(`INSERT INTO part_inbounds (part_id, qty, remaining, note) VALUES (?, ?, ?, NULL)`,
					pids[0], 50, 50)
				if e != nil {
					return e
				}
				inboundID, e := res.LastInsertId()
				if e != nil {
					return e
				}
				if _, e := tx.Exec(`UPDATE part_inbounds SET batch_no = 'RK-LOT' WHERE id = ?`, inboundID); e != nil {
					return e
				}
				// 两个生产批次都用了这个入库批次，各挂一条带分配的记录。
				for i, bid := range bids {
					qty := 2.0
					if i == 1 {
						qty = 3
					}
					if e := tx.DrawPartInboundFIFO(pids[0], bid, qty); e != nil {
						return e
					}
					trace := &model.BatchTrace{BatchID: bid, PartID: pids[0], UsedQty: qty}
					trace.Allocations = []model.BatchTraceAlloc{{InboundID: inboundID, Qty: qty}}
					if _, e := tx.ReplacePartTrace(trace); e != nil {
						return e
					}
				}
				// 旧记录：自由文本批次号同样命中。放在同一个生产批次
				// （bids[1]）上、但没有分配行——覆盖"旧文本也能命中"的路径。
				legacy := "RK-LOT"
				if _, e := tx.CreateTrace(&model.BatchTrace{BatchID: bids[1], PartID: pids[0], UsedQty: 1, PartBatchNo: &legacy}); e != nil {
					return e
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			rows, err := store.TraceByPartLot("RK-LOT")
			if err != nil {
				t.Fatal(err)
			}
			if rows == nil {
				t.Fatal("empty results must serialize as []")
			}
			got := map[int64]bool{}
			for _, r := range rows {
				got[r.BatchID] = true
			}
			if !got[bids[0]] || !got[bids[1]] {
				t.Fatalf("应同时命中两个生产批次：got=%v", got)
			}
			// bids[0] 一条（分配命中）；bids[1] 两条（分配 + 旧文本各一条，
			// 不是同一条记录，都要出现）。共 3 条，无需去重。
			if len(rows) != 3 {
				t.Fatalf("应命中 3 条记录：got=%d", len(rows))
			}
			miss, err := store.TraceByPartLot("RK-MISSING")
			if err != nil {
				t.Fatal(err)
			}
			if miss == nil || len(miss) != 0 {
				t.Fatalf("未命中应返回空数组：%+v", miss)
			}
		})
	}
}

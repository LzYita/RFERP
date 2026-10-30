package repository

import (
	"app/internal/migrate"
	"app/internal/model"
	"fmt"
	"github.com/jmoiron/sqlx"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTraceQueries(t *testing.T) {
	for _, engine := range []string{"sqlite", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			var db *sqlx.DB
			var store Store
			var err error
			if engine == "sqlite" {
				db, err = OpenSQLite(filepath.Join(t.TempDir(), "traces.db"))
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
			var bids, pids, tids []int64
			err = store.WithTx(func(tx TxOps) error {
				// 编码与名称刻意不同：按名称（精确/部分）也要能命中同一个零件。
				for _, p := range [][2]string{{"TRACE-A", "加热管"}, {"TRACE-B", "温控器"}} {
					id, e := tx.CreatePart(&model.Part{Code: p[0], Name: p[1], Unit: "个", Status: 1})
					if e != nil {
						return e
					}
					pids = append(pids, id)
				}
				for _, no := range []string{"TRACE-B1", "TRACE-B2"} {
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
				for _, ids := range [][2]int64{{bids[0], pids[0]}, {bids[0], pids[1]}, {bids[1], pids[0]}} {
					supplier := "合成供应商"
					id, e := tx.CreateTrace(&model.BatchTrace{BatchID: ids[0], PartID: ids[1], UsedQty: 1.25, Supplier: &supplier})
					if e != nil {
						return e
					}
					tids = append(tids, id)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				name, query string
				part        bool
				partial     bool
				want        []int64
			}{
				{"batch", "TRACE-B1", false, false, tids[:2]},
				{"part across products", "TRACE-A", true, false, []int64{tids[0], tids[2]}},
				{"unknown batch", "missing", false, false, []int64{}},
				{"unknown part", "missing", true, false, []int64{}},
				{"batch injection", "' OR 1=1 --", false, false, []int64{}},
				{"part injection", "' OR 1=1 --", true, false, []int64{}},
				// 部分匹配：只输片段就命中。
				{"batch partial fragment", "E-B1", false, true, tids[:2]},
				{"batch partial across batches", "ACE-B", false, true, tids},
				{"part partial by code", "RACE-A", true, true, []int64{tids[0], tids[2]}},
				{"part by name exact", "温控器", true, false, []int64{tids[1]}},
				{"part by name partial", "温控", true, true, []int64{tids[1]}},
				// 部分匹配不是通配符：% 与 _ 按字面字符比对。
				{"batch partial wildcard is literal", "%", false, true, []int64{}},
				{"part partial wildcard is literal", "_", true, true, []int64{}},
				{"part partial injection", "' OR 1=1 --", true, true, []int64{}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var rows []model.BatchTrace
					var e error
					if tc.part {
						rows, e = store.TraceByPart(tc.query, tc.partial)
					} else {
						rows, e = store.TraceByProduct(tc.query, tc.partial)
					}
					if e != nil {
						t.Fatal(e)
					}
					if rows == nil {
						t.Fatal("empty results must serialize as []")
					}
					got := []int64{}
					for _, r := range rows {
						got = append(got, r.ID)
						if r.UsedQty != 1.25 || r.Supplier == nil || *r.Supplier != "合成供应商" {
							t.Fatalf("lost trace data: %+v", r)
						}
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("trace ids=%v want=%v", got, tc.want)
					}
				})
			}

			// 上限：501 个批次各一条投料，部分匹配只能回 500 条（LIMIT 500）。
			if err := store.WithTx(func(tx TxOps) error {
				capPartID, e := tx.CreatePart(&model.Part{Code: "TRACE-CAP-P", Name: "上限零件", Unit: "个", Status: 1})
				if e != nil {
					return e
				}
				capProductID, e := tx.CreateProduct(&model.Product{Code: "TRACE-CAP", Name: "上限产品", Unit: "台", Status: 1})
				if e != nil {
					return e
				}
				for i := 0; i < 501; i++ {
					bid, e := tx.CreateBatch(&model.ProductBatch{
						BatchNo:   fmt.Sprintf("TRACE-CAP-%03d", i),
						ProductID: capProductID,
						PlanQty:   1,
					})
					if e != nil {
						return e
					}
					if _, e := tx.CreateTrace(&model.BatchTrace{BatchID: bid, PartID: capPartID, UsedQty: 1}); e != nil {
						return e
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			t.Run("limit", func(t *testing.T) {
				rows, e := store.TraceByProduct("TRACE-CAP", true)
				if e != nil {
					t.Fatal(e)
				}
				if len(rows) != 500 {
					t.Fatalf("部分匹配应被 LIMIT 500 截断：got=%d", len(rows))
				}
			})
		})
	}
}

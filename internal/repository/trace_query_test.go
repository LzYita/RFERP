package repository

import (
	"app/internal/migrate"
	"app/internal/model"
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
				for _, code := range []string{"TRACE-A", "TRACE-B"} {
					id, e := tx.CreatePart(&model.Part{Code: code, Name: code, Unit: "个", Status: 1})
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
				want        []int64
			}{
				{"batch", "TRACE-B1", false, tids[:2]}, {"part across products", "TRACE-A", true, []int64{tids[0], tids[2]}},
				{"unknown batch", "missing", false, []int64{}}, {"unknown part", "missing", true, []int64{}},
				{"batch injection", "' OR 1=1 --", false, []int64{}}, {"part injection", "' OR 1=1 --", true, []int64{}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var rows []model.BatchTrace
					var e error
					if tc.part {
						rows, e = store.TraceByPart(tc.query)
					} else {
						rows, e = store.TraceByProduct(tc.query)
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
		})
	}
}

package repository

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"app/internal/migrate"
	"app/internal/model"
)

func TestClientRegistrationQuota(t *testing.T) {
	for _, backend := range []string{"sqlite", "mysql"} {
		t.Run(backend, func(t *testing.T) {
			var store Store
			var db *sqlx.DB
			var err error
			if backend == "mysql" {
				dsn := os.Getenv("RFERP_TEST_DSN")
				if dsn == "" {
					t.Skip("RFERP_TEST_DSN not set")
				}
				db, err = sqlx.Connect("mysql", dsn)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				_, err = migrate.Run(db, migrate.Options{
					DSN: dsn, BackupDir: testTempDir(t),
					Backup: func(string, string, string) (string, error) { return "test-backup", nil },
				})
				store = New(db)
			} else {
				db, err = OpenSQLite(filepath.Join(testTempDir(t), "quota.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
				_, err = migrate.RunSQLite(db, migrate.SQLiteOptions{})
				store = NewSQLite(db)
			}
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(5)
			if _, err := db.Exec("DELETE FROM devices"); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveLicense(&model.LicenseRecord{
				Payload: "test", Signature: "test", ImportedAt: time.Now(),
			}); err != nil {
				t.Fatal(err)
			}

			run := func(sameDevice bool) {
				t.Helper()
				start := make(chan struct{})
				results := make(chan error, 5)
				var workers sync.WaitGroup
				for index := 0; index < 5; index++ {
					deviceIndex := index + 1
					if sameDevice {
						deviceIndex = 1
					}
					workers.Go(func() {
						<-start
						results <- store.RegisterClientDevice(&model.Device{
							DeviceIDHash: fmt.Sprintf("%064x", deviceIndex),
							Name:         "client", Kind: model.DeviceKindClient,
						}, 1)
					})
				}
				close(start)
				workers.Wait()
				close(results)
				allowed, limited := 0, 0
				for err := range results {
					switch {
					case err == nil:
						allowed++
					case errors.Is(err, ErrDeviceLimit):
						limited++
					default:
						t.Fatalf("registration failed: %v", err)
					}
				}
				want := 1
				if sameDevice {
					want = 5
				}
				if allowed != want || limited != 5-want {
					t.Fatalf("allowed=%d limited=%d want=%d/%d", allowed, limited, want, 5-want)
				}
				if used, err := store.CountDevices(model.DeviceKindClient, ""); err != nil || used != 1 {
					t.Fatalf("registered=%d err=%v", used, err)
				}
			}
			run(false)
			if _, err := db.Exec("DELETE FROM devices"); err != nil {
				t.Fatal(err)
			}
			run(true)
			deviceID := fmt.Sprintf("%064x", 1)
			if err := store.RevokeDevice(deviceID, "test"); err != nil {
				t.Fatal(err)
			}
			if err := store.RegisterClientDevice(&model.Device{DeviceIDHash: deviceID}, 1); !errors.Is(err, ErrDeviceRevoked) {
				t.Fatalf("revoked device: %v", err)
			}
			newDevice := &model.Device{DeviceIDHash: fmt.Sprintf("%064x", 6), Kind: model.DeviceKindClient}
			if err := store.RegisterClientDevice(newDevice, 1); !errors.Is(err, ErrDeviceLimit) {
				t.Fatalf("revocation released quota: %v", err)
			}
			if err := store.DeleteDevice(deviceID); err != nil {
				t.Fatal(err)
			}
			if err := store.RegisterClientDevice(newDevice, 1); err != nil {
				t.Fatalf("delete did not release quota: %v", err)
			}
		})
	}
}

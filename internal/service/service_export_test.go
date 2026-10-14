package service

import (
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
)

type exportQueryStore struct {
	repository.Store
	query func() ([]model.Product, error)
}

func (s exportQueryStore) ListProducts() ([]model.Product, error) { return s.query() }

func exportTestCandidate(saveDir string, at time.Time, i int) string {
	base := filepath.Join(saveDir, at.Format("20060102_150405"))
	if i > 1 {
		return fmt.Sprintf("%s-%d", base, i)
	}
	return base
}

func populateExportCandidates(t *testing.T, saveDir string, at time.Time, count int) {
	t.Helper()
	for i := 1; i <= count; i++ {
		p := exportTestCandidate(saveDir, at, i)
		if err := os.Mkdir(p, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "existing.txt"), []byte(fmt.Sprintf("existing-%d", i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertExportCandidatesPreserved(t *testing.T, saveDir string, at time.Time, count int) {
	t.Helper()
	for i := 1; i <= count; i++ {
		p := filepath.Join(exportTestCandidate(saveDir, at, i), "existing.txt")
		data, err := os.ReadFile(p)
		if err != nil || string(data) != fmt.Sprintf("existing-%d", i) {
			t.Fatalf("old export %d changed: %q, %v", i, data, err)
		}
	}
}

func TestExportAllDataCSVExhaustionPreservesExistingDirectories(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	populateExportCandidates(t, dir, at, 99)
	calls := 0
	svc := NewWithSnapshot(exportQueryStore{query: func() ([]model.Product, error) { calls++; return nil, errors.New("must not query") }}, nil, nil)
	files, subDir, err := svc.exportAllDataCSV(dir, at)
	if !errors.Is(err, os.ErrExist) || files != nil || subDir != "" {
		t.Fatalf("exhaustion: files=%v dir=%q err=%v", files, subDir, err)
	}
	if calls != 0 {
		t.Fatalf("database queried %d times after exhaustion", calls)
	}
	assertExportCandidatesPreserved(t, dir, at, 99)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 99 {
		t.Fatalf("directory count=%d, err=%v", len(entries), err)
	}
}

func TestExportAllDataCSVLastCandidateCleansOnlyNewDirectory(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	populateExportCandidates(t, dir, at, 98)
	queryErr := errors.New("query failed after allocation")
	calls := 0
	svc := NewWithSnapshot(exportQueryStore{query: func() ([]model.Product, error) {
		calls++
		if info, err := os.Stat(exportTestCandidate(dir, at, 99)); err != nil || !info.IsDir() {
			t.Fatalf("last candidate not allocated: %v", err)
		}
		return nil, queryErr
	}}, nil, nil)
	files, subDir, err := svc.exportAllDataCSV(dir, at)
	if !errors.Is(err, queryErr) || files != nil || subDir != "" || calls != 1 {
		t.Fatalf("files=%v dir=%q err=%v calls=%d", files, subDir, err, calls)
	}
	assertExportCandidatesPreserved(t, dir, at, 98)
	if _, err := os.Stat(exportTestCandidate(dir, at, 99)); !os.IsNotExist(err) {
		t.Fatalf("new incomplete directory remains: %v", err)
	}
}

func TestExportAllDataCSVOccupiedFileIsPreserved(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	old := exportTestCandidate(dir, at, 1)
	if err := os.WriteFile(old, []byte("existing file"), 0600); err != nil {
		t.Fatal(err)
	}
	queryErr := errors.New("query error")
	svc := NewWithSnapshot(exportQueryStore{query: func() ([]model.Product, error) {
		if info, err := os.Stat(exportTestCandidate(dir, at, 2)); err != nil || !info.IsDir() {
			t.Fatalf("suffix candidate not allocated: %v", err)
		}
		return nil, queryErr
	}}, nil, nil)
	_, _, err := svc.exportAllDataCSV(dir, at)
	if !errors.Is(err, queryErr) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(old)
	if err != nil || string(data) != "existing file" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
	if _, err := os.Stat(exportTestCandidate(dir, at, 2)); !os.IsNotExist(err) {
		t.Fatalf("new directory remains: %v", err)
	}
}

func TestExportAllDataCSVAllocationErrorDoesNotQueryOrChangeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parent-file")
	if err := os.WriteFile(path, []byte("keep parent"), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	svc := NewWithSnapshot(exportQueryStore{query: func() ([]model.Product, error) { calls++; return nil, errors.New("must not query") }}, nil, nil)
	files, dir, err := svc.exportAllDataCSV(path, time.Now())
	if err == nil || files != nil || dir != "" || calls != 0 {
		t.Fatalf("files=%v dir=%q err=%v calls=%d", files, dir, err, calls)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep parent" {
		t.Fatalf("parent changed: %q, %v", data, err)
	}
}

func TestExportAllDataCSVConcurrentFailuresOwnSeparateDirectories(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	populateExportCandidates(t, dir, at, 1)
	const n = 8
	ready := make(chan struct{}, n)
	releases := make([]chan struct{}, n)
	released := make([]bool, n)
	queryErr := errors.New("injected concurrent query failure")
	type result struct {
		id    int
		files map[string]string
		dir   string
		err   error
	}
	done := make(chan result, n)
	var wg sync.WaitGroup
	t.Cleanup(func() {
		for i, release := range releases {
			if !released[i] {
				close(release)
			}
		}
		wg.Wait()
	})
	for i := range releases {
		releases[i] = make(chan struct{})
	}
	for i := range releases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := NewWithSnapshot(exportQueryStore{query: func() ([]model.Product, error) { ready <- struct{}{}; <-releases[i]; return nil, queryErr }}, nil, nil)
			files, subDir, err := svc.exportAllDataCSV(dir, at)
			done <- result{i, files, subDir, err}
		}()
	}
	for i := 0; i < n; i++ {
		select {
		case <-ready:
		case <-time.After(10 * time.Second):
			t.Fatal("exports did not all reach query")
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != n+1 {
		t.Fatalf("active exports share directories: count=%d error=%v", len(entries), err)
	}
	for i := range releases {
		close(releases[i])
		released[i] = true
		select {
		case r := <-done:
			if r.id != i || !errors.Is(r.err, queryErr) || r.files != nil || r.dir != "" {
				t.Fatalf("unexpected export result: %+v", r)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("released export did not finish")
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != n-i {
			t.Fatalf("cleanup changed another export: count=%d error=%v", len(entries), err)
		}
		assertExportCandidatesPreserved(t, dir, at, 1)
	}
}

func TestExportAllDataCSVSuccessKeepsTimestampAndPriorExport(t *testing.T) {
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "export.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatal(err)
	}
	store := repository.NewSQLite(db)
	if _, err := db.Exec("INSERT INTO products(code,name,unit,status) VALUES (?,?,?,?)", "P-1", "export row", "piece", 1); err != nil {
		t.Fatal(err)
	}
	svc := NewWithSnapshot(store, nil, nil)
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	first, firstDir, err := svc.exportAllDataCSV(dir, at)
	if err != nil || firstDir != exportTestCandidate(dir, at, 1) {
		t.Fatalf("first export: dir=%q error=%v", firstDir, err)
	}
	if _, err := db.Exec("UPDATE products SET name=? WHERE code=?", "new export row", "P-1"); err != nil {
		t.Fatal(err)
	}
	second, secondDir, err := svc.exportAllDataCSV(dir, at)
	if err != nil || secondDir != exportTestCandidate(dir, at, 2) {
		t.Fatalf("second export: dir=%q error=%v", secondDir, err)
	}
	for _, result := range []struct {
		files            map[string]string
		dir, productName string
	}{{first, firstDir, "export row"}, {second, secondDir, "new export row"}} {
		files := result.files
		if len(files) != 8 {
			t.Fatalf("file count=%d, want 8", len(files))
		}
		for _, key := range []string{"products", "parts", "bom_items", "product_batches", "batch_consumptions", "batch_trace", "batch_skip_parts", "audit_log"} {
			if files[key] != filepath.Join(result.dir, key+".csv") {
				t.Fatalf("%s file belongs to wrong export directory: %q", key, files[key])
			}
			data, err := os.ReadFile(files[key])
			if err != nil {
				t.Fatalf("read %s: %v", key, err)
			}
			rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\uFEFF"))).ReadAll()
			if err != nil || len(rows) == 0 {
				t.Fatalf("invalid %s csv: rows=%v error=%v", key, rows, err)
			}
			if key == "products" && (len(rows) != 2 || rows[1][0] != "P-1" || rows[1][1] != result.productName) {
				t.Fatalf("product export changed: %v", rows)
			}
		}
	}
}

func TestExportAllDataCSVFailurePreservesOtherActiveExport(t *testing.T) {
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "export.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatal(err)
	}
	store := repository.NewSQLite(db)
	dir := t.TempDir()
	at := time.Date(2026, 10, 14, 12, 34, 56, 0, time.UTC)
	populateExportCandidates(t, dir, at, 1)
	queryErr := errors.New("first export fails")
	type result struct {
		files map[string]string
		dir   string
		err   error
	}
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	released := []bool{false, false}
	var wg sync.WaitGroup
	t.Cleanup(func() {
		for i, release := range releases {
			if !released[i] {
				close(release)
			}
		}
		wg.Wait()
	})
	ready := make(chan struct{}, 1)
	failed := make(chan result, 1)
	complete := make(chan result, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc := NewWithSnapshot(exportQueryStore{query: func() ([]model.Product, error) { ready <- struct{}{}; <-releases[0]; return nil, queryErr }}, nil, nil)
		files, subDir, err := svc.exportAllDataCSV(dir, at)
		failed <- result{files, subDir, err}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("first export did not reach query")
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc := NewWithSnapshot(exportQueryStore{Store: store, query: func() ([]model.Product, error) { ready <- struct{}{}; <-releases[1]; return store.ListProducts() }}, nil, nil)
		files, subDir, err := svc.exportAllDataCSV(dir, at)
		complete <- result{files, subDir, err}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("second export did not reach query")
	}
	close(releases[0])
	released[0] = true
	select {
	case r := <-failed:
		if !errors.Is(r.err, queryErr) || r.files != nil || r.dir != "" {
			t.Fatalf("failed export: %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first export did not finish")
	}
	if _, err := os.Stat(exportTestCandidate(dir, at, 2)); !os.IsNotExist(err) {
		t.Fatalf("failed directory remains: %v", err)
	}
	if info, err := os.Stat(exportTestCandidate(dir, at, 3)); err != nil || !info.IsDir() {
		t.Fatalf("other active directory lost: %v", err)
	}
	assertExportCandidatesPreserved(t, dir, at, 1)
	close(releases[1])
	released[1] = true
	select {
	case r := <-complete:
		if r.err != nil || r.dir != exportTestCandidate(dir, at, 3) || len(r.files) != 8 {
			t.Fatalf("other export failed: %+v", r)
		}
		for _, path := range r.files {
			if filepath.Dir(path) != r.dir {
				t.Fatalf("CSV outside active directory: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil || len(data) == 0 {
				t.Fatalf("CSV lost after another export failed: %q, %v", data, err)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second export did not finish")
	}
}

func TestExportAllDataCSVReturnsQueryErrorAndRemovesIncompleteDirectory(t *testing.T) {
	svc, mock := newMockService(t)
	saveDir := t.TempDir()
	queryErr := errors.New("products query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM products ORDER BY id")).
		WillReturnError(queryErr)

	files, subDir, err := svc.ExportAllDataCSV(saveDir)
	if !errors.Is(err, queryErr) {
		t.Fatalf("ExportAllDataCSV returned the wrong error: %v", err)
	}
	if files != nil || subDir != "" {
		t.Fatalf("ExportAllDataCSV returned output on failure: files=%v dir=%q", files, subDir)
	}
	entries, readErr := os.ReadDir(saveDir)
	if readErr != nil {
		t.Fatalf("read export parent: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("incomplete export directory was not removed: %s", filepath.Join(saveDir, entries[0].Name()))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestExportAllDataCSVQueriesBatchConsumptions(t *testing.T) {
	svc, mock := newMockService(t)
	saveDir := t.TempDir()
	queryErr := errors.New("batch consumptions query failed")

	emptyProducts := sqlmock.NewRows([]string{
		"id", "code", "name", "spec", "unit", "status", "version", "created_at", "updated_at", "operator",
	})
	emptyParts := sqlmock.NewRows([]string{
		"id", "code", "name", "spec", "unit", "part_type", "stock_qty", "warn_qty", "status", "version",
		"created_at", "updated_at", "operator", "supplier",
	})
	emptyBatches := sqlmock.NewRows([]string{
		"id", "batch_no", "product_id", "plan_qty", "produced_qty", "status", "version",
		"created_at", "updated_at", "operator", "customer", "consumption_recorded",
		"product_name", "product_code",
	})
	emptyBOM := sqlmock.NewRows([]string{
		"id", "product_id", "part_id", "quantity", "loss_rate", "remark", "version",
		"created_at", "updated_at", "operator", "replaceable", "use_mode", "part_code", "part_name",
	})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM products ORDER BY id")).WillReturnRows(emptyProducts)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM parts ORDER BY id")).WillReturnRows(emptyParts)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT b.*, p.name AS product_name, p.code AS product_code
		FROM product_batches b
		JOIN products p ON p.id = b.product_id
		ORDER BY b.id DESC`)).
		WillReturnRows(emptyBatches)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT b.*, p.code AS part_code, p.name AS part_name
		FROM bom_items b
		JOIN parts p ON p.id = b.part_id
		ORDER BY b.product_id, b.id`)).
		WillReturnRows(emptyBOM)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT batch_id, part_id, consumed_qty FROM batch_consumptions ORDER BY batch_id, part_id")).
		WillReturnError(queryErr)

	files, subDir, err := svc.ExportAllDataCSV(saveDir)
	if !errors.Is(err, queryErr) {
		t.Fatalf("ExportAllDataCSV returned the wrong error: %v", err)
	}
	if files != nil || subDir != "" {
		t.Fatalf("ExportAllDataCSV returned output on failure: files=%v dir=%q", files, subDir)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestExportAuditLogCSVReturnsQueryError(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "audit.csv")
	queryErr := errors.New("audit query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? ORDER BY id DESC")).
		WithArgs(start, end.Add(24*time.Hour)).
		WillReturnError(queryErr)

	count, err := svc.ExportAuditLogCSV(start, end, path)
	if count != 0 || !errors.Is(err, queryErr) {
		t.Fatalf("ExportAuditLogCSV = (%d, %v), want (0, query error)", count, err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("audit output exists after query failure: %v", statErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestExportAuditLogCSVReturnsFileCreationError(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	path := t.TempDir()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? ORDER BY id DESC")).
		WithArgs(start, end.Add(24*time.Hour)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "table_name", "record_id", "action", "old_data", "new_data", "operator", "created_at",
		}))

	count, err := svc.ExportAuditLogCSV(start, end, path)
	if count != 0 || err == nil {
		t.Fatalf("ExportAuditLogCSV = (%d, %v), want file creation error", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

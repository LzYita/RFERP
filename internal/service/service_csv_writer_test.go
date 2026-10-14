package service

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type testCSVOutput struct {
	bytes.Buffer
	writeCalls int
	failAfter  int
	writeErr   error
	closeErr   error
}

func TestFinalizeCSVByCopyExclusivePublishesAndRemovesSource(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "temp.csv"), filepath.Join(dir, "output.csv")
	want := bytes.Repeat([]byte("商品,数量\r\n"), 4096)
	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := finalizeCSVByCopyExclusive(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("published bytes differ: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
}

func TestFinalizeCSVByCopyExclusiveFailureCleanup(t *testing.T) {
	for _, kind := range []string{"existing file", "existing directory", "missing parent", "copy error"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			src, dst := filepath.Join(dir, "temp.csv"), filepath.Join(dir, "output.csv")
			if kind == "copy error" {
				if err := os.Mkdir(src, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "existing file":
				if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "existing directory":
				if err := os.Mkdir(dst, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dst, "sentinel"), []byte("old"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing parent":
				dst = filepath.Join(dir, "missing", "output.csv")
			}
			if err := finalizeCSVByCopyExclusive(src, dst); err == nil {
				t.Fatal("expected publication failure")
			}
			if _, err := os.Stat(src); !os.IsNotExist(err) {
				t.Fatalf("source remains: %v", err)
			}
			if kind == "existing file" || kind == "existing directory" {
				kept := dst
				if kind == "existing directory" {
					kept = filepath.Join(dst, "sentinel")
				}
				got, err := os.ReadFile(kept)
				if err != nil || string(got) != "old" {
					t.Fatalf("existing target changed: %q, %v", got, err)
				}
			} else if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatalf("partial target remains: %v", err)
			}
		})
	}
}

func TestFinalizeCSVByCopyExclusiveConcurrentWinner(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "output.csv")
	const count = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	type outcome struct {
		payload string
		err     error
	}
	results := make(chan outcome, count)
	for i := 0; i < count; i++ {
		payload := string(rune('A' + i))
		src := filepath.Join(dir, payload+".tmp")
		if err := os.WriteFile(src, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- outcome{payload, finalizeCSVByCopyExclusive(src, dst)} }()
	}
	close(start)
	wg.Wait()
	close(results)
	winners, winner := 0, ""
	for result := range results {
		if result.err == nil {
			winners++
			winner = result.payload
		}
	}
	got, err := os.ReadFile(dst)
	if winners != 1 || err != nil || string(got) != winner {
		t.Fatalf("winners=%d, bytes=%q, winner=%q, err=%v", winners, got, winner, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "output.csv" {
		t.Fatalf("leftovers: %v, %v", entries, err)
	}
}

func (w *testCSVOutput) Write(p []byte) (int, error) {
	w.writeCalls++
	if w.writeErr != nil && w.writeCalls >= w.failAfter {
		return 0, w.writeErr
	}
	return w.Buffer.Write(p)
}

func (w *testCSVOutput) Close() error {
	return w.closeErr
}

func csvOutputFactory(output io.WriteCloser) func(string) (io.WriteCloser, error) {
	return func(string) (io.WriteCloser, error) {
		return output, nil
	}
}

func TestWriteCSVFileReturnsWriteError(t *testing.T) {
	writeErr := errors.New("disk write failed")
	output := &testCSVOutput{failAfter: 1, writeErr: writeErr}

	err := writeCSVFile("write-error.csv", []string{"header"}, nil, csvOutputFactory(output))
	if !errors.Is(err, writeErr) {
		t.Fatalf("writeCSVFile returned the wrong error: %v", err)
	}
}

func TestWriteCSVFileReturnsFlushError(t *testing.T) {
	flushErr := errors.New("disk flush failed")
	output := &testCSVOutput{failAfter: 2, writeErr: flushErr}

	err := writeCSVFile("flush-error.csv", []string{"header"}, [][]string{{"row"}}, csvOutputFactory(output))
	if !errors.Is(err, flushErr) {
		t.Fatalf("writeCSVFile returned the wrong error: %v", err)
	}
}

func TestWriteCSVFileReturnsCloseError(t *testing.T) {
	closeErr := errors.New("file close failed")
	output := &testCSVOutput{closeErr: closeErr}

	err := writeCSVFile("close-error.csv", []string{"header"}, nil, csvOutputFactory(output))
	if !errors.Is(err, closeErr) {
		t.Fatalf("writeCSVFile returned the wrong error: %v", err)
	}
}

func TestWriteCSVFileAtomicPreservesExistingOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.csv")
	original := []byte("existing export\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatalf("write existing export: %v", err)
	}

	if err := writeCSVFileAtomic(path, []string{"header"}, [][]string{{"new"}}); err == nil {
		t.Fatal("writeCSVFileAtomic overwrote an existing export")
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing export: %v", err)
	}
	if !bytes.Equal(actual, original) {
		t.Fatalf("existing export changed: %q", actual)
	}
}

func TestFinalizeCSVExclusiveDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	tempPath := filepath.Join(dir, ".audit.csv.tmp")
	path := filepath.Join(dir, "audit.csv")
	if err := os.WriteFile(tempPath, []byte("new export\n"), 0o600); err != nil {
		t.Fatalf("write temp export: %v", err)
	}
	if err := os.WriteFile(path, []byte("existing export\n"), 0o600); err != nil {
		t.Fatalf("write existing export: %v", err)
	}

	if err := finalizeCSVExclusive(tempPath, path); err == nil {
		t.Fatal("finalizeCSVExclusive overwrote an existing export")
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing export: %v", err)
	}
	if string(actual) != "existing export\n" {
		t.Fatalf("existing export changed: %q", actual)
	}
}

func TestFinalizeCSVExclusivePublishesNewTarget(t *testing.T) {
	dir := t.TempDir()
	tempPath := filepath.Join(dir, ".audit.csv.tmp")
	path := filepath.Join(dir, "audit.csv")
	if err := os.WriteFile(tempPath, []byte("new export\n"), 0o600); err != nil {
		t.Fatalf("write temp export: %v", err)
	}

	if err := finalizeCSVExclusive(tempPath, path); err != nil {
		t.Fatalf("finalizeCSVExclusive: %v", err)
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published export: %v", err)
	}
	if string(actual) != "new export\n" {
		t.Fatalf("published export = %q", actual)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists, stat err = %v", err)
	}
}

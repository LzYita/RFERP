package service

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type testCSVOutput struct {
	bytes.Buffer
	writeCalls int
	failAfter  int
	writeErr   error
	closeErr   error
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

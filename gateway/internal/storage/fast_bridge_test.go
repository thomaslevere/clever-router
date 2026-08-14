package storage

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestZstdTarArchiveAndExtract(t *testing.T) {
	tmpSource := t.TempDir()
	tmpTarget := t.TempDir()

	// Create test files in source directory
	file1 := filepath.Join(tmpSource, "config.json")
	file2 := filepath.Join(tmpSource, "sub", "app.sqlite")
	_ = os.MkdirAll(filepath.Dir(file2), 0755)

	content1 := []byte(`{"version": "1.0", "name": "test"}`)
	content2 := []byte(`SQLite format 3 - sample database bytes header`)

	if err := os.WriteFile(file1, content1, 0644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	if err := os.WriteFile(file2, content2, 0644); err != nil {
		t.Fatalf("write file2: %v", err)
	}

	// Archive with zstd + tar
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	tw := tar.NewWriter(enc)

	err = filepath.Walk(tmpSource, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, _ := filepath.Rel(tmpSource, path)
		hdr, _ := tar.FileInfoHeader(info, info.Name())
		hdr.Name = relPath
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		data, _ := os.ReadFile(path)
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	_ = tw.Close()
	_ = enc.Close()

	if buf.Len() == 0 {
		t.Fatalf("archive buffer is empty")
	}

	// Extract from zstd + tar into tmpTarget
	zr, err := zstd.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("zstd reader: %v", err)
	}
	defer zr.Close()

	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		targetPath := filepath.Join(tmpTarget, hdr.Name)
		_ = os.MkdirAll(filepath.Dir(targetPath), 0755)
		f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			t.Fatalf("open target: %v", err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			t.Fatalf("copy target: %v", err)
		}
		f.Close()
	}

	// Verify extracted files
	res1, err := os.ReadFile(filepath.Join(tmpTarget, "config.json"))
	if err != nil || !bytes.Equal(res1, content1) {
		t.Errorf("file1 content mismatch: got %q, want %q (err: %v)", string(res1), string(content1), err)
	}

	res2, err := os.ReadFile(filepath.Join(tmpTarget, "sub", "app.sqlite"))
	if err != nil || !bytes.Equal(res2, content2) {
		t.Errorf("file2 content mismatch: got %q, want %q (err: %v)", string(res2), string(content2), err)
	}
}

func TestFastVolumeBridgeMissingCredentials(t *testing.T) {
	_, err := NewFastVolumeBridge("", "", "", "", false)
	if err == nil {
		t.Fatal("expected error with empty credentials, got nil")
	}
}

func TestFastVolumeBridgeEmptySourceDir(t *testing.T) {
	// Bridge with nil client won't panic on missing source directory
	b := &FastVolumeBridge{}
	err := b.StreamSnapshotToS3(context.Background(), "/path/that/does/not/exist/999", "key.tar.zst")
	if err != nil {
		t.Fatalf("expected nil on nonexistent directory, got %v", err)
	}
}

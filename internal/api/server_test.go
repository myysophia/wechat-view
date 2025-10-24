package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractDateFromPath(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chatlogs/2025-09-23", nil)
	date, err := srv.extractDate(req)
	if err != nil {
		t.Fatalf("提取日期失败: %v", err)
	}
	if date != "2025-09-23" {
		t.Fatalf("期望 2025-09-23，得到 %s", date)
	}
}

func TestExtractDateFromQuery(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chatlogs?date=2025-09-24", nil)
	date, err := srv.extractDate(req)
	if err != nil {
		t.Fatalf("提取日期失败: %v", err)
	}
	if date != "2025-09-24" {
		t.Fatalf("期望 2025-09-24，得到 %s", date)
	}
}

func TestHandleChatlogSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "2025-09-25.json"), []byte(`{"date":"2025-09-25"}`), 0o644); err != nil {
		t.Fatalf("写入测试文件失败: %v", err)
	}
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chatlogs/2025-09-25", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，得到 %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"date":"2025-09-25"}` {
		t.Fatalf("返回内容不匹配: %s", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type 异常: %s", ct)
	}
}

func TestHandleChatlogNotFound(t *testing.T) {
	dir := t.TempDir()
	srv, err := NewServer(dir)
	if err != nil {
		t.Fatalf("创建服务失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/chatlogs/2025-01-01", nil)
	rec := httptest.NewRecorder()

	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("期望状态码 404，得到 %d", rec.Code)
	}
}

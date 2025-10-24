package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Server 提供访问原始聊天记录的 RESTful API。
type Server struct {
	dataDir string
	mux     *http.ServeMux
}

// NewServer 创建 API Server，dataDir 指向原始聊天记录目录。
func NewServer(dataDir string) (*Server, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("data dir is required")
	}
	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data dir: %w", err)
	}
	s := &Server{dataDir: absDir, mux: http.NewServeMux()}
	s.registerRoutes()
	return s, nil
}

// ServeHTTP 实现 http.Handler 接口。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/v1/chatlogs", s.handleChatlog)
	s.mux.HandleFunc("/api/v1/chatlogs/", s.handleChatlog)
	s.mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]string{"status": "ok"}
		writeJSON(w, http.StatusOK, resp)
	})
}

func (s *Server) handleChatlog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	date, err := s.extractDate(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.streamChatlog(w, r, date); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeError(w, http.StatusNotFound, fmt.Errorf("未找到 %s 的聊天记录", date))
			return
		}
		log.Printf("serve %s failed: %v", date, err)
		writeError(w, http.StatusInternalServerError, errors.New("读取聊天记录失败"))
		return
	}
}

func (s *Server) extractDate(r *http.Request) (string, error) {
	const prefix = "/api/v1/chatlogs"
	path := strings.TrimPrefix(r.URL.Path, prefix)
	path = strings.Trim(path, "/")
	date := path
	if date == "" {
		date = strings.TrimSpace(r.URL.Query().Get("date"))
	}
	if date == "" {
		return "", errors.New("缺少日期，请提供 YYYY-MM-DD 格式的 date")
	}
	if strings.Contains(date, "/") {
		return "", errors.New("日期格式非法")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", fmt.Errorf("日期格式非法: %w", err)
	}
	return date, nil
}

func (s *Server) streamChatlog(w http.ResponseWriter, r *http.Request, date string) error {
	filePath := filepath.Join(s.dataDir, fmt.Sprintf("%s.json", date))
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	return nil
}

func writeError(w http.ResponseWriter, status int, err error) {
	type resp struct {
		Error string `json:"error"`
	}
	writeJSON(w, status, resp{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("仅支持 %s 请求", allow))
}

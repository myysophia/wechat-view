package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wechat-view/internal/api"
	"wechat-view/internal/config"
)

func main() {
	var (
		cfgPath = flag.String("config", "report.config.json", "配置文件路径（可选）")
		dataDir = flag.String("data-dir", "", "原始聊天记录目录（默认读取配置文件）")
		listen  = flag.String("listen", ":8080", "HTTP 监听地址")
	)
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	cfg.Defaults()

	resolvedDataDir := firstNonEmpty(*dataDir, cfg.Report.DataDir, "data")
	if _, err := os.Stat(resolvedDataDir); errors.Is(err, os.ErrNotExist) {
		log.Printf("警告: 数据目录 %s 不存在，接口访问将返回 404", resolvedDataDir)
	} else if err != nil {
		log.Fatalf("检查数据目录失败: %v", err)
	}

	apiServer, err := api.NewServer(resolvedDataDir)
	if err != nil {
		log.Fatalf("初始化 API Server 失败: %v", err)
	}

	srv := &http.Server{
		Addr:         *listen,
		Handler:      apiServer,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  90 * time.Second,
	}

	go func() {
		log.Printf("REST API 服务启动，监听 %s，数据目录 %s", *listen, resolvedDataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("服务运行异常: %v", err)
		}
	}()

	waitForSignal()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("优雅关闭失败: %v", err)
	}
	log.Println("服务已退出")
}

func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	<-ch
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

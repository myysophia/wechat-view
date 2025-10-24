# WeChat Chatlog Daily Reporter (Go)

Core functionality: fetch local chatlog for a given day and talker, generate a static HTML daily report, and maintain a simple archive site.

<img width="1091" height="827" alt="image" src="https://github.com/user-attachments/assets/5b1633de-e270-423f-9b7a-1a843f19bf53" />

## Requirements

- Go 1.21+
- Local chatlog API running (WeChat data local only), e.g. `http://127.0.0.1:5030/api/v1/chatlog`

## Usage

```
go run ./cmd/report   --talker 27587714869@chatroom   --date 2025-09-17   --base-url http://127.0.0.1:5030   --data-dir data   --site-dir site --image-base-url http://127.0.0.1:5030  --force -v
```

Defaults:
- `--date` defaults to yesterday
- `--base-url` defaults to `http://127.0.0.1:5030`
- Raw JSON is saved under `data/YYYY-MM-DD.json`
- Day page is generated under `site/YYYY/MM/DD/index.html`
- Home index is generated at `site/index.html`

Re-run is idempotent. Use `--force` to refetch when raw exists.

### Images

- If your chatlog JSON includes image messages (type=3) with `contents.md5` and `contents.path`, set `--image-base-url` to your local service origin (e.g., `http://127.0.0.1:5030`).
- Image URLs are rendered as `${IMAGE_BASE_URL}/image/{md5},{path}`. They work when viewing locally; they will not load on Cloudflare Pages since that host cannot access your local machine.

## Scheduling (Local)

Run every day via Windows Task Scheduler or a simple script. Example PowerShell script `daily.ps1`:

```
$date = (Get-Date).AddDays(-1).ToString('yyyy-MM-dd')
go run ./cmd/report --talker '27587714869@chatroom' --date $date -v
```

Then register a daily task at 00:05 local time.

## Deploying to Cloudflare Pages

Push the repository to GitHub and connect it to Cloudflare Pages. Configure the build output directory to `site`. Since the chatlog API is local-only, the fetching must run locally. You can push the generated `site/` contents (and `data/` if desired) to GitHub, and Pages will publish the static site.

## REST API 服务

新增的 API 服务用于按日期对外提供 `data/` 目录中的原始聊天记录：

1. 启动方式
   ```
   go run ./cmd/api --listen :8080 --data-dir data --config report.config.json
   ```
   - `--listen`：HTTP 监听地址（默认 `:8080`）
   - `--data-dir`：原始聊天记录目录，默认读取配置文件中的 `report.dataDir`
   - `--config`：可选配置文件，用于复用现有目录配置

2. 核心接口
   - `GET /api/v1/chatlogs/{date}`：按 `YYYY-MM-DD` 返回对应的 JSON 文件内容
   - `GET /api/v1/chatlogs?date=YYYY-MM-DD`：同上，提供查询参数形式
   - `GET /healthz`：健康检查

3. 响应约定
   - 成功时直接返回原始 JSON 内容，`Content-Type: application/json`
   - 日期格式错误返回 `400`
   - 文件不存在返回 `404`
   - 发生其他错误时返回 `500`，并包含 `{ "error": "..." }` 的错误描述

## Notes

- The chatlog API JSON schema can vary; the client performs best-effort mapping of common fields (sender/content/timestamp, etc.). You can extend `internal/chatlog/client.go` once you know the exact schema.
- Keyword extraction is naive for ASCII tokens. For better Chinese segmentation and topic modeling, integrate a tokenizer later.
- If the API envelope is different (e.g., messages under another key), adapt `normalizeResponse`.

## Third-party modules

Clone the helper projects locally (they stay untracked by git):

```
git clone https://github.com/sjzar/chatlog.git third_party/chatlog
git clone https://github.com/sinyu1012/chatlog-web.git third_party/chatlog-web
```

Update them with `git pull` inside each directory whenever you need the latest upstream changes.


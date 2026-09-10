# Statsig signer

独立 Python 签名器，协议对齐 `https://grok.wodf.de/sign`。HEX 算法是 `hot/hex.js`，常驻 Node `vm.eval`（源码变了才重新编译）。grok2api 把 `StatsigSignerURL` 指到签名服务地址。签名 HTTP 接口不接收 SSO；常驻采集进程使用单独配置的 SSO。

## 协议

```http
POST /sign
Content-Type: application/json

{
  "method": "POST",
  "path": "/rest/app-chat/conversations/new",
  "environment": { "metaContent": "<grok-site-verification seed>" }
}
```

```json
{ "x-statsig-id": "<70-byte shell, base64 raw>" }
```

`metaContent` 是 48 字节 seed。签名器用当前 `data/pair.json` 里的 4 条 curves 和 `data/formula.json` 下标算 HEX，再套 70 字节壳（epoch `1682924400`，salt `obfiowerehiring`，末字节 `0x03`）。不覆盖全局 pair。

grok2api 允许的内网地址：`http://127.0.0.1:8788/sign`。

## 运行

### Docker Compose

`external-signer` profile 同时启动 `statsig-signer` 签名服务和 `statsig-watch` 常驻监测修复进程。
两者共用包含 Python、Node、Playwright 和 Chromium 的镜像，以非 root 用户运行。
签名端口仅在 Docker 网络内开放，不向宿主机发布 8788 端口。

首次部署将仓库根目录 `.env.example` 作为 `.env`，填写以下配置；`.env` 已被 Git 忽略：

| 配置 | 用途 |
| --- | --- |
| `STATSIG_LLM_BASE` | 外部兼容 API 基础地址，含 `/v1`，不含 `/chat/completions` |
| `STATSIG_LLM_API_KEY` | 该 API 的密钥 |
| `STATSIG_LLM_MODEL` | 支持工具调用的模型名称 |
| `STATSIG_GROK_SSO` | 用于浏览器采集的 Grok SSO Cookie 值，不含 `sso=` |
| `STATSIG_PROXY_SERVER` | 可选采集代理，例如 `http://proxy:8080` |
| `STATSIG_PROXY_USERNAME` / `STATSIG_PROXY_PASSWORD` | 可选代理认证 |

LLM 凭据只注入监测容器，不会注入签名服务。缺少必填配置时监测容器报错退出。
主项目管理页面中的内置签名模型配置不会传给独立签名器。

在仓库根目录一行重新构建并部署主项目、签名器和常驻监测（Docker Compose v2）：

```bash
docker compose -f docker-compose.yml -f docker-compose.statsig.yml --profile external-signer up -d --build --wait
```

主项目与签名器均从当前代码构建。监测进程使用同一份新签名器镜像启动。
命令等待主项目、签名器及监测心跳健康；首次真实抓包或修复可能仍在进行，需查看 `/fingerprint` 或监测日志。

首次部署后，在管理端运行设置中将 Statsig 模式设为 `url`，签名服务地址设为
`http://statsig-signer:8788/sign` 并保存。不要填写 `127.0.0.1`，那是主程序容器自身。
数据库中已有的运行设置会覆盖 YAML；仅修改 `config.yaml` 不一定能切换已有部署的签名地址。
保存后热生效，无需重启主程序，后续部署保留该设置。

```bash
docker compose ps
docker compose logs --tail=50 statsig-signer statsig-watch
docker compose exec statsig-signer python3 -c 'import urllib.request; print(urllib.request.urlopen("http://127.0.0.1:8788/fingerprint").read().decode())'
```

默认每 60 秒检查 HTML 发版指纹，首次启动和每 30 轮做浏览器 HEX 对照。
可通过 `STATSIG_WATCH_INTERVAL`、`STATSIG_DEEP_EVERY` 调整；后者设为 0 关闭周期性浏览器深探。
首次启动也会校验镜像附带的材料，变化时触发修复。失败默认等待 300 秒重试，可用
`STATSIG_REPAIR_RETRY_SECONDS` 调整；新发版不受旧失败的退避限制。

`statsig_state` 数据卷保存已发布的签名材料、发版指纹和监测状态，容器重建不会覆盖已经修复的材料。
候选算法在临时目录中修复和验证，通过后将代码、公式、curves 一起原子发布到 `active.json`。
签名服务每次请求读取一个完整版本，立即使用新材料；修复失败继续使用上次版本。
上次版本如果已被 Grok 淘汰，仍可能无法签名，需检查修复失败原因。

`GET /fingerprint` 返回 `verified`、`published_at`、`frontend` 和 `watch.last_result`。
健康检查表示服务及监测进程存活，不等于 Grok 已接受签名；需通过一次真实主项目请求确认。

### 本机运行

```bash
cd tools/statsig-signer
python3 -m statsig_signer serve --listen 127.0.0.1:8788
```

健康检查：`GET /health`。agent 写完 `data/*.json` 后进程按文件 mtime 热加载。

一次性签名：

```bash
python3 -m statsig_signer sign \
  --method POST \
  --path /rest/app-chat/conversations/new \
  --meta '62/GxIkYy2AWvW+igufpMkTf0VEYX4X8Pf4awTv5dWxpQeEFtcYt+MGmcGCsgGrM'
```

## 发版更新

默认本机 Playwright。可选 `--browser x2api` 走 x2api Browser Worker gRPC。

```bash
export GROK_SSO=...          # 或 STATSIG_SECRETS=/tmp/g2a-local-test/browser.secrets.json
python3 -m statsig_signer capture --browser local
python3 -m statsig_signer update --browser local
```

`update` 顺序：

1. 抓同一页 seed / 官方 HEX / curves
2. 当前公式能对上官方 HEX → 只写 `pair.json`
3. 真实网页的公式变化时进入 **Hermes 最小内核**（兼容 `/v1/chat/completions` 工具循环），多次采集确定唯一的公式下标
4. 候选代码必须通过额外新页面的 HEX 验证；Docker 监测进程在完整验证成功后才发布材料

```bash
export GROK2API_BASE=http://127.0.0.1:18000/v1
export GROK2API_KEY=...
export GROK2API_MODEL=grok-4
python3 -m statsig_signer update --browser local
```

x2api：

```bash
export X2API_ROOT=/Users/real/Documents/code/x2api
export X2API_BROWSER_ADDR=127.0.0.1:50051
python3 -m statsig_signer update --browser x2api
```

不要把 Playwright / x2api 接到生产签名路径。生产只跑 `serve`。

## 发版监测

grok.com HTML 里已经有发版标记，不必每轮开浏览器：

- `meta baggage` 的 `sentry-release=grok-web@<git sha>`（发版必变）
- RSC 里的 `\"curves\":`（4 组 SVG 控制点）
- `cdn.grok.com/_next/static/chunks/*.js` 文件名集合的哈希（内容哈希命名，JS 一变就变）

```bash
python3 -m statsig_signer watch --interval 60 --repair
```

默认 60 秒 GET `/imagine`。sentry / curves / chunks 任一变化才跑 Hermes。不要用最后 12 个 chunk 名，也不要把 HTML 脚本列表和 Playwright 抓到的 signer URL 交叉比较。`--deep-every 0` 表示识别只靠 HTML；HEX 对错由 repair 自己抓包验证。

## 测试

```bash
cd tools/statsig-signer
PYTHONPATH=. python3 -m unittest discover -s tests -v
```

私有浏览器抓包调试文件未随仓库发布，相关测试默认在文件缺失时跳过。
可通过 `STATSIG_HOOK_DEBUG` 指定该文件；仓库内的签名样本、修复流程和 HTTP 接口测试照常运行。

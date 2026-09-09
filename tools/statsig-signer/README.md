# Statsig signer

独立 Python 签名器，协议对齐 `https://grok.wodf.de/sign`。HEX 算法是 `hot/hex.js`，常驻 Node `vm.eval`（源码变了才重新编译）。grok2api 把 `StatsigSignerURL` 指到 `http://127.0.0.1:8788/sign`。不改 Go，不把 SSO 传给签名器。

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

仓库根目录的 Compose 默认启动独立的 `statsig-signer` 服务。镜像包含 Python 和 Node，
以非 root 用户运行，只监听容器内网端口，不向宿主机发布 8788 端口。
主程序等待签名器健康检查通过后启动。

在仓库根目录执行原有的本地构建命令即可同时部署两个服务（使用支持 `pull_policy: build` 的 Docker Compose v2）：

```bash
docker build -t grok2api:local . && GROK2API_IMAGE=grok2api:local docker compose up -d
```

首次部署后，在管理端运行设置中将 Statsig 模式设为 `url`，签名服务地址设为
`http://statsig-signer:8788/sign` 并保存。不要填写 `127.0.0.1`，那是主程序容器自身。
数据库中已有的运行设置会覆盖 YAML；仅修改 `config.yaml` 不一定能切换已有部署的签名地址。
保存后热生效，无需重启主程序，后续部署保留该设置。

```bash
docker compose ps
docker compose logs --tail=50 statsig-signer
docker compose exec statsig-signer python3 -c 'import urllib.request; print(urllib.request.urlopen("http://127.0.0.1:8788/health").read().decode())'
```

`pull_policy: build` 使每次 `docker compose up -d` 都按当前代码构建签名器，未变更的层使用缓存。
签名代码和 `data/pair.json`、`data/formula.json` 随镜像发布，不使用持久卷保留旧版本。
`serve` 不会自动抓取网页更新算法；网页算法变化时，先更新仓库中的代码和数据，再执行部署命令。
健康检查验证服务和数据可用，实际能否被 Grok 接受仍需通过一次真实请求确认。

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
3. 穷举下标能对上 → 写 `formula.json` + `pair.json`
4. 对不上 → **Hermes 最小内核**（grok2api `/v1/chat/completions` 工具循环）读 chunk、试公式，只有 HEX 匹配才 apply

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

## 测试

```bash
cd tools/statsig-signer
PYTHONPATH=. python3 -m unittest discover -s tests -v
```

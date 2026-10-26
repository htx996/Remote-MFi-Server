# Remote-MFi-Server

面向 FNOS/NAS 的 xcertplay Remote MFi HTTP 服务框架。项目提供可直接构建运行的
FastAPI、Bearer Token、requestId 幂等缓存、健康检查、JSON 日志和 Docker Compose；
真实 CH341F/I2C/MFi 芯片读写由你自己的硬件后端程序接入。

> 重要：本项目没有、也不会伪造 MFi 签名逻辑。没有真实硬件后端时，服务可以正常
> 启动并通过健康检查，但 `/mfi/sign` 和 `/mfi/reset` 会明确返回 503。

## 兼容基线

接口根据 `shilapi/xcertplay` 当前 `RemoteMfiAuthenticationClient` 核对，基线为 master
提交 `1298fb35e29ee30e7bd849cb0e751b52aa22a13f`（2026-10-18，1.4.2）：

- [`RemoteMfiAuthenticationClient.kt`](https://github.com/shilapi/xcertplay/blob/master/shared/src/main/java/com/shilapi/xcertplay/mfi/RemoteMfiAuthenticationClient.kt)
- [xcertplay Remote MFI README](https://github.com/shilapi/xcertplay#remote-mfi)

上游 README 目前注明 Remote Authentication 只有 BAA 路径做过测试。客户端源码同时
接受 `type=mfi`，但普通 MFi 芯片的端到端结果仍必须由你的真实证书、芯片和车机验证。

## 能直接运行与尚需接入的部分

| 部分 | 本项目状态 |
|---|---|
| FastAPI、三个 `/mfi/*` 接口 | 已实现，可直接运行 |
| Bearer Token | 已实现，Compose 默认要求启用 |
| certificate Base64 与 SHA-256 | 已实现并自动生成 |
| challenge Base64/长度校验 | 已实现，解码后 1–128 字节 |
| requestId 幂等、冲突检测、并发串行 | 已实现，单进程内存 TTL/LRU 缓存 |
| healthcheck 与 JSON 日志 | 已实现 |
| Docker/FNOS 设备映射 | 已提供 Compose override 和操作说明 |
| CH341F USB/I2C 事务 | **未伪造**，需要按你的板卡协议实现后端程序 |
| MFi 芯片证书读取、真实签名、RST | **未伪造**，由后端程序完成 |
| 实机 CarPlay/MFi 成功 | 必须在真实芯片、iPhone、车机上验证 |

## 项目结构

```text
Remote-MFi-Server/
├── app/                         FastAPI、鉴权、幂等和后端适配器
├── backend/                     放置你自己的 mfi-backend 可执行程序
├── secrets/                     可选的只读证书目录
├── docs/
│   ├── design.md
│   └── hardware-backend-contract.md
├── tests/                       API、并发幂等和命令适配器测试
├── .env.example
├── Dockerfile
├── docker-compose.yml           基础服务，不暴露 USB
└── docker-compose.usb.yml       显式映射实际 CH341 设备
```

## 一、从 ZIP 部署到 FNOS

### 0. 也可以直接使用 Docker Hub 镜像

已发布的多架构镜像为：

```text
hanfu1997/remote-mfi-server:1.0.0
```

镜像同时支持 `linux/amd64` 和 `linux/arm64`。当前 Docker Hub 仓库为私有仓库，先在
FNOS/NAS 上登录，再拉取镜像：

```bash
docker login -u hanfu1997
export REMOTE_MFI_IMAGE=hanfu1997/remote-mfi-server:1.0.0
docker compose pull
docker compose up -d --no-build
```

登录密码应使用 Docker Hub Personal Access Token，不要把 Docker Hub 主密码或 Token
写进 `docker-compose.yml`。如果把 `REMOTE_MFI_IMAGE` 写进 `.env`，后续执行 Compose
命令时就不需要重复 `export`。`latest` 会跟随 GitHub 的 `main` 分支，正式部署建议固定
使用 `1.0.0` 这类版本标签，便于升级和回滚。

### 1. 准备条件

- FNOS 已安装 Docker，并能使用 `docker compose`；若只使用 FNOS 图形界面的 Compose
  项目，也可以把同一目录作为项目目录导入。
- 已启用 SSH，或能通过 FNOS 文件管理器上传并解压 ZIP。
- 选择 FNOS 数据盘上的持久目录，不要放在系统临时目录。下面用
  `/vol1/docker/remote-mfi` 举例；你的卷名可能不同。
- 防火墙只向需要连接的局域网/VPN 网段开放端口，默认不对公网开放。

下载交付的 `Remote-MFi-Server-1.0.0.zip` 和同目录下的
`Remote-MFi-Server-1.0.0.zip.sha256`，上传到选定目录。通过 SSH 校验并解压：

```bash
mkdir -p /vol1/docker/remote-mfi
cd /vol1/docker/remote-mfi
sha256sum -c Remote-MFi-Server-1.0.0.zip.sha256
unzip Remote-MFi-Server-1.0.0.zip
cd Remote-MFi-Server
```

校验结果必须包含 `OK`。解压后的关键目录应为：

```text
/vol1/docker/remote-mfi/Remote-MFi-Server/
├── app/
├── backend/README.md
├── secrets/README.md
├── docs/
├── .env.example
├── Dockerfile
├── docker-compose.yml
└── docker-compose.usb.yml
```

### 2. 创建安全配置

先复制模板，再用本机 Python 直接写入 48 字节随机 Token；Token 不会出现在 shell
历史中：

```bash
cp .env.example .env
python3 - <<'PY'
from pathlib import Path
import secrets

path = Path(".env")
text = path.read_text(encoding="utf-8")
text = text.replace(
    "MFI_BEARER_TOKEN=",
    "MFI_BEARER_TOKEN=" + secrets.token_urlsafe(48),
    1,
)
path.write_text(text, encoding="utf-8")
path.chmod(0o600)
PY
```

服务会拒绝少于 32 字节的 Token，也会拒绝旧模板中的公开占位值。然后按实际环境修改
`.env` 的端口、后端命令、证书和 USB 节点。

如果放置真实后端或证书，确保容器 UID 10001 至少可读取，后端还必须可执行：

```bash
chmod 0755 backend
chmod 0555 backend/mfi-backend
chmod 0755 secrets
chmod 0444 secrets/certificate.bin
```

不要把私钥放入本项目；本框架只需要真实芯片后端和对应证书数据。

### 3. 首次启动与局域网验证

未映射 USB 时先启动基础服务：

```bash
docker compose up -d --build
docker compose ps
curl -fsS http://127.0.0.1:8765/healthz
```

再从同一局域网的另一台设备访问 `http://FNOS_IP:8765/healthz`。若本机可访问而局域网
不可访问，检查 FNOS 防火墙、端口映射和 VLAN/访客网络隔离。基础 Compose 为服务及
硬件后端进程合计设置 256 MiB 内存和 128 个 PID 上限；若你的经审计后端确实需要更
多资源，再按实测修改。

### 4. 备份、升级、回滚和移除

需要备份的本地状态只有 `.env`、`backend/` 和 `secrets/`。不要只保留容器本身。

升级时把新 ZIP 解压到新的版本目录，复制上述三个本地状态目录，先在不同端口启动并
验证；确认后停止旧目录中的 Compose，再让新目录使用正式端口。保留旧目录即可快速
回滚：停止新版本，重新在旧目录执行 `docker compose up -d`。

停止但保留全部配置：

```bash
docker compose down
```

只有确认备份后，才删除版本目录。项目没有数据库卷；`docker compose down` 不会删除
主机上的 `.env`、`backend/` 或 `secrets/`。

## 二、先启动 API 框架

以下步骤不要求硬件后端，适合先确认 FNOS 的 Docker、端口和防火墙。

```bash
cp .env.example .env
openssl rand -hex 32
```

若未使用上一节的自动写入方式，把第二条命令生成的随机值填入 `.env` 的
`MFI_BEARER_TOKEN`。随后启动：

```bash
docker compose up -d --build
docker compose ps
```

健康检查不需要 Token：

```bash
curl -fsS http://127.0.0.1:8765/healthz
```

预期：

```json
{"status":"ok"}
```

这里的 `ok` 只表示 HTTP 进程可用，不表示 CH341F 或 MFi 芯片已经接通。

未接入硬件后端时，携带正确 Token 调用签名会得到 503 和清楚的 `detail`，不会得到
假签名：

```bash
curl -i \
  -H 'Authorization: Bearer 你的Token' \
  -H 'Content-Type: application/json' \
  -d '{"challenge":"AQ==","requestId":"smoke-test"}' \
  http://127.0.0.1:8765/mfi/sign
```

## 三、接入真实 MFi 硬件后端

你需要提供一个可执行程序，例如：

```text
backend/mfi-backend
```

赋予权限：

```bash
chmod 0555 backend/mfi-backend
```

默认 `.env` 已将它配置为：

```dotenv
MFI_BACKEND_COMMAND=/opt/remote-mfi/backend/mfi-backend
```

服务会调用：

```text
mfi-backend certificate
mfi-backend sign
mfi-backend reset
```

请求走 JSON stdin，响应走 JSON stdout。精确字段、超时和错误约定见
[`docs/hardware-backend-contract.md`](docs/hardware-backend-contract.md)。后端必须按你的
CH341F 板卡、I2C 地址、MFi 芯片型号和复位接线实现真实硬件事务。

如果你已有从真实芯片读取出的、与该芯片签名对应的证书字节，也可以放到：

```text
secrets/certificate.bin
```

并启用：

```dotenv
MFI_CERTIFICATE_FILE=/run/secrets/remote-mfi/certificate.bin
MFI_CERTIFICATE_TYPE=mfi
MFI_PROTOCOL_MAJOR=3
```

这时 certificate 不调用后端，但 sign/reset 仍必须调用真实硬件程序。不要盲目复制
示例中的 `3`；`MFI_PROTOCOL_MAJOR` 必须与实际芯片协议一致。

## 四、在 FNOS 上识别和映射 CH341F USB

### 1. 找到实际设备

通过 SSH 登录 FNOS。先拔掉板卡执行一次 `lsusb`，再插入板卡执行一次，比较新增行：

```bash
lsusb
```

`xcertplay` 当前源码刻意不内置 CH341 VID/PID，因此不要仅凭网上常见值判断。以你的
真实 `lsusb` 输出为准。假设它显示在 Bus `001`、Device `004`，则设备节点通常是：

```text
/dev/bus/usb/001/004
```

核对节点和数字组 ID：

```bash
ls -l /dev/bus/usb/001/004
stat -c '%g' /dev/bus/usb/001/004
```

把实际结果写入 `.env`：

```dotenv
CH341_DEVICE_PATH=/dev/bus/usb/001/004
CH341_DEVICE_GID=实际数字GID
```

### 2. 启用 USB override

```bash
docker compose \
  -f docker-compose.yml \
  -f docker-compose.usb.yml \
  up -d --build
```

确认容器能看到节点：

```bash
docker compose exec remote-mfi-server \
  sh -c 'ls -l "$CH341_DEVICE_PATH"'
```

如果这里能看到节点，只能证明 Docker 映射成功；它不证明 I2C、MFi 地址、证书读取或
签名成功。下一步必须看你的 `mfi-backend` 自检和服务日志。

### 3. USB 路径变化

`/dev/bus/usb/BBB/DDD` 中的设备编号可能在重新插拔或重启后改变。长期部署建议在 FNOS
主机上根据实际 VID/PID/序列号创建 udev 稳定符号链接，再把链接作为
`CH341_DEVICE_PATH`。如果 FNOS 不便配置 udev，可在设备变化后更新 `.env` 并重建
容器。

另一种做法是把整个 `/dev/bus/usb` 绑定进容器并允许 USB major 189，但权限范围更大，
不作为默认模板。确需使用时，可自行在 override 中增加：

```yaml
services:
  remote-mfi-server:
    volumes:
      - /dev/bus/usb:/dev/bus/usb
    device_cgroup_rules:
      - "c 189:* rwm"
```

优先使用单设备映射。

### 4. 权限故障

基础镜像使用 UID/GID `10001:10001` 的非 root 用户。USB override 用 `group_add` 把
设备节点的数字 GID 加入容器。若仍出现 `Permission denied`：

1. 重新确认设备节点没有因重插而变化。
2. 重新执行 `stat -c '%g'`，确认 `.env` 中 GID 正确。
3. 确认后端文件对容器用户可读且可执行。
4. 查看是否有宿主机驱动独占设备。
5. 只在诊断期间临时以 root 运行容器；确认原因后恢复非 root。

## 五、环境变量

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `REMOTE_MFI_PORT` | `8765` | NAS 对外端口 |
| `MFI_AUTH_REQUIRED` | `true` | 是否要求 Bearer Token |
| `MFI_BEARER_TOKEN` | 无 | 必填的长随机 Token；未配置时业务接口返回 503 |
| `MFI_BACKEND_COMMAND` | 无 | 真实硬件后端命令，不通过 shell 执行 |
| `MFI_BACKEND_TIMEOUT_SECONDS` | `8` | 单次硬件命令超时，范围 0–120 秒 |
| `MFI_CERTIFICATE_FILE` | 无 | 可选原始证书文件，和 Base64 二选一 |
| `MFI_CERTIFICATE_BASE64` | 无 | 可选标准 Base64 证书，长内容更建议文件 |
| `MFI_CERTIFICATE_TYPE` | `mfi` | `mfi` 或 `baa` |
| `MFI_PROTOCOL_MAJOR` | `3` | 0–255，必须与真实硬件一致 |
| `IDEMPOTENCY_TTL_SECONDS` | `300` | 成功签名缓存时间 |
| `IDEMPOTENCY_MAX_ENTRIES` | `1024` | 最大缓存条数，超出时淘汰最旧项 |
| `MAX_REQUEST_BODY_BYTES` | `4096` | JSON 解析/鉴权前的请求体硬上限 |
| `LOG_LEVEL` | `INFO` | `DEBUG/INFO/WARNING/ERROR/CRITICAL` |
| `CH341_DEVICE_PATH` | 无 | USB override 使用的真实宿主机设备节点 |
| `CH341_DEVICE_GID` | 无 | USB 节点的数字 GID |

服务固定使用一个 Uvicorn worker。幂等缓存位于进程内存，容器重启后会清空；不要擅自
增加多 worker，否则每个 worker 会拥有独立缓存并可能重复访问芯片。

## 六、API 契约

三个业务接口均使用：

```http
Authorization: Bearer <MFI_BEARER_TOKEN>
Content-Type: application/json
```

### GET `/mfi/certificate`

成功响应：

```json
{
  "type": "mfi",
  "protocolMajor": 3,
  "certificate": "BASE64...",
  "certificateSha256": "64位小写十六进制"
}
```

SHA-256 由服务根据证书字节计算，避免配置值与证书不一致。

### POST `/mfi/sign`

请求：

```json
{
  "challenge": "BASE64...",
  "requestId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}
```

成功响应：

```json
{
  "signature": "BASE64..."
}
```

相同 `requestId` + 相同 challenge 返回缓存签名；相同 `requestId` + 不同 challenge
返回 HTTP 409。只有成功签名会进入缓存，后端错误不会缓存。

### POST `/mfi/reset`

请求：

```json
{}
```

成功响应：

```json
{
  "detail": ""
}
```

reset 不做假成功：真实后端复位失败时返回 5xx，并保留现有幂等缓存。

## 七、xcertplay 中配置

在 xcertplay 设置中选择 Remote MFi，并填写：

```text
Server: http://FNOS局域网IP:8765
Token:  .env 中的 MFI_BEARER_TOKEN
```

如果通过公网访问，建议优先使用 WireGuard/Tailscale 等 VPN。不要直接把明文 HTTP 和
NAS 端口暴露到公网。确需反向代理时，启用 HTTPS、限制来源、把请求体上限设置为与
`MAX_REQUEST_BODY_BYTES` 相同或更小，并配置合理超时，同时继续保留 Bearer Token。

## 八、日志和排查

查看状态与日志：

```bash
docker compose ps
docker compose logs --tail=200 remote-mfi-server
docker compose logs -f remote-mfi-server
```

日志为单行 JSON，包含请求 ID、路径、状态码和耗时。服务不会记录 Token、challenge、
signature、证书正文或请求体。

| 现象 | 常见原因 |
|---|---|
| `/healthz` 200，但 sign 503 | 未配置/无法启动真实 `MFI_BACKEND_COMMAND` |
| certificate 503 | 没有静态证书，且硬件后端不可用 |
| certificate 502 | 后端返回非法 JSON/Base64/类型/版本/长度 |
| sign 422 | challenge 不是标准 Base64，或解码长度不在 1–128 |
| sign 409 | 同一个 requestId 被用于不同 challenge |
| sign 503 timeout | 后端未在配置时间内完成，或 USB/I2C 阻塞 |
| 401 | xcertplay Token 缺失或不匹配 |
| 容器 unhealthy | HTTP 进程不可访问；查看 Docker 日志 |
| USB Permission denied | 节点/GID 已变化、映射错误或宿主机权限限制 |

## 九、本地测试

```bash
python3 -m venv .venv
.venv/bin/python -m pip install -r requirements-dev.txt
.venv/bin/python -m pytest -q
```

测试中的 `RecordingBackend` 只用于验证 API 编排、幂等和错误处理，测试字节不会被打包
成生产后端，也不会在未配置硬件时由服务返回。

## 安全提醒

- Token 至少使用 32 字节随机值；一旦泄露立即轮换。
- 不把 `.env`、私钥、真实证书或硬件工具输出提交到仓库。
- 使用 VPN 或 HTTPS；Bearer Token 在明文 HTTP 上可被窃听。
- 基础 Compose 不暴露 USB；只在确认实际节点后启用 USB override。
- 后端命令拥有访问物理芯片的能力，只挂载你审计过的程序。
- 证书接口成功、镜像构建成功、健康检查成功都不等于真实 MFi 认证成功；最终必须以
  xcertplay、iPhone、车机和真实芯片的端到端行为为准。

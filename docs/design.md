# Remote MFi Server 设计说明

## 目标

为 FNOS/NAS 提供一个可用 Docker Compose 部署的 Remote MFi HTTP 服务，兼容
`shilapi/xcertplay` 当前 `RemoteMfiAuthenticationClient` 的三个接口，并通过独立的
Go/libusb 辅助程序支持已核对的 CH341F + MFI337S3959 实板。

## 已确认的客户端契约

核对基线：`shilapi/xcertplay` master 提交
`1298fb35e29ee30e7bd849cb0e751b52aa22a13f`（2026-10-18，1.4.2）。

- `GET /mfi/certificate` 返回 `type`、`protocolMajor`、`certificate`、
  `certificateSha256`。证书是标准 Base64；SHA-256 是 64 个十六进制字符。
- `POST /mfi/sign` 接收 `challenge` 与 `requestId`，返回 `signature`。客户端发生可重试
  故障时会复用同一个 `requestId`。
- `POST /mfi/reset` 接收空 JSON 对象，返回 `detail`；客户端不重试 reset。
- challenge 解码后必须为 1–128 字节。
- 可选标准 Bearer Token；本项目部署模板默认要求启用。

## 方案选择

采用“API 服务 + 独立硬件后端命令”结构：FastAPI 负责网络协议、鉴权、校验、幂等、
日志和错误格式；镜像内置的 `/usr/local/bin/mfi-backend` 负责真实 CH341F/I²C/MFi
操作。服务通过无 shell 的子进程调用，以 JSON stdin/stdout 交换数据。管理员仍可通过
环境变量显式替换为自己审计的后端。

没有映射 USB 或芯片不可访问时，服务仍可启动并通过 HTTP 健康检查，但真实硬件调用
返回 5xx。项目不包含软件私钥签名、固定测试签名或伪造芯片响应。

## 安全与运行约束

- Bearer Token 使用常量时间比较，业务接口默认全部受保护；健康检查不要求 Token。
- 日志不记录 Token、challenge、signature、证书正文或请求体。
- `requestId` 缓存为单进程内存缓存；Compose 固定一个 Uvicorn worker。
- 同一进程中的硬件操作串行执行，避免多个请求同时访问单个芯片。
- 容器默认只读、移除 Linux capabilities，并使用非 root 用户。
- USB 设备不在基础 Compose 中默认暴露；管理员必须依据 NAS 上实际设备节点显式启用
  USB override。
- `/healthz` 固定为 HTTP 进程健康检查；硬件可用性由 `mfi-backend probe` 分开验证。

## 非目标

- 不承诺支持未经核对的 CH341/MFi 板卡、其他 MFi 芯片型号或不同接线。
- 不提供真实 MFi 证书、私钥、签名样本或公共认证服务。
- 不承诺没有真实硬件的端到端 CarPlay/MFi 认证成功。

## 内置硬件实现

镜像内置辅助程序只打开环境变量指定的 CH341 USB VID/PID，默认值为 `1a86:5512`；
CH341 I²C stream 默认运行在 100 kHz，MFi 业务地址固定为显式配置的 `0x11`。证书与
签名均来自 MFI337S3959 实际寄存器窗口。reset 只重开 USB 会话并重新探测，不拉低
物理 RST，以避免芯片锁存到无法读取当前配件证书的 `0x10` 地址。

辅助程序位于 [`../hardware-backend/`](../hardware-backend/)，按 GNU GPL v3 独立发布；
来源和确切上游提交见 [`../hardware-backend/NOTICE.md`](../hardware-backend/NOTICE.md)。
FastAPI 与它不进行语言级链接，仅使用 argv、JSON stdin/stdout、退出码和进程边界。

## 发布门禁

发布任务必须依赖独立测试任务。门禁在 Linux 上安装 libusb 开发包，运行全部 Python
测试、Go race detector 和 Go vet；随后才允许 Buildx 构建并推送 `linux/amd64` 与
`linux/arm64` 镜像。发布前分别通过 QEMU/本机运行每个目标架构的最终镜像
`mfi-backend --version`。这只证明各架构镜像可启动，不能替代 FNOS 上的真实 USB、
证书、签名或 `AA05 AuthenticationSucceeded` 验收。

# Remote MFi Server 设计说明

## 目标

为 FNOS/NAS 提供一个可用 Docker Compose 部署的 Remote MFi HTTP 服务框架，兼容
`shilapi/xcertplay` 当前 `RemoteMfiAuthenticationClient` 的三个接口，并明确保留真实
CH341F/I2C/MFi 芯片协议的接入边界。

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

采用“API 服务 + 外部硬件后端命令”结构：FastAPI 负责网络协议、鉴权、校验、幂等、
日志和错误格式；管理员提供的本地可执行程序负责真实 CH341F/I2C/MFi 操作。服务通过
无 shell 的子进程调用，以 JSON stdin/stdout 交换数据。

没有配置真实后端时，服务仍可启动、通过健康检查，并可在配置静态证书后提供证书；
签名和硬件重置返回 HTTP 503。项目不包含软件私钥签名、固定测试签名或伪造芯片响应。

## 安全与运行约束

- Bearer Token 使用常量时间比较，业务接口默认全部受保护；健康检查不要求 Token。
- 日志不记录 Token、challenge、signature、证书正文或请求体。
- `requestId` 缓存为单进程内存缓存；Compose 固定一个 Uvicorn worker。
- 同一进程中的硬件操作串行执行，避免多个请求同时访问单个芯片。
- 容器默认只读、移除 Linux capabilities，并使用非 root 用户。
- USB 设备不在基础 Compose 中默认暴露；管理员必须依据 NAS 上实际设备节点显式启用
  USB override。

## 非目标

- 不猜测或实现未知的 CH341F USB/I2C/MFi 寄存器事务。
- 不提供真实 MFi 证书、私钥、签名样本或公共认证服务。
- 不承诺没有真实硬件的端到端 CarPlay/MFi 认证成功。

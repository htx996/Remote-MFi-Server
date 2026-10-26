# CH341F + MFI337S3959 真实硬件后端设计

日期：2026-10-26

## 意图

在现有 `Remote-MFi-Server` FastAPI 服务中内置一个真实硬件辅助程序，使已经接入 FNOS/NAS 的
`CH341F (VID:PID 1a86:5512) + MFI337S3959` USB-A 3.3 V 板能够为 xcertplay 提供真实的证书读取、
challenge 签名和兼容 reset 操作。保留当前 HTTP API、Bearer Token、requestId 幂等缓存、日志、
健康检查和端口 `8765`，不生成软件私钥、固定签名或测试签名。

## 已确认事实

- 用户本地 `ibom.html` 与 `shilapi/ch341-to-mfi-chip` 的
  `USBA-3.3V-CH341-MFI337S3959/bom/ibom.html` SHA-256 完全相同：
  `d9d16918b78c2e746800b06d8897087a2791f41277ed93b23e7001c9f8756fc8`。
- 作者将该 USB-A 3.3 V 版本标记为已测试；硬件包含 CH341F、MFI337S3959、SDA/SCL 2.2 kΩ
  上拉和 RST 4.7 kΩ 上拉。
- NAS 已枚举 `1a86:5512`，设备节点当前为 `/dev/bus/usb/003/004`；接口未被内核驱动占用。
- 当前容器以 `10001:10001` 运行，已经获得该设备的 `rwm` 映射和补充组 `0`。
- 当前 FastAPI 容器健康，但 `/vol1/1000/Docker/mfi/backend` 为空，因此不存在真实硬件后端。
- xcertplay 当前实现使用 CH341 I²C stream、默认 100 kHz，并探测 MFi 地址 `0x10`/`0x11`。
  这块板应保持 RST 硬件上拉并使用 `0x11`；主动拉低 RST 可能使芯片锁存到 `0x10`，该地址下
  可能无法读取配件证书。

## 目标

1. 在镜像内提供 `/usr/local/bin/mfi-backend`，不再要求用户手工创建该文件。
2. 后端通过 libusb 访问 CH341F，并按 xcertplay 的真实寄存器流程访问 MFI337S3959。
3. 支持 `probe`、`certificate`、`sign` 和 `reset` 四个操作。
4. `certificate`、`sign`、`reset` 严格兼容现有 FastAPI command backend 契约。
5. 提供 amd64 和 arm64 Docker 镜像。
6. 先完成只读硬件探测，再读取证书，再进行独立签名测试，最后由 xcertplay/iPhone 完成
   `AA05 AuthenticationSucceeded` 验收。
7. 保留 `1.0.0` 作为一键回滚版本，新版本使用新的不可变版本标签。

## 非目标

- 不支持软件私钥、固定字节、challenge 回显或任何伪造签名。
- 本轮不支持 BAA 芯片或 MFI341/MFI343。
- 不主动脉冲 MFi RST，不宣称完成物理芯片复位。
- 不把 `/healthz` 改成硬件健康检查；它继续表示 HTTP 进程健康。
- 不默认映射整个 `/dev/bus/usb`，继续使用当前精确设备节点映射。
- 不宣称单元测试或证书读取等同于 iPhone 端认证成功。

## 方案比较

### 方案 A：后端内置现有镜像（已选）

在同一 Docker 镜像中聚合 FastAPI 服务和独立 Go/libusb 辅助程序。两者只通过 argv、JSON stdin、
JSON stdout 和退出码通信。

优点：保留当前 URL、Token、Compose 和幂等行为；部署简单；多架构发布统一。缺点：镜像需要增加
Go 构建阶段和 libusb 运行库，并明确处理 GPL-3.0 辅助程序的源码与许可证。

### 方案 B：替换为第三方 Go HTTP 服务（未选）

部署快，但会替换当前 FastAPI 服务；第三方项目明确标记为实验性，默认映射整个 USB 总线，且
Bearer Token 位于命令参数中。

### 方案 C：外挂单独二进制（未选）

对主镜像改动小，但需要用户自行维护 CPU 架构、libc、libusb 依赖、文件权限和升级流程。

## 组件边界

```text
xcertplay
    |
    | HTTP + Bearer Token
    v
FastAPI Remote-MFi-Server
    |  serialized subprocess + JSON stdin/stdout
    v
/usr/local/bin/mfi-backend
    |  libusb bulk transfers
    v
CH341F 1a86:5512
    |  I2C 100 kHz, address 0x11
    v
MFI337S3959
```

FastAPI 继续负责：鉴权、请求体限制、API 模型、requestId 冲突检测、幂等缓存、HTTP 错误映射和
安全日志。Go 辅助程序负责：USB 枚举/打开、CH341 stream 编解码、I²C 事务、MFi 寄存器协议和
硬件错误分类。

## 硬件后端命令契约

默认命令：

```text
MFI_BACKEND_COMMAND=/usr/local/bin/mfi-backend
```

### `probe`

该操作只用于容器内诊断，不由 xcertplay API 自动调用。

调用：

```text
mfi-backend probe
```

stdin：

```json
{}
```

stdout 仅返回非敏感字段：

```json
{
  "usbId": "1a86:5512",
  "i2cAddress": 17,
  "deviceVersion": 3,
  "protocolMajor": 3,
  "certificateLength": 607
}
```

诊断会分别探测 `0x11` 和 `0x10` 并报告非敏感结果；业务操作默认只使用明确配置的 `0x11`，
不会因为一次失败自动退到 `0x10`。只有管理员显式修改 `MFI_I2C_ADDRESS` 后，业务地址才改变。
输出不得包含证书正文、challenge 或 signature。

### `certificate`

stdin 为 `{}`。后端读取协议主版本、证书长度和证书窗口，并返回：

```json
{
  "type": "mfi",
  "protocolMajor": 3,
  "certificate": "BASE64_OF_REAL_CERTIFICATE"
}
```

FastAPI 继续计算 `certificateSha256`。

### `sign`

stdin：

```json
{
  "challenge": "BASE64_OF_REAL_CHALLENGE",
  "requestId": "..."
}
```

后端验证 challenge 解码后长度为 1..128，随后执行真实芯片流程：

1. 将大端 challenge 长度写入 `0x20`；
2. 将 challenge 写入 `0x21`；
3. 向 `0x10` 写 `0x01`；
4. 初始等待 10 ms，之后每 10 ms 轮询 `0x10`，最多 3 秒；
5. 成功状态必须为 `0x10`；
6. 从 `0x11` 读取签名长度，从 `0x12` 读取真实签名字节；
7. stdout 返回 Base64 signature。

### `reset`

每次 command backend 调用本来就是一个新进程和新 libusb 会话，因此 reset 的定义为：打开 CH341、
确认 `0x11` 芯片仍可读、关闭会话，并返回：

```json
{
  "detail": "USB session reopened; physical MFi RST was not pulsed to preserve I2C address 0x11"
}
```

如果探测失败则非零退出，不能虚报成功。该操作不拉低 RST，也不声称物理复位芯片。

## CH341 与 MFi 细节

- 默认 USB ID：`1a86:5512`。
- 默认 I²C 速度：100 kHz。
- CH341 stream 使用 32 字节 USB 包边界。
- MFi 寄存器读取使用“写寄存器选择 + STOP”，等待至少 5 ms，再执行独立纯读事务。
- 读取操作对睡眠/NACK 做最多 2 秒、20 ms 间隔的完整 select/read 重试。
- 证书窗口最大 128 字节，起始寄存器 `0x31`，逐窗口递增。
- USB、I²C 和芯片错误只写安全诊断到 stderr，stdout 必须只有一个 JSON 对象。
- FastAPI 已从后端子进程环境中移除 `MFI_BEARER_TOKEN`。

## 后端环境变量

辅助程序只读取与硬件相关的非秘密配置，不读取或接收 Bearer Token：

| 变量 | 默认值 | 作用 |
| --- | --- | --- |
| `CH341_USB_VID` | `0x1a86` | CH341 USB vendor ID |
| `CH341_USB_PID` | `0x5512` | CH341 USB product ID |
| `MFI_I2C_ADDRESS` | `0x11` | 业务操作使用的 7 位 MFi I²C 地址 |
| `CH341_I2C_SPEED_KHZ` | `100` | I²C 速度；首版只允许 `20/100/400/750` |

无效值必须在任何 USB/I²C 操作前使进程非零退出。`probe` 可额外诊断 `0x10`，但不得静默改变
`certificate`、`sign` 或 `reset` 的业务地址。

## 代码与许可证设计

- 新增独立目录 `hardware-backend/`，包含 Go module、源码、测试和 `LICENSE`。
- CH341/MFi 实现基于：
  - `shilapi/xcertplay` 当前 CH341/MFi 源码；
  - `cuckoohello/remote-mfi-for-xcertplay` GPL-3.0-only 的 Linux/libusb 移植。
- 复制或改写的辅助程序代码保持 GPL-3.0-only，并保留来源、原作者、上游 commit 和修改说明。
- 新增 `THIRD_PARTY_NOTICES.md`，明确辅助程序是独立进程，完整对应源码随仓库公开。
- 许可证说明固定记录上游 commit：`shilapi/xcertplay@1298fb35e29ee30e7bd849cb0e751b52aa22a13f`
  和 `cuckoohello/remote-mfi-for-xcertplay@b7bd0852539c00b36c7cd850990abb64cf300ead`。
- Python FastAPI 服务与辅助程序不进行语言级链接，只通过进程边界通信；Docker 镜像是二者的聚合分发。

## Docker 设计

- 增加 Go 1.23 Debian 构建阶段，安装 `libusb-1.0-0-dev` 和 `pkg-config`。
- Buildx 对每个目标平台运行对应架构的 Debian 构建阶段（必要时通过 QEMU），让 CGO 与目标架构的
  libusb 一致；不从 amd64 主机构造未经验证的 arm64 CGO 交叉链接。
- 分别产生 linux/amd64 和 linux/arm64 辅助程序，并在 CI 中至少启动各架构镜像执行
  `mfi-backend --version`；硬件 USB 验收只在真实 NAS 架构上完成。
- Python 运行阶段安装最小 `libusb-1.0-0` 运行库。
- 辅助程序安装到 `/usr/local/bin/mfi-backend`，不会被当前空的 `/opt/remote-mfi/backend` 挂载覆盖。
- 容器继续以 `10001:10001` 运行；Compose 使用 `group_add: "0"` 访问 `root:root 0664` 的 USB 节点。
- 保留 `cap_drop: [ALL]`、`no-new-privileges` 和只读根文件系统。
- `.env.example` 默认改为 `MFI_BACKEND_COMMAND=/usr/local/bin/mfi-backend`。
- 用户仍可显式改回 `/opt/remote-mfi/backend/mfi-backend`，挂载自己的替代后端。

## 测试策略

开发遵循测试先行：每个新行为先写失败测试并确认失败，再实现最小代码。

### 纯单元测试

- CH341 configuration 和 transaction stream 的精确字节序列；
- 32 字节包边界与分包；
- 写入 ACK/NACK 状态处理；
- 读数据从响应尾部对齐；
- 读寄存器的 STOP + 5 ms 间隔；
- MFi 地址选择、协议版本、证书长度和窗口读取；
- challenge 长度限制；
- challenge 写入、状态轮询、超时、错误码和签名读取；
- `probe/certificate/sign/reset` JSON stdin/stdout 契约；
- stdout 不混入日志或敏感字段。

### 项目回归

- 运行全部 Go 测试和 race detector；
- 运行现有全部 Python pytest；
- 构建 Docker 镜像；
- 检查 amd64/arm64 manifest；
- 验证镜像仍以 `10001:10001` 运行，并保留安全选项。

### 实机分阶段验收

1. **USB 层**：容器中节点存在、可读、可写；Docker inspect 显示精确设备映射。
2. **只读芯片层**：运行 `mfi-backend probe`，仅接受 `0x11` 的有效版本和合理证书长度。
3. **证书层**：调用 `/mfi/certificate`，只报告长度和 SHA-256，不在聊天或日志暴露证书正文。
4. **签名层**：用受控 challenge 调用 `/mfi/sign`，只报告签名长度和摘要；这一步只证明芯片返回结果。
5. **端到端层**：在 xcertplay 中使用 Remote MFi，最终以 iPhone 返回
   `AA05 AuthenticationSucceeded` 为成功标准。

每层失败都停止，不越级继续写操作。USB/证书/签名阶段都不能替代 AA05 验收。

## 安全与日志

- 不记录 Bearer Token、证书正文、challenge、signature 或私钥。
- 可记录操作名、USB ID、I²C 地址、长度、耗时和结构化错误类别。
- 证书和签名验收只输出长度和 SHA-256 摘要。
- 默认仅映射当前 CH341 节点，不暴露 UPS 和 USB 音频设备。
- 如果设备编号因拔插变化，更新 Compose 后重建容器；稳定 udev 链接作为后续增强，不在本轮实现。

## 发布、部署和回滚

- 版本建议：`1.1.0`，同时更新 `1.1`、`1` 和 `latest`；`1.0.0` 保持不变。
- 发布前必须确认源码、许可证、测试和多架构镜像均可获取。
- FNOS 部署只改镜像标签和 `MFI_BACKEND_COMMAND`，保留当前 Bearer Token、端口和挂载。
- 部署后先运行只读 probe；未通过时立即把镜像标签改回 `1.0.0` 并重建容器。
- 不删除旧镜像、旧 Compose 或用户 `.env`。

## 完成标准

- 代码和许可证随 GitHub 仓库公开；
- Python 与 Go 测试全部通过；
- amd64/arm64 镜像构建并发布；
- FNOS 容器保持非 root、健康且可访问精确 USB 节点；
- 实机 `probe` 成功；
- 实机证书读取成功且 SHA-256 稳定；
- 实机签名返回真实非空结果；
- 最终成功状态只能在 xcertplay 日志出现 `AA05 AuthenticationSucceeded` 后声明。

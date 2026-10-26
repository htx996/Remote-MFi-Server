# 真实硬件后端命令契约

## 边界

FastAPI 服务本身不实现 CH341F USB、I²C 寄存器或 MFi Authentication Coprocessor
协议；这些操作由镜像内置的独立程序 `/usr/local/bin/mfi-backend` 完成。内置程序支持
本项目已经核对的 CH341F `1a86:5512` + MFI337S3959（丝印 3959）USB-A 3.3 V 板，默认
使用 I²C 100 kHz 和地址 `0x11`。它访问真实芯片，不包含软件私钥、固定签名或 challenge
回显。本项目不会在 USB、芯片或后端缺失时生成替代签名。

管理员仍可将 `MFI_BACKEND_COMMAND` 显式改为自备程序。无论使用内置还是替代后端，
FastAPI 都只通过进程边界通信，并继续负责鉴权、requestId 幂等、并发串行和 HTTP 错误。

服务启动子进程时不经过 shell。配置值会按 shell 风格拆分为程序和固定参数，然后把
操作名追加为最后一个参数：

```text
<MFI_BACKEND_COMMAND> probe
<MFI_BACKEND_COMMAND> certificate
<MFI_BACKEND_COMMAND> sign
<MFI_BACKEND_COMMAND> reset
```

请求从 UTF-8 JSON stdin 输入；成功响应必须是 stdout 上唯一的 UTF-8 JSON 对象。程序
以 0 退出表示成功；非 0、超时、非法 JSON、缺失字段均转换为 5xx。不要把二进制数据
直接写到 stdout，全部使用标准 Base64。

服务在调用后端时会不区分大小写地从子进程环境中移除 `MFI_BEARER_TOKEN`。所有芯片
操作在服务内部串行执行。后端在独立进程组中运行；超时、输出超限或请求取消时，服务
会终止整个进程组，避免残留程序继续占用 USB/I2C。

## probe

`probe` 是管理员在容器内使用的只读诊断操作，不由 xcertplay HTTP API 自动调用：

```text
mfi-backend probe
```

stdin：

```json
{}
```

成功 stdout 只包含非敏感元数据：

```json
{
  "usbId": "1a86:5512",
  "i2cAddress": 17,
  "deviceVersion": 3,
  "protocolMajor": 3,
  "certificateLength": 607
}
```

内置后端只打开配置的 USB VID/PID，并只探测明确配置的业务地址；它不会在 `0x11`
失败时静默切换到 `0x10`。该操作不会返回证书正文。
需要排查备选地址时，管理员可以只为一次 `probe` 子进程显式设置
`MFI_I2C_ADDRESS=0x10`；该设置不影响 API 服务或其他业务操作。

## certificate

调用：

```text
mfi-backend certificate
```

stdin：

```json
{}
```

stdout：

```json
{
  "type": "mfi",
  "protocolMajor": 3,
  "certificate": "BASE64_OF_REAL_CERTIFICATE_BYTES"
}
```

`type` 只能是 `mfi` 或 `baa`。`protocolMajor` 必须来自实际硬件/协议。服务会计算并向
xcertplay 返回 `certificateSha256`，后端无须提供该字段。

如果配置 `MFI_CERTIFICATE_FILE` 或 `MFI_CERTIFICATE_BASE64`，服务不会调用
`certificate` 操作，而是使用环境变量中的 `MFI_CERTIFICATE_TYPE` 和
`MFI_PROTOCOL_MAJOR`。

## sign

调用：

```text
mfi-backend sign
```

stdin：

```json
{
  "challenge": "BASE64_OF_1_TO_128_REAL_CHALLENGE_BYTES",
  "requestId": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}
```

stdout：

```json
{
  "signature": "BASE64_OF_REAL_MFI_CHIP_SIGNATURE"
}
```

API 服务会按 `requestId + challenge SHA-256` 缓存成功结果。相同 `requestId` 和相同
challenge 的重试不会再次访问芯片；相同 `requestId` 搭配不同 challenge 返回 409。
后端不得使用软件私钥、固定字节或回显 challenge 来冒充硬件签名。

## reset

调用：

```text
mfi-backend reset
```

stdin：

```json
{}
```

stdout：

```json
{
  "detail": ""
}
```

内置后端的 reset 定义是重新打开 USB 会话，并确认配置的 MFi 地址仍可读取。成功响应为：

```json
{
  "detail": "USB session reopened; physical MFi RST was not pulsed; probed I2C address 0x11"
}
```

它不会脉冲物理 RST：该板依赖 RST 硬件上拉保持地址 `0x11`，主动拉低可能使芯片重新
锁存到 `0x10`。只有 USB 会话重开并探测成功后，API 服务才清空 requestId 缓存；任何
失败都返回 5xx，不能虚报成功。自备后端也必须在 `detail` 中准确说明实际完成的动作。

## 运行要求

- 程序必须在 `MFI_BACKEND_TIMEOUT_SECONDS` 内完成。
- stdout 响应不得超过 256 KiB；证书和签名解码后不得超过 65,525 字节。
- stderr 不得超过 64 KiB。可把简短诊断信息写入 stderr，但不要输出 Token、证书正文、
  challenge、signature 或私钥。
- USB 程序需自行选择/验证正确设备，不得把“容器中存在某个 USB 节点”等同于 MFi
  芯片可用。

## 内置实现、许可证与来源

内置辅助程序源码位于 [`../hardware-backend/`](../hardware-backend/)，按 GNU GPL v3
（GPL-3.0-only）发布。确切上游版本、修改边界和许可证说明见
[`../hardware-backend/NOTICE.md`](../hardware-backend/NOTICE.md) 与
[`../THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md)。发布镜像在
`/usr/share/doc/remote-mfi/hardware-backend` 内携带完整对应源码和许可证。

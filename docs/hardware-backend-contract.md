# 真实硬件后端命令契约

## 边界

FastAPI 服务不实现 CH341F USB、I2C 寄存器或 MFi Authentication Coprocessor 协议。
`MFI_BACKEND_COMMAND` 指向的程序由硬件持有者提供，它才负责真实证书读取、challenge
签名与复位。本项目不会在后端缺失时生成替代签名。

服务启动子进程时不经过 shell。配置值会按 shell 风格拆分为程序和固定参数，然后把
操作名追加为最后一个参数：

```text
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

只有后端成功复位后，API 服务才清空 requestId 缓存。若实际板卡没有可执行的复位路径，
后端应以非 0 退出并让 API 返回失败，而不是报告虚假成功。

## 运行要求

- 程序必须在 `MFI_BACKEND_TIMEOUT_SECONDS` 内完成。
- stdout 响应不得超过 256 KiB；证书和签名解码后不得超过 65,525 字节。
- stderr 不得超过 64 KiB。可把简短诊断信息写入 stderr，但不要输出 Token、证书正文、
  challenge、signature 或私钥。
- USB 程序需自行选择/验证正确设备，不得把“容器中存在某个 USB 节点”等同于 MFi
  芯片可用。

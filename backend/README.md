# 硬件后端放置目录

把你根据实际 CH341F/I2C/MFi 硬件协议实现的可执行程序放在这里，默认文件名为
`mfi-backend`，并在 NAS 上赋予执行权限：

```bash
chmod 0555 backend/mfi-backend
```

完整 stdin/stdout 契约见 `docs/hardware-backend-contract.md`。

本目录没有附带签名程序，也没有测试私钥或伪造签名实现。

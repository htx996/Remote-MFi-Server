# 本地证书目录

若不想让硬件后端处理 certificate 操作，可把真实设备对应的原始证书字节保存为：

```text
secrets/certificate.bin
```

然后在 `.env` 中启用：

```dotenv
MFI_CERTIFICATE_FILE=/run/secrets/remote-mfi/certificate.bin
```

不要把 Bearer Token、私钥或敏感材料提交到版本库。

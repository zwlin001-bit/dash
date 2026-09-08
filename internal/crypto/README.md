# crypto

信封加密核心包 (AES-256-GCM)。

## 职责边界

- 读取主密钥（默认 `/etc/dash/master.key`，权限 0400/0600）。
- 提供标准 AES-256-GCM 加解密接口，返回与解析 `ciphertext`, `nonce`, `key_id`。
- 提供机密掩码函数 `Mask(secret)`（用于 API 响应只展示尾号）。
- 密钥与明文绝不进日志。

## 对外接口

- `NewManager(path string) (*Manager, error)`
- `NewManagerWithKey(key []byte) (*Manager, error)`
- `(m *Manager) Encrypt(plaintext []byte) (ciphertext []byte, nonceHex string, keyID string, err error)`
- `(m *Manager) Decrypt(ciphertext []byte, nonceHex string, keyID string) ([]byte, error)`
- `Mask(s string) string`

## 依赖谁

- 标准库 `crypto/aes`, `crypto/cipher`, `crypto/rand`, `encoding/hex`。
- 零第三方依赖。

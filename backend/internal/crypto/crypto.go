// Package crypto 提供网关落库密钥的强加密能力。
//
// 历史实现仅对明文做 base64 编码后对前 N 字节 XOR（N = len(EncryptionKey)），
// 既未覆盖整段明文，也无完整性校验，相当于明文存储。本包改用 AES-256-GCM
// （带认证标签的对称加密），对任意长度明文做整段加密，并附带前缀 "gcm." 以
// 在解密时区分新旧格式，从而兼容存量数据（双格式解密回退）。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// gcmPrefix 标记采用 AES-GCM 新格式编码的密文。解密时据此区分新旧格式。
const gcmPrefix = "gcm."

// deriveKey 把任意长度的主密钥派生为 AES-256 所需的 32 字节密钥。
// 使用 SHA-256 单向散列，避免主密钥长度不足或过长的问题。
func deriveKey(master string) []byte {
	sum := sha256.Sum256([]byte(master))
	return sum[:]
}

// Encrypt 用 AES-256-GCM 加密 plaintext，返回带 "gcm." 前缀的 base64 字符串。
// 每个密文使用随机 12 字节 nonce，保证相同明文每次加密结果不同。
func Encrypt(plaintext, masterKey string) (string, error) {
	if masterKey == "" {
		return "", errors.New("加密主密钥为空")
	}
	block, err := aes.NewCipher(deriveKey(masterKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// Seal 把密文与认证标签拼接在 nonce 之后：nonce || ciphertext || tag
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	combined := append(nonce, sealed...)
	return gcmPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

// Decrypt 解密 ciphertext：以 "gcm." 前缀的走 AES-GCM，否则回退旧版 base64+XOR。
// 解密失败时返回原串（兼容旧行为：base64 解码失败即原样返回密文）。
func Decrypt(ciphertext, masterKey string) (string, error) {
	if masterKey == "" {
		return "", errors.New("解密主密钥为空")
	}
	if strings.HasPrefix(ciphertext, gcmPrefix) {
		return decryptGCM(ciphertext[len(gcmPrefix):], masterKey)
	}
	return decryptLegacy(ciphertext, masterKey)
}

// decryptGCM 解密 AES-GCM 格式（已去除前缀的 base64）。
func decryptGCM(b64, masterKey string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(deriveKey(masterKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns {
		return "", errors.New("密文过短")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

// decryptLegacy 兼容旧版：base64 解码后对前 N 字节做 XOR 还原明文。
// base64 解码失败时原样返回密文（与旧实现行为一致）。
func decryptLegacy(encrypted, masterKey string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return encrypted, nil
	}
	key := []byte(masterKey)
	for i := 0; i < len(data) && i < len(key); i++ {
		data[i] ^= key[i]
	}
	return string(data), nil
}

// IsGCMFormat 判断密文是否已采用新格式（用于迁移跳过已升级行）。
func IsGCMFormat(ciphertext string) bool {
	return strings.HasPrefix(ciphertext, gcmPrefix)
}

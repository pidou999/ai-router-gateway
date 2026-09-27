package crypto

import (
	"encoding/base64"
	"strings"
	"testing"
)

// legacyEncrypt 复刻旧版「base64 + 前 N 字节 XOR」编码，用于构造存量数据 fixture。
func legacyEncrypt(plain, key string) string {
	data := []byte(plain)
	k := []byte(key)
	for i := 0; i < len(data) && i < len(k); i++ {
		data[i] ^= k[i]
	}
	return base64.StdEncoding.EncodeToString(data)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := "change-me-in-production"
	plaintexts := []string{
		"sk-1234567890abcdef",
		"short",
		"",
		"unicode密钥🔑longerthan32bytesxxxxxxxxxxxxxx",
	}
	for _, p := range plaintexts {
		ct, err := Encrypt(p, key)
		if err != nil {
			t.Fatalf("Encrypt(%q) error: %v", p, err)
		}
		if !strings.HasPrefix(ct, gcmPrefix) {
			t.Fatalf("密文缺少 gcm. 前缀: %s", ct)
		}
		pt, err := Decrypt(ct, key)
		if err != nil {
			t.Fatalf("Decrypt error: %v", err)
		}
		if pt != p {
			t.Fatalf("往返不一致: got %q want %q", pt, p)
		}
	}
}

func TestDecryptLegacy(t *testing.T) {
	key := "change-me-in-production"
	plain := "sk-secret-value-123"
	legacy := legacyEncrypt(plain, key)
	if strings.HasPrefix(legacy, gcmPrefix) {
		t.Fatalf("测试 fixture 不应带 gcm. 前缀")
	}
	pt, err := Decrypt(legacy, key)
	if err != nil {
		t.Fatalf("解密旧格式失败: %v", err)
	}
	if pt != plain {
		t.Fatalf("旧格式解密错误: got %q want %q", pt, plain)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	ct, _ := Encrypt("hello", "key-a")
	if _, err := Decrypt(ct, "key-b"); err == nil {
		t.Fatalf("错误密钥应解密失败")
	}
}

func TestIsGCMFormat(t *testing.T) {
	if !IsGCMFormat("gcm.abc") {
		t.Fatalf("应识别 gcm. 前缀")
	}
	if IsGCMFormat("anythingelse") {
		t.Fatalf("非 gcm. 前缀应返回 false")
	}
}

func TestEmptyMasterKey(t *testing.T) {
	if _, err := Encrypt("x", ""); err == nil {
		t.Fatalf("空主密钥 Encrypt 应报错")
	}
	if _, err := Decrypt("x", ""); err == nil {
		t.Fatalf("空主密钥 Decrypt 应报错")
	}
}

func TestEncryptDeterministicNonce(t *testing.T) {
	// 相同明文两次加密结果应不同（随机 nonce）。
	a, _ := Encrypt("repeat", "k")
	b, _ := Encrypt("repeat", "k")
	if a == b {
		t.Fatalf("相同明文加密结果不应相同（nonce 应随机）")
	}
}

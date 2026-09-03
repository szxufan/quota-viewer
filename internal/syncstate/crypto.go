package syncstate

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// 密钥派生与加密格式(见 .trae/documents/oss-state-sync.md):
// 密钥 = SHA-256(密码 UTF-8 字节) → AES-256-GCM;
// 密文 = base64( nonce ‖ ciphertext ),nonce 为 12 字节随机数。

// ErrEmptyPassword 密码为空时返回。
var ErrEmptyPassword = errors.New("加密密码不能为空")

// DeriveKey 由密码派生 AES-256 密钥(SHA-256 单次散列)。
func DeriveKey(password string) []byte {
	sum := sha256.Sum256([]byte(password))
	return sum[:]
}

// Encrypt 序列化载荷并用 AES-256-GCM 加密,输出 base64( nonce ‖ ciphertext )。
func Encrypt(p *Payload, password string) ([]byte, error) {
	if password == "" {
		return nil, ErrEmptyPassword
	}
	plain, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("序列化状态失败: %w", err)
	}
	gcm, err := newGCM(password)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("生成随机数失败: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, plain, nil)
	out := make([]byte, base64.StdEncoding.EncodedLen(len(sealed)))
	base64.StdEncoding.Encode(out, sealed)
	return out, nil
}

// Decrypt 解密 base64( nonce ‖ ciphertext ) 数据并反序列化为载荷。
// base64 非法、长度不足或密码错误(GCM Tag 校验失败)均返回中文错误。
func Decrypt(data []byte, password string) (*Payload, error) {
	if password == "" {
		return nil, ErrEmptyPassword
	}
	raw := make([]byte, base64.StdEncoding.DecodedLen(len(data)))
	n, err := base64.StdEncoding.Decode(raw, data)
	if err != nil {
		return nil, fmt.Errorf("状态文件格式错误(base64 解码失败): %w", err)
	}
	raw = raw[:n]
	gcm, err := newGCM(password)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errors.New("状态文件格式错误(长度过短)")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return nil, errors.New("解密失败: 密码错误或文件被篡改")
	}
	var p Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		return nil, fmt.Errorf("状态文件格式错误(JSON 解析失败): %w", err)
	}
	if p.Version != payloadVersion {
		return nil, fmt.Errorf("不支持的状态文件版本: %d", p.Version)
	}
	return &p, nil
}

// newGCM 用派生密钥构建 AES-256-GCM。
func newGCM(password string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(DeriveKey(password))
	if err != nil {
		return nil, fmt.Errorf("初始化加密器失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("初始化加密器失败: %w", err)
	}
	return gcm, nil
}

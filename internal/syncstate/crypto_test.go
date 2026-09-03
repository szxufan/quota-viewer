package syncstate

import (
	"bytes"
	"testing"
	"time"

	"quota-viewer/internal/fetcher"
)

// TestDeriveKeyDeterministic 同一密码派生密钥必须确定且为 32 字节。
func TestDeriveKeyDeterministic(t *testing.T) {
	k1 := DeriveKey("secret")
	k2 := DeriveKey("secret")
	if len(k1) != 32 {
		t.Fatalf("密钥长度 = %d, 应为 32", len(k1))
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("同一密码派生密钥不一致")
	}
	if bytes.Equal(k1, DeriveKey("other")) {
		t.Fatal("不同密码派生出相同密钥")
	}
}

// TestEncryptDecryptRoundTrip 加密后能用同密码解密还原载荷。
func TestEncryptDecryptRoundTrip(t *testing.T) {
	next := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	in := NewPayload([]fetcher.QuotaResult{
		{ID: "kimi", Platform: "Kimi", KeyIndex: 0, Percent: 42.5},
		{ID: "aliyun", Platform: "阿里云", KeyIndex: 1, Balance: 88.0, Kind: fetcher.KindBalance},
	}, next)

	data, err := Encrypt(in, "密码123")
	if err != nil {
		t.Fatalf("Encrypt 出错: %v", err)
	}
	out, err := Decrypt(data, "密码123")
	if err != nil {
		t.Fatalf("Decrypt 出错: %v", err)
	}
	if out.Version != 1 {
		t.Errorf("Version = %d, 应为 1", out.Version)
	}
	if !out.NextRefreshAt.Equal(next) {
		t.Errorf("NextRefreshAt = %v, 应为 %v", out.NextRefreshAt, next)
	}
	if len(out.Results) != 2 || out.Results[0].ID != "kimi" || out.Results[1].KeyIndex != 1 {
		t.Errorf("Results 往返不一致: %+v", out.Results)
	}
}

// TestEncryptRandomNonce 两次加密同一份载荷输出必须不同(随机 nonce)。
func TestEncryptRandomNonce(t *testing.T) {
	p := NewPayload(nil, time.Now())
	a, err := Encrypt(p, "pw")
	if err != nil {
		t.Fatalf("Encrypt 出错: %v", err)
	}
	b, err := Encrypt(p, "pw")
	if err != nil {
		t.Fatalf("Encrypt 出错: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("两次加密输出相同(nonce 未随机化)")
	}
}

// TestDecryptWrongPassword 错误密码解密必须失败。
func TestDecryptWrongPassword(t *testing.T) {
	data, err := Encrypt(NewPayload(nil, time.Now()), "right")
	if err != nil {
		t.Fatalf("Encrypt 出错: %v", err)
	}
	if _, err := Decrypt(data, "wrong"); err == nil {
		t.Fatal("错误密码解密未报错")
	}
}

// TestDecryptTampered 篡改密文解密必须失败。
func TestDecryptTampered(t *testing.T) {
	data, err := Encrypt(NewPayload(nil, time.Now()), "pw")
	if err != nil {
		t.Fatalf("Encrypt 出错: %v", err)
	}
	data[len(data)-5] ^= 0x01
	if _, err := Decrypt(data, "pw"); err == nil {
		t.Fatal("篡改密文解密未报错")
	}
}

// TestDecryptInvalidInput 非 base64 / 过短输入必须报错而非 panic。
func TestDecryptInvalidInput(t *testing.T) {
	if _, err := Decrypt([]byte("!!!not-base64!!!"), "pw"); err == nil {
		t.Fatal("非法 base64 未报错")
	}
	if _, err := Decrypt([]byte("aGVsbG8="), "pw"); err == nil { // "hello" 过短
		t.Fatal("过短密文未报错")
	}
}

// TestEmptyPassword 空密码加密/解密均报错。
func TestEmptyPassword(t *testing.T) {
	if _, err := Encrypt(NewPayload(nil, time.Now()), ""); err != ErrEmptyPassword {
		t.Fatalf("Encrypt 空密码错误 = %v, 应为 ErrEmptyPassword", err)
	}
	if _, err := Decrypt([]byte("aGVsbG8="), ""); err != ErrEmptyPassword {
		t.Fatalf("Decrypt 空密码错误 = %v, 应为 ErrEmptyPassword", err)
	}
}

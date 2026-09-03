package syncstate

import (
	"bytes"
	"context"
	"fmt"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
)

// UploadOSS 将加密后的状态数据上传到 OSS 指定对象(覆盖写)。
// bucket 需允许该 AK 写入;订阅端走公网匿名读,因此 bucket 应为公共读。
func UploadOSS(_ context.Context, endpoint, bucketName, objectKey, akID, akSecret string, data []byte) error {
	if endpoint == "" || bucketName == "" || objectKey == "" || akID == "" || akSecret == "" {
		return fmt.Errorf("OSS 配置不完整(Endpoint/Bucket/对象路径/AccessKey 均为必填)")
	}
	client, err := oss.New(endpoint, akID, akSecret)
	if err != nil {
		return fmt.Errorf("创建 OSS 客户端失败: %w", err)
	}
	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return fmt.Errorf("访问 Bucket %s 失败: %w", bucketName, err)
	}
	if err := bucket.PutObject(objectKey, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("上传状态到 OSS 失败: %w", err)
	}
	return nil
}

// DeleteOSSObject 删除 OSS 对象(TestSync 清理测试对象用)。
func DeleteOSSObject(endpoint, bucketName, objectKey, akID, akSecret string) error {
	client, err := oss.New(endpoint, akID, akSecret)
	if err != nil {
		return fmt.Errorf("创建 OSS 客户端失败: %w", err)
	}
	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return fmt.Errorf("访问 Bucket %s 失败: %w", bucketName, err)
	}
	if err := bucket.DeleteObject(objectKey); err != nil {
		return fmt.Errorf("删除 OSS 对象失败: %w", err)
	}
	return nil
}

// Package blob uploads the original .docx to S3-compatible storage (RustFS /
// MinIO / AWS) using aws-sdk-go-v2, mirroring s3_client.upload_docx.
package blob

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

type Uploader struct {
	client *s3.Client
	bucket string
}

// NewUploader builds an S3 client. s3URL may omit the scheme (e.g.
// "localhost:9000"); http:// is assumed. Path-style addressing is used so the
// same client works against RustFS/MinIO and AWS.
func NewUploader(s3URL, bucket, accessKey, secretKey string) *Uploader {
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(endpoint(s3URL)),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		UsePathStyle: true,
	})
	return &Uploader{client: client, bucket: bucket}
}

// UploadDocx stores the bytes under project/doc_type/<uuid>.docx and returns the
// object key.
func (u *Uploader) UploadDocx(ctx context.Context, docxBytes []byte, project, docType string) (string, error) {
	key := fmt.Sprintf("%s/%s/%s.docx", project, docType, uuid.NewString())
	_, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(u.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(docxBytes),
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

func endpoint(s3URL string) string {
	if !strings.HasPrefix(s3URL, "http://") && !strings.HasPrefix(s3URL, "https://") {
		return "http://" + s3URL
	}
	return s3URL
}

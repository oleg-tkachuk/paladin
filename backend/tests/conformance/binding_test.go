//go:build conformance

package conformance

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
)

// digest is body's checksum under algo, base64 as S3 writes it.
func digest(algo string, body []byte) string {
	switch algo {
	case checksum.CRC32C:
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli)))
		return base64.StdEncoding.EncodeToString(b[:])
	case checksum.MD5:
		s := md5.Sum(body)
		return base64.StdEncoding.EncodeToString(s[:])
	}
	s := sha256.Sum256(body)
	return base64.StdEncoding.EncodeToString(s[:])
}

// send issues one request carrying every header the presigner returned.
func send(ctx context.Context, method, url string, headers map[string]string, body []byte) (int, string, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, trim(string(b)), res.Header, nil
}

// putSigned presigns a PUT for signed and sends sent through it — the same
// bytes for an upload, different ones to see whether the binding holds.
func putSigned(ctx context.Context, tg *target, key, algo string, signed, sent []byte) (int, string, error) {
	url, headers, _, err := tg.client.PresignPut(ctx, objecth.PresignPutArgs{
		Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection, Key: key,
		ContentType: "text/plain", SizeBytes: int64(len(signed)),
		ChecksumAlgo: algo, ChecksumValue: digest(algo, signed), TTL: ttl(),
	})
	if err != nil {
		return 0, "", err
	}
	status, body, _, err := send(ctx, http.MethodPut, url, headers, sent)
	return status, body, err
}

// postSigned presigns a POST for signed and submits sent as the form's file,
// with every field the policy requires — or, with extra, more.
func postSigned(ctx context.Context, tg *target, key string, signed, sent []byte, extra map[string]string) (int, string, error) {
	action, fields, _, err := tg.client.PresignPost(ctx, objecth.PresignPostArgs{
		Bucket: tg.bucket, TenantID: tg.tenant, Collection: tg.collection, Key: key,
		ContentType: "text/plain", SizeBytes: int64(len(signed)),
		ChecksumAlgo: checksum.SHA256, ChecksumValue: digest(checksum.SHA256, signed), TTL: ttl(),
	})
	if err != nil {
		return 0, "", err
	}
	for k, v := range extra {
		fields[k] = v
	}
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return 0, "", err
		}
	}
	fw, err := w.CreateFormFile("file", "upload")
	if err != nil {
		return 0, "", err
	}
	if _, err := fw.Write(sent); err != nil {
		return 0, "", err
	}
	if err := w.Close(); err != nil {
		return 0, "", err
	}
	status, body, _, err := send(ctx, http.MethodPost, action, map[string]string{"Content-Type": w.FormDataContentType()}, form.Bytes())
	return status, body, err
}

func ok(status int) bool { return status/100 == 2 }

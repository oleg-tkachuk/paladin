package paladintest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"testing"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// The control half of a multipart upload whose bytes a browser sends: the
// server opens it, signs each part for the checksum the browser computed,
// and completes it with the ETags the browser read — quoted, as a header
// carries them.
func TestMultipartControlDrivesABrowserUpload(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	body := bytes.Repeat([]byte("b"), 2*paladintest.PartSize+3)

	session, err := paladin.BeginMultipart(ctx, p.Data, paladin.MultipartInput{
		Parent: srv.Collection().String(), Key: "upload.bin",
		ContentType: "application/octet-stream", Size: int64(len(body)),
	})
	if err != nil {
		t.Fatalf("BeginMultipart: %v", err)
	}
	if session.PartSize != paladintest.PartSize || session.TotalParts != 3 {
		t.Fatalf("session split = %d × %d, want 3 parts of %d", session.TotalParts, session.PartSize, paladintest.PartSize)
	}

	// What the browser does: slice at the server's part size, hash, presign,
	// PUT, and keep the ETag it reads from the response header.
	var parts []*datav1.CompletedPart
	for i := range session.TotalParts {
		start := int64(i) * session.PartSize
		chunk := body[start:min(start+session.PartSize, int64(len(body)))]
		sum := sha256.Sum256(chunk)
		checksum := base64.StdEncoding.EncodeToString(sum[:])
		signed, err := paladin.PresignPart(ctx, p.Data, session, i+1, checksum)
		if err != nil {
			t.Fatalf("PresignPart %d: %v", i+1, err)
		}
		// A part URL that expired is signed again for the same part.
		if _, err := paladin.PresignPart(ctx, p.Data, session, i+1, checksum); err != nil {
			t.Fatalf("PresignPart %d again: %v", i+1, err)
		}
		etag, err := p.Data.Transfer().Put(ctx, signed, bytes.NewReader(chunk), int64(len(chunk)))
		if err != nil {
			t.Fatalf("PUT part %d: %v", i+1, err)
		}
		parts = append(parts, &datav1.CompletedPart{PartNumber: i + 1, Etag: `"` + etag + `"`, ChecksumValue: checksum})
	}

	obj, err := paladin.CompleteMultipart(ctx, p.Data, session, parts)
	if err != nil {
		t.Fatalf("CompleteMultipart: %v", err)
	}
	if obj.GetName() != session.ObjectName || obj.GetSizeBytes() != int64(len(body)) {
		t.Errorf("completed %s of %d bytes, want %s of %d", obj.GetName(), obj.GetSizeBytes(), session.ObjectName, len(body))
	}
	r, err := paladin.Download(ctx, p.Data, obj.GetName(), paladin.DownloadOptions{})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = r.Close() }()
	if got, _ := io.ReadAll(r); !bytes.Equal(got, body) {
		t.Error("the assembled object differs from the bytes sent")
	}
}

// Aborting drops the upload; aborting one already gone is not an error.
func TestAbortMultipartIsSafeToRepeat(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	ctx := context.Background()
	session, err := paladin.BeginMultipart(ctx, p.Data, paladin.MultipartInput{
		Parent: srv.Collection().String(), Key: "dropped.bin",
		ContentType: "application/octet-stream", Size: 2 * paladintest.PartSize,
	})
	if err != nil {
		t.Fatalf("BeginMultipart: %v", err)
	}
	for range 2 {
		if err := paladin.AbortMultipart(ctx, p.Data, session); err != nil {
			t.Fatalf("AbortMultipart: %v", err)
		}
	}
	if _, err := paladin.PresignPart(ctx, p.Data, session, 1, "x"); err == nil {
		t.Error("a part of an aborted upload was presigned")
	}
}

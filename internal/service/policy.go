package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"paladin/internal/config"
)

type Policy struct {
	MaxObjectSizeBytes  int64
	AllowedContentTypes map[string]struct{}
	PresignPutTTL       time.Duration
	PresignGetTTL       time.Duration
	PresignPartTTL      time.Duration
}

func NewPolicy(cfg config.Policy) Policy {
	m := make(map[string]struct{}, len(cfg.AllowedContentTypes))
	for _, ct := range cfg.AllowedContentTypes {
		m[strings.ToLower(strings.TrimSpace(ct))] = struct{}{}
	}

	return Policy{
		MaxObjectSizeBytes:  cfg.MaxObjectSizeBytes,
		AllowedContentTypes: m,
		PresignPutTTL:       cfg.PresignPutTTL,
		PresignGetTTL:       cfg.PresignGetTTL,
		PresignPartTTL:      cfg.PresignPartTTL,
	}
}

func (p Policy) Validate(contentType string, sizeBytes int64) error {
	if sizeBytes <= 0 {
		return errors.New("size_bytes must be > 0")
	}

	if sizeBytes > p.MaxObjectSizeBytes {
		return fmt.Errorf("payload too large: %d > %d", sizeBytes, p.MaxObjectSizeBytes)
	}

	ct := strings.ToLower(strings.TrimSpace(contentType))
	if _, ok := p.AllowedContentTypes[ct]; !ok {
		return fmt.Errorf("content_type not allowed: %s", contentType)
	}

	return nil
}

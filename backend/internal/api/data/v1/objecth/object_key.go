package objecth

import (
	"fmt"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// The request fields an object key arrives in, as a refusal names them.
const (
	FieldKey                  = "key"
	FieldDestinationKey       = "destination_key"
	FieldDestinationKeyPrefix = "destination_key_prefix"
)

// AdmitKey refuses a key a caller named in field that is not canonical
// (storagepath.ValidateKey), as InvalidArgument: the rule, then why. Every
// path that creates an object checks the key it will store here, where it
// enters, so the refusal is the request's and is logged as one.
func AdmitKey(field, key string) error {
	if err := storagepath.ValidateKey(key); err != nil {
		return rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", field, err))
	}
	return nil
}

// AdmitKeyPrefix is AdmitKey for a prefix every key of a batch is written
// under (storagepath.ValidateKeyPrefix). The empty prefix is admitted.
func AdmitKeyPrefix(field, prefix string) error {
	if err := storagepath.ValidateKeyPrefix(prefix); err != nil {
		return rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("%s: %w", field, err))
	}
	return nil
}

// AdmitOptionalKey is AdmitKey for an optional key: the empty key is the
// caller naming none, which the server fills in.
func AdmitOptionalKey(field, key string) error {
	if key == "" {
		return nil
	}
	return AdmitKey(field, key)
}

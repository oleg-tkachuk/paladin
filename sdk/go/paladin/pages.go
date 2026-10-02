package paladin

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Paging fields: every List request carries a paladin.common.v1.PageRequest
// in `page`, and every response a PageResponse in `page`.
const (
	pageField          = "page"
	pageTokenField     = "page_token"
	nextPageTokenField = "next_page_token"
)

// ErrNotPaged is yielded by Pages for a request or response without the
// paging fields.
var ErrNotPaged = errors.New("paladin: message has no page field")

// Pages calls a List RPC page by page and yields every item, following
// next_page_token until it is empty. items picks the items out of a response.
// The first error is yielded and ends the sequence; stopping the loop early
// makes no further calls.
//
//	for obj, err := range paladin.Pages(ctx, p.Data.Object.ListObjects,
//		&datav1.ListObjectsRequest{Parent: collection},
//		(*datav1.ListObjectsResponse).GetObjects) { … }
func Pages[Req, Res any, PReq interface {
	*Req
	proto.Message
}, PRes interface {
	*Res
	proto.Message
}, Item any](
	ctx context.Context,
	call func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error),
	req PReq,
	items func(PRes) []Item,
) iter.Seq2[Item, error] {
	return func(yield func(Item, error) bool) {
		var zero Item
		next := proto.Clone(req).(PReq)
		for {
			resp, err := call(ctx, connect.NewRequest((*Req)(next)))
			if err != nil {
				yield(zero, err)
				return
			}
			msg := PRes(resp.Msg)
			for _, item := range items(msg) {
				if !yield(item, nil) {
					return
				}
			}
			token, err := nextPageToken(msg)
			if err != nil {
				yield(zero, err)
				return
			}
			if token == "" {
				return
			}
			next = proto.Clone(next).(PReq)
			if err := setPageToken(next, token); err != nil {
				yield(zero, err)
				return
			}
		}
	}
}

func pageOf(m proto.Message) (protoreflect.FieldDescriptor, error) {
	fd := m.ProtoReflect().Descriptor().Fields().ByName(pageField)
	if fd == nil || fd.Message() == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotPaged, m.ProtoReflect().Descriptor().FullName())
	}
	return fd, nil
}

func nextPageToken(m proto.Message) (string, error) {
	fd, err := pageOf(m)
	if err != nil {
		return "", err
	}
	page := m.ProtoReflect().Get(fd).Message()
	tfd := page.Descriptor().Fields().ByName(nextPageTokenField)
	if tfd == nil {
		return "", fmt.Errorf("%w: %s", ErrNotPaged, page.Descriptor().FullName())
	}
	return page.Get(tfd).String(), nil
}

func setPageToken(m proto.Message, token string) error {
	fd, err := pageOf(m)
	if err != nil {
		return err
	}
	page := m.ProtoReflect().Mutable(fd).Message()
	tfd := page.Descriptor().Fields().ByName(pageTokenField)
	if tfd == nil {
		return fmt.Errorf("%w: %s", ErrNotPaged, page.Descriptor().FullName())
	}
	page.Set(tfd, protoreflect.ValueOfString(token))
	return nil
}

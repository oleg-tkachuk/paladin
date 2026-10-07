package paladintest

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Public collections, as the server keeps them: anyone reads an object at its
// public_url, unsigned; the server names every object; nothing goes to the
// trash.
const (
	// DefaultPublicCollection is the collection PublicCollection names when
	// given none.
	DefaultPublicCollection = "public"
	// PublicCacheControl is what the fake stores every public object with:
	// the server's default for a public collection.
	PublicCacheControl = "public, max-age=31536000, immutable"
	// publicPath is where the fake serves public objects, as a store under a
	// public bucket policy does.
	publicPath = "/public/"
	// publicKeyBytes is the randomness in a public object's key, as the
	// server draws it.
	publicKeyBytes     = 16
	headerCacheControl = "Cache-Control"
)

var publicKeyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// PublicCollection is the name of a public collection in the fake's tenant,
// DefaultPublicCollection unless another is given. Objects uploaded into it
// are named by the fake and served unsigned at their public_url.
func (s *Server) PublicCollection(collection ...string) paladin.CollectionName {
	name := s.Collection(DefaultPublicCollection)
	if len(collection) > 0 {
		name = s.Collection(collection[0])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.public[name.String()] = true
	return name
}

// errPublicRule is the server's answer to a request a public collection
// refuses.
func errPublicRule(what string) error {
	err := connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("public collection: %s", what))
	detail, derr := connect.NewErrorDetail(&errdetails.ErrorInfo{
		Reason: commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE.String(), Domain: paladin.ErrorDomain,
	})
	if derr != nil {
		panic(fmt.Sprintf("paladintest: %v", derr)) // an ErrorInfo always marshals
	}
	err.AddDetail(detail)
	return err
}

// publicKey names an object in a public collection, or refuses a key the
// client chose, as the server does. Outside one it returns key unchanged.
// The caller holds mu.
func (s *Server) publicKey(parent, key string) (string, error) {
	if !s.public[parent] {
		return key, nil
	}
	if key != "" {
		return "", errPublicRule("a public collection names its objects itself; leave the key empty")
	}
	b := make([]byte, publicKeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", connect.NewError(connect.CodeInternal, err)
	}
	return strings.ToLower(publicKeyEncoding.EncodeToString(b)), nil
}

// publish gives a new object in a public collection its URL. The caller holds
// mu.
func (s *Server) publish(o *object, parent string) {
	if !s.public[parent] {
		return
	}
	c, err := paladin.ParseCollectionName(parent)
	if err != nil {
		return
	}
	o.msg.PublicUrl = s.URL + publicPath + s.tenant + "/" + c.Collection + "/" + o.msg.GetKey()
	o.cacheControl = PublicCacheControl
}

// refuseTrash: a public object is deleted permanently or not at all.
func refuseTrash(o *object) error {
	if o.msg.GetPublicUrl() != "" {
		return errPublicRule("a public object is deleted with permanent=true; it has no trash")
	}
	return nil
}

// servePublic answers an unsigned GET of a public object as the store does:
// the bytes with the collection's Cache-Control, 404 once it is gone.
func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "anonymous access is read-only", http.StatusForbidden)
		return
	}
	url := s.URL + r.URL.Path
	s.mu.Lock()
	var found *object
	for _, o := range s.objects {
		if o.msg.GetPublicUrl() == url && o.msg.GetState() == datav1.ObjectState_OBJECT_STATE_AVAILABLE {
			found = o
			break
		}
	}
	var body []byte
	var contentType string
	if found != nil {
		body, contentType = found.body, found.msg.GetContentType()
	}
	s.mu.Unlock()
	if found == nil {
		http.Error(w, errors.New("no such key").Error(), http.StatusNotFound)
		return
	}
	w.Header().Set(headerContentType, contentType)
	w.Header().Set(headerCacheControl, PublicCacheControl)
	_, _ = w.Write(body)
}

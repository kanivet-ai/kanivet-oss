package watcher

import (
	"bufio"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utiljson "k8s.io/apimachinery/pkg/util/json"
)

// A list response is decoded as it arrives: each item is handed over as soon
// as its bytes are in, so the first rows reach the UI while the rest is still
// downloading and no more than one encoded item is held at a time. Decoding
// the whole body at once made a list of 2,400 pods (124 MB of protobuf) peak
// near 500 MB of heap and show nothing until its last byte.

// errListFormat marks a response the streaming decoders cannot read: not what
// the request asked for, or not laid out the way they expect. The transport
// worked, so the caller can list again the ordinary way.
var errListFormat = errors.New("unexpected list encoding")

func formatErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errListFormat, fmt.Sprintf(format, args...))
}

// maxListItemBytes bounds one item's encoded size. etcd caps an object at a
// few MB, so a larger length is a misread stream, not an item to allocate for.
const maxListItemBytes = 64 << 20

// maxListBytes bounds a whole list the same way.
const maxListBytes = 1 << 40

// protoMagic opens every Kubernetes protobuf response.
const protoMagic = "k8s\x00"

// protoItem is a generated API type, which decodes itself from protobuf.
type protoItem interface {
	runtime.Object
	Unmarshal([]byte) error
}

// decodeProtoList reads a protobuf list response, calling each for every item
// in order, and returns the list's resourceVersion.
//
// The wire layout is the magic number, then a runtime.Unknown whose raw field
// (2) is the list message; every API list type has its ListMeta as field 1
// and its repeated items as field 2.
func decodeProtoList(r io.Reader, newItem func() protoItem, each func(runtime.Object) error) (string, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	var magic [len(protoMagic)]byte
	if _, err := io.ReadFull(br, magic[:]); err != nil {
		return "", readErr(err)
	}
	if string(magic[:]) != protoMagic {
		return "", formatErr("no protobuf magic number")
	}
	var rv string
	var buf []byte
	for {
		field, size, _, err := protoField(br)
		if err == io.EOF {
			return rv, nil
		}
		if err != nil {
			return rv, err
		}
		if field != 2 {
			if _, err := br.Discard(size); err != nil {
				return rv, readErr(err)
			}
			continue
		}
		for left := size; left > 0; {
			field, size, header, err := protoField(br)
			if err != nil {
				return rv, readErr(err)
			}
			if left -= header + size; left < 0 {
				return rv, formatErr("list item overruns the list")
			}
			if field != 1 && field != 2 {
				if _, err := br.Discard(size); err != nil {
					return rv, readErr(err)
				}
				continue
			}
			if size > maxListItemBytes {
				return rv, formatErr("list item of %d bytes", size)
			}
			if cap(buf) < size {
				buf = make([]byte, size)
			}
			buf = buf[:size]
			if _, err := io.ReadFull(br, buf); err != nil {
				return rv, readErr(err)
			}
			if field == 1 {
				var meta metav1.ListMeta
				if err := meta.Unmarshal(buf); err != nil {
					return rv, formatErr("list metadata: %v", err)
				}
				rv = meta.ResourceVersion
				continue
			}
			// Unmarshal copies what it keeps, so buf is reused.
			item := newItem()
			if err := item.Unmarshal(buf); err != nil {
				return rv, formatErr("list item: %v", err)
			}
			if err := each(item); err != nil {
				return rv, err
			}
		}
	}
}

// protoField reads the tag and length of a length-delimited field, the only
// kind these messages have, and reports how many bytes that header took.
func protoField(br *bufio.Reader) (field, size, header int, err error) {
	tag, n1, err := protoUvarint(br)
	if err != nil {
		return 0, 0, 0, err
	}
	if tag&7 != 2 {
		return 0, 0, 0, formatErr("wire type %d", tag&7)
	}
	length, n2, err := protoUvarint(br)
	if err != nil {
		return 0, 0, 0, readErr(err)
	}
	if length > maxListBytes {
		return 0, 0, 0, formatErr("field of %d bytes", length)
	}
	return int(tag >> 3), int(length), n1 + n2, nil
}

func protoUvarint(br *bufio.Reader) (v uint64, n int, err error) {
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := br.ReadByte()
		if err != nil {
			if n > 0 {
				err = readErr(err)
			}
			return 0, n, err
		}
		n++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, n, nil
		}
	}
	return 0, n, formatErr("varint too long")
}

// readErr reports a stream that ended inside a value as a short read rather
// than a clean end.
func readErr(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// decodeJSONList reads a JSON list response, calling each for every item in
// order, and returns the list's resourceVersion. Items come out exactly as the
// dynamic client decodes them: integers stay integers, and an item without
// kind and apiVersion (built-in kinds omit them inside a list) gets the
// list's.
func decodeJSONList(r io.Reader, each func(runtime.Object) error) (string, error) {
	dec := jsontext.NewDecoder(r)
	if tok, err := dec.ReadToken(); err != nil {
		return "", jsonErr(err)
	} else if tok.Kind() != '{' {
		return "", formatErr("JSON list is not an object")
	}
	var rv, listKind, listAPIVersion string
	// The consumer's own error is returned as it is, not as a decoding one.
	var eachErr error
	consume := func(obj runtime.Object) error {
		eachErr = each(obj)
		return eachErr
	}
	for dec.PeekKind() == '"' {
		name, err := dec.ReadToken()
		if err != nil {
			return rv, jsonErr(err)
		}
		switch name.String() {
		case "kind":
			err = jsonv2.UnmarshalDecode(dec, &listKind)
		case "apiVersion":
			err = jsonv2.UnmarshalDecode(dec, &listAPIVersion)
		case "metadata":
			var meta struct {
				ResourceVersion string `json:"resourceVersion"`
			}
			err = jsonv2.UnmarshalDecode(dec, &meta)
			rv = meta.ResourceVersion
		case "items":
			err = decodeJSONItems(dec, listKind, listAPIVersion, consume)
		default:
			err = dec.SkipValue()
		}
		if eachErr != nil {
			return rv, eachErr
		}
		if err != nil {
			return rv, jsonErr(err)
		}
	}
	if _, err := dec.ReadToken(); err != nil {
		return rv, jsonErr(err)
	}
	return rv, nil
}

func decodeJSONItems(dec *jsontext.Decoder, listKind, listAPIVersion string, each func(runtime.Object) error) error {
	switch dec.PeekKind() {
	case 'n':
		_, err := dec.ReadToken()
		return err
	case '[':
	default:
		return formatErr("JSON list items is not an array")
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	for dec.PeekKind() == '{' {
		raw, err := dec.ReadValue()
		if err != nil {
			return err
		}
		obj := make(map[string]interface{})
		if err := utiljson.Unmarshal(raw, &obj); err != nil {
			return formatErr("list item: %v", err)
		}
		u := &unstructured.Unstructured{Object: obj}
		if u.GetKind() == "" && u.GetAPIVersion() == "" {
			// The list's own kind comes before its items whenever items
			// leave theirs out. If it has not, the item cannot be completed
			// the way a whole-body decode would.
			if listKind == "" {
				return formatErr("list item without a kind before the list's")
			}
			u.SetKind(strings.TrimSuffix(listKind, "List"))
			u.SetAPIVersion(listAPIVersion)
		}
		if err := each(u); err != nil {
			return err
		}
	}
	tok, err := dec.ReadToken()
	if err == nil && tok.Kind() != ']' {
		return formatErr("JSON list item is not an object")
	}
	return err
}

// jsonErr classifies a JSON decoding failure: a body that is not valid JSON is
// a format error, anything else (the connection, a cancelled context) is the
// transport's.
func jsonErr(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	if errors.Is(err, errListFormat) {
		return err
	}
	var syntactic *jsontext.SyntacticError
	var semantic *jsonv2.SemanticError
	if errors.As(err, &syntactic) || errors.As(err, &semantic) {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return io.ErrUnexpectedEOF
		}
		return formatErr("%v", err)
	}
	return err
}

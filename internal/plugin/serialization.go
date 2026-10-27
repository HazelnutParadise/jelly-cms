package plugin

import (
	"encoding/binary"
	"errors"

	"github.com/fastschema/qjs"
)

// stringifyJSON uses the JSON.stringify function captured before plugin code
// runs, then copies its UTF-8 output into Go memory before releasing QuickJS's
// CString. QJS_JSONStringify in the pinned qjs runtime frees that CString
// before returning its packed pointer, so Value.JSONStringify is unsafe here.
func (s *workerJS) stringifyJSON(value *qjs.Value) (string, error) {
	if s == nil || s.ctx == nil || s.runtime == nil || s.jsonStringify == nil || value == nil {
		return "", errors.New("JSON serializer is unavailable")
	}

	this := s.ctx.NewUndefined()
	defer this.Free()

	result, err := s.ctx.Invoke(s.jsonStringify, this, value)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", errors.New("JSON.stringify returned no value")
	}
	defer result.Free()
	if !result.IsString() {
		return "", errors.New("JSON.stringify did not return a string")
	}

	packed := s.ctx.Call("QJS_ToCString", s.ctx.Raw(), result.Raw())
	if packed == nil {
		return "", errors.New("convert JSON string to UTF-8 failed")
	}
	packedPointer := packed.Raw()
	if packedPointer != 0 {
		defer s.ctx.FreeHandle(packedPointer)
	}
	if packedPointer == 0 || packedPointer>>32 != 0 {
		return "", errors.New("JSON string pointer is invalid")
	}

	packedBytes, err := s.runtime.Mem().Read(uint32(packedPointer), 8)
	if err != nil || len(packedBytes) != 8 {
		return "", errors.New("read packed JSON string pointer failed")
	}
	packedValue := binary.LittleEndian.Uint64(packedBytes)
	address := uint32(packedValue >> 32)
	length := uint32(packedValue)
	if address == 0 {
		return "", errors.New("JSON string address is invalid")
	}

	// Release the C string on both bounded-size rejection and memory-read
	// failures. The packed pointer remains live until its handle is freed above.
	freeCString := func() {
		s.ctx.Call("JS_FreeCString", s.ctx.Raw(), uint64(address))
	}
	if length > maxPayloadBytes {
		freeCString()
		return "", errors.New("JSON string exceeds the payload limit")
	}

	bytes, err := s.runtime.Mem().Read(address, uint64(length))
	if err != nil {
		freeCString()
		return "", errors.New("read JSON string failed")
	}
	encoded := string(bytes)
	freeCString()
	return encoded, nil
}

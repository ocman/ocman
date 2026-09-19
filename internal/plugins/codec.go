package plugins

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// Decoder reads one complete NDJSON envelope per line. Any malformed line or
// transport error is terminal; callers must terminate the associated process.
// A decoder has one reader and must not be used concurrently.
type Decoder struct {
	r    *bufio.Reader
	err  error
	size int // Last frame's JSON bytes, including unknown additive fields.
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{r: bufio.NewReaderSize(r, MaxMessageBytes+2)}
}

func (d *Decoder) Decode() (Envelope, error) {
	if d.err != nil {
		return Envelope{}, d.err
	}
	line, err := d.r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		err = ErrMessageTooLarge
	}
	if errors.Is(err, io.EOF) && len(line) > 0 {
		err = ErrInvalidMessage
	}
	if err != nil {
		d.err = err
		return Envelope{}, err
	}
	line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte{'\n'}), []byte{'\r'})
	d.size = len(line)
	if len(line) > MaxMessageBytes {
		d.err = ErrMessageTooLarge
		return Envelope{}, d.err
	}
	var e Envelope
	if checkJSON(line) != nil || checkFields(line, reflect.TypeFor[Envelope]()) != nil || json.Unmarshal(line, &e) != nil || e.Validate() != nil {
		d.err = ErrInvalidMessage
		return Envelope{}, d.err
	}
	return e, nil
}

// Encoder writes one validated line. The owner must serialize writes. A failed
// transport cannot be reused because it may have written a partial envelope.
type Encoder struct {
	w   io.Writer
	err error
}

func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

func (e *Encoder) Encode(message Envelope) error {
	if e.err != nil {
		return e.err
	}
	if err := message.Validate(); err != nil {
		return err
	}
	line, err := json.Marshal(message)
	if err != nil {
		return ErrInvalidMessage
	}
	if len(line) > MaxMessageBytes {
		return ErrMessageTooLarge
	}
	if err := checkJSON(line); err != nil {
		return err
	}
	line = append(line, '\n')
	n, err := e.w.Write(line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	e.err = err
	return err
}

// encoding/json otherwise accepts duplicate keys and invalid UTF-8. Check
// unknown fields too, so later protocol versions cannot interpret them differently.
func checkJSON(data []byte) error {
	if !utf8.Valid(data) {
		return ErrInvalidMessage
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := checkValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidMessage
	}
	return nil
}

func checkValue(d *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return ErrInvalidMessage
	}
	token, err := d.Token()
	if err != nil {
		return ErrInvalidMessage
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return ErrInvalidMessage
	}
	keys := make(map[string]bool)
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return ErrInvalidMessage
			}
			name, ok := key.(string)
			if !ok || keys[name] {
				return ErrInvalidMessage
			}
			keys[name] = true
		}
		if err := checkValue(d, depth+1); err != nil {
			return err
		}
	}
	if _, err := d.Token(); err != nil {
		return ErrInvalidMessage
	}
	return nil
}

// Reject case aliases accepted by encoding/json and null known fields. Raw
// capability data remains opaque; unknown additive fields remain permitted.
func checkFields(data []byte, typ reflect.Type) error {
	if typ == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return ErrInvalidMessage
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil {
			return ErrInvalidMessage
		}
		for key, value := range fields {
			for i := range typ.NumField() {
				field := typ.Field(i)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if strings.EqualFold(key, name) {
					if key != name || checkFields(value, field.Type) != nil {
						return ErrInvalidMessage
					}
					break
				}
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(data, &values) != nil {
			return ErrInvalidMessage
		}
		for _, value := range values {
			if checkFields(value, typ.Elem()) != nil {
				return ErrInvalidMessage
			}
		}
	}
	return nil
}

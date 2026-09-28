package codex

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/michaelquigley/df/dd"
)

// wireBindOpts binds provider event trees to the response DTOs. strict mode
// keeps typed fields exact: strict intake preserves number lexemes, integer
// fields parse them without a float64 pass, and type coercion is rejected.
// each DTO's +extra field lets unknown provider fields pass through
// uninterpreted, and its +nullable fields bind explicit provider nulls as
// absent, matching the previous encoding/json behavior.
var wireBindOpts = &dd.Options{Strict: true}

// opaqueSpans extracts the provider's exact byte spans from an event line:
// the top-level item member and each element of response.output. the
// strict-decoded tree cannot recover the original bytes (escape spellings,
// key order, and whitespace are not part of a decoded value), so these spans
// are the only verbatim copies pane retains of opaque provider items. the
// line has already passed strict decoding, so this walk only locates
// subtrees and cannot fail on syntax.
func opaqueSpans(data []byte) (item json.RawMessage, outputs []json.RawMessage, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, errors.New("event is not a JSON object")
	}
	if err := walkEventObject(dec, data, &item, &outputs); err != nil {
		return nil, nil, err
	}
	_, err = dec.Token()
	return item, outputs, err
}

func walkEventObject(dec *json.Decoder, data []byte, item *json.RawMessage, outputs *[]json.RawMessage) error {
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("event has a non-string key")
		}
		switch key {
		case "item":
			span, err := valueSpan(dec, data)
			if err != nil {
				return err
			}
			if !bytes.Equal(span, []byte("null")) {
				*item = span
			}
		case "response":
			if err := walkResponse(dec, data, outputs); err != nil {
				return err
			}
		default:
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
	}
	return nil
}

// walkResponse consumes the event's response value and records the byte
// spans of the elements of its output array. a null response or a response
// without an output array yields no spans.
func walkResponse(dec *json.Decoder, data []byte, outputs *[]json.RawMessage) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		return nil
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return errors.New("event response is not an object")
	}
	if d != '{' {
		return finishSkippedValue(dec, d)
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return errors.New("response has a non-string key")
		}
		if key != "output" {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
			continue
		}
		spans, err := walkOutputArray(dec, data)
		if err != nil {
			return err
		}
		*outputs = spans
	}
	_, err = dec.Token()
	return err
}

// walkOutputArray consumes the output member's value and returns the exact
// byte span of each array element, in order. an omitted or null value yields
// no spans; any other shape — a non-array container or a null or non-object
// element — is a protocol error. elements are never dropped, so a malformed
// member cannot masquerade as the empty terminal output that legitimately
// preserves completed streamed items.
func walkOutputArray(dec *json.Decoder, data []byte) ([]json.RawMessage, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, nil
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil, errors.New("response output is not an array or null")
	}
	if d != '[' {
		if err := finishSkippedValue(dec, d); err != nil {
			return nil, err
		}
		return nil, errors.New("response output is not an array or null")
	}
	var spans []json.RawMessage
	for dec.More() {
		span, err := valueSpan(dec, data)
		if err != nil {
			return nil, err
		}
		if len(span) == 0 || span[0] != '{' {
			return nil, errors.New("response output element is not an object")
		}
		spans = append(spans, span)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return spans, nil
}

// valueSpan consumes one JSON value and returns the exact bytes it occupied
// in the event line. the decoder's next-scan position may still sit on a
// separating comma or colon (or the whitespace around one) rather than on
// the value itself, so the start is stepped past any of those; the value's
// first byte can never be one of them.
func valueSpan(dec *json.Decoder, data []byte) (json.RawMessage, error) {
	start := int(dec.InputOffset())
	for start < len(data) {
		c := data[start]
		if isJSONWhitespace(c) || c == ',' || c == ':' {
			start++
			continue
		}
		break
	}
	if err := skipJSONValue(dec); err != nil {
		return nil, err
	}
	return data[start:int(dec.InputOffset())], nil
}

func isJSONWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// skipJSONValue consumes one whole JSON value, starting at the decoder's
// next token.
func skipJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); ok {
		return finishSkippedValue(dec, d)
	}
	return nil
}

// finishSkippedValue consumes the rest of a value whose opening delimiter
// has already been read.
func finishSkippedValue(dec *json.Decoder, d json.Delim) error {
	switch d {
	case '{':
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}
	return nil
}

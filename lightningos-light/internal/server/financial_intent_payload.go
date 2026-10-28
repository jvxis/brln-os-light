package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"lightningos-light/internal/logsafe"
)

// Reject case-folded duplicate properties before canonicalizing. Otherwise
// sorting object keys could change which value encoding/json assigns to a Go
// struct field. Canonical bytes are both approved and passed to the handler.
func decodeFinancialValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("financial request nesting is too deep")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("invalid financial request JSON")
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		result := map[string]any{}
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, errors.New("invalid financial request property")
			}
			key, ok := keyToken.(string)
			if !ok || seen[strings.ToLower(key)] {
				return nil, errors.New("duplicate financial request property")
			}
			seen[strings.ToLower(key)] = true
			value, err := decodeFinancialValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("invalid financial request object")
		}
		return result, nil
	case '[':
		result := []any{}
		for decoder.More() {
			value, err := decodeFinancialValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("invalid financial request array")
		}
		return result, nil
	default:
		return nil, errors.New("invalid financial request delimiter")
	}
}

func canonicalFinancialPayload(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoded, err := decodeFinancialValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("financial request must contain exactly one JSON object")
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("financial request must be a JSON object")
	}
	for key := range object {
		if strings.EqualFold(key, "confirm_password") {
			delete(object, key)
		}
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, errors.New("invalid financial request")
	}
	if logsafe.ContainsPrivateMaterial(string(canonical)) {
		return nil, errors.New("seed or private key material cannot be included in a financial request")
	}
	return canonical, nil
}

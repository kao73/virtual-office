package export

import "encoding/json"

// Encode returns the JSON encoding of v.
func Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}

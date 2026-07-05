package playground

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// idReference carries a parsed numeric ID or named resource reference.
type idReference struct {
	id         *int64
	identifier string
}

// parseIDReference accepts positive integer IDs or nonblank names.
func parseIDReference(raw json.RawMessage, field string) (idReference, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return idReference{}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return idReference{}, fmt.Errorf("%s must be an integer id or name", field)
	}
	switch typed := value.(type) {
	case nil:
		return idReference{}, nil
	case string:
		return textIDReference(typed, field)
	case json.Number:
		return numericIDReference(typed, field)
	default:
		return idReference{}, fmt.Errorf("%s must be an integer id or name", field)
	}
}

// textIDReference parses a string reference as an ID when possible or a name.
func textIDReference(text string, field string) (idReference, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return idReference{}, nil
	}
	if id, err := strconv.ParseInt(text, 10, 64); err == nil {
		return positiveIDReference(id, field)
	}
	return idReference{identifier: text}, nil
}

// numericIDReference parses a JSON number reference as a strict integer ID.
func numericIDReference(number json.Number, field string) (idReference, error) {
	id, err := number.Int64()
	if err != nil {
		return idReference{}, fmt.Errorf("%s must be an integer id or name", field)
	}
	return positiveIDReference(id, field)
}

// positiveIDReference rejects zero and negative numeric references.
func positiveIDReference(id int64, field string) (idReference, error) {
	if id <= 0 {
		return idReference{}, fmt.Errorf("%s must be a positive integer id or name", field)
	}
	return idReference{id: &id}, nil
}

package servicecatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// Decode accepts exactly one closed JSON value, enforces collection limits
// before appending another element, and returns the normalized catalog.
func Decode(content []byte) (Catalog, error) {
	if len(content) > MaxEncodedBytes {
		return Catalog{}, ErrEncodedLimit
	}
	if !utf8.Valid(content) {
		return Catalog{}, fmt.Errorf("%w: decode: input is not valid UTF-8", ErrInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	var catalog Catalog
	if err := decodeCatalog(decoder, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	if err := finish(decoder); err != nil {
		return Catalog{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	return Normalize(catalog)
}

func decodeCatalog(decoder *json.Decoder, out *Catalog) error {
	if err := beginObject(decoder, "catalog"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, 6)
	successorCount := 0
	for decoder.More() {
		field, err := objectField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "schema":
			err = decodeString(decoder, &out.Schema)
		case "authority":
			err = decodeAuthority(decoder, &out.Authority)
		case "override":
			var value OperatorOverride
			err = decodeOverride(decoder, &value)
			if err == nil {
				out.Override = &value
			}
		case "services":
			err = decodeServices(decoder, &out.Services, &successorCount)
		case "memberships":
			err = decodeMemberships(decoder, &out.Memberships)
		case "unowned":
			err = decodeUnowned(decoder, &out.Unowned)
		default:
			err = unknownField(field)
		}
		if err != nil {
			return err
		}
	}
	if err := endObject(decoder); err != nil {
		return err
	}
	return requireFields(seen, "schema", "authority", "services", "memberships", "unowned")
}

func decodeAuthority(decoder *json.Decoder, out *Authority) error {
	if err := beginObject(decoder, "authority"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		field, err := objectField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "kind":
			err = decodeString(decoder, &out.Kind)
		case "id":
			err = decodeString(decoder, &out.ID)
		case "version":
			err = decodeString(decoder, &out.Version)
		default:
			err = unknownField(field)
		}
		if err != nil {
			return err
		}
	}
	if err := endObject(decoder); err != nil {
		return err
	}
	return requireFields(seen, "kind", "id", "version")
}

func decodeOverride(decoder *json.Decoder, out *OperatorOverride) error {
	if err := beginObject(decoder, "override"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		field, err := objectField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "id":
			err = decodeString(decoder, &out.ID)
		case "version":
			err = decodeString(decoder, &out.Version)
		default:
			err = unknownField(field)
		}
		if err != nil {
			return err
		}
	}
	if err := endObject(decoder); err != nil {
		return err
	}
	return requireFields(seen, "id", "version")
}

func decodeServices(decoder *json.Decoder, out *[]Service, successorCount *int) error {
	if err := beginArray(decoder, "services"); err != nil {
		return err
	}
	for decoder.More() {
		if len(*out) >= MaxServices {
			return ErrServiceLimit
		}
		var service Service
		if err := decodeService(decoder, &service, successorCount); err != nil {
			return err
		}
		*out = append(*out, service)
	}
	return endArray(decoder)
}

func decodeService(decoder *json.Decoder, out *Service, successorCount *int) error {
	if err := beginObject(decoder, "service"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, 6)
	for decoder.More() {
		field, err := objectField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "key":
			err = decodeString(decoder, &out.Key)
		case "display_name":
			err = decodeString(decoder, &out.DisplayName)
		case "disposition":
			err = decodeString(decoder, &out.Disposition)
		case "origin":
			err = decodeString(decoder, &out.Origin)
		case "reason":
			err = decodeString(decoder, &out.Reason)
		case "successors":
			err = decodeSuccessors(decoder, &out.Successors, successorCount)
		default:
			err = unknownField(field)
		}
		if err != nil {
			return err
		}
	}
	if err := endObject(decoder); err != nil {
		return err
	}
	return requireFields(seen, "key", "display_name", "disposition", "origin")
}

func decodeSuccessors(decoder *json.Decoder, out *[]string, total *int) error {
	if err := beginArray(decoder, "successors"); err != nil {
		return err
	}
	for decoder.More() {
		if *total >= MaxSuccessorEdges {
			return ErrSuccessorLimit
		}
		var successor string
		if err := decodeString(decoder, &successor); err != nil {
			return err
		}
		*total++
		*out = append(*out, successor)
	}
	return endArray(decoder)
}

func decodeMemberships(decoder *json.Decoder, out *[]Membership) error {
	if err := beginArray(decoder, "memberships"); err != nil {
		return err
	}
	for decoder.More() {
		if len(*out) >= MaxMemberships {
			return ErrMembershipLimit
		}
		var membership Membership
		if err := decodeMembership(decoder, &membership); err != nil {
			return err
		}
		*out = append(*out, membership)
	}
	return endArray(decoder)
}

func decodeMembership(decoder *json.Decoder, out *Membership) error {
	if err := beginObject(decoder, "membership"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, 4)
	for decoder.More() {
		field, err := objectField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "service_key":
			err = decodeString(decoder, &out.ServiceKey)
		case "path":
			err = decodeString(decoder, &out.Path)
		case "role":
			err = decodeString(decoder, &out.Role)
		case "origin":
			err = decodeString(decoder, &out.Origin)
		default:
			err = unknownField(field)
		}
		if err != nil {
			return err
		}
	}
	if err := endObject(decoder); err != nil {
		return err
	}
	return requireFields(seen, "service_key", "path", "role", "origin")
}

func decodeUnowned(decoder *json.Decoder, out *[]UnownedPlacement) error {
	if err := beginArray(decoder, "unowned"); err != nil {
		return err
	}
	for decoder.More() {
		if len(*out) >= MaxDistinctPaths {
			return ErrDistinctPathLimit
		}
		var unowned UnownedPlacement
		if err := decodeUnownedPlacement(decoder, &unowned); err != nil {
			return err
		}
		*out = append(*out, unowned)
	}
	return endArray(decoder)
}

func decodeUnownedPlacement(decoder *json.Decoder, out *UnownedPlacement) error {
	if err := beginObject(decoder, "unowned placement"); err != nil {
		return err
	}
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		field, err := objectField(decoder, seen)
		if err != nil {
			return err
		}
		switch field {
		case "path":
			err = decodeString(decoder, &out.Path)
		case "origin":
			err = decodeString(decoder, &out.Origin)
		default:
			err = unknownField(field)
		}
		if err != nil {
			return err
		}
	}
	if err := endObject(decoder); err != nil {
		return err
	}
	return requireFields(seen, "path", "origin")
}

func beginObject(decoder *json.Decoder, label string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("%s must be an object", label)
	}
	return nil
}

func endObject(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("object is not terminated")
	}
	return nil
}

func beginArray(decoder *json.Decoder, label string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
		return fmt.Errorf("%s must be an array", label)
	}
	return nil
}

func endArray(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != ']' {
		return errors.New("array is not terminated")
	}
	return nil
}

func objectField(decoder *json.Decoder, seen map[string]struct{}) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	field, ok := token.(string)
	if !ok {
		return "", errors.New("object field name is not a string")
	}
	if _, duplicate := seen[field]; duplicate {
		return "", errors.New("duplicate field " + strconv.Quote(field))
	}
	seen[field] = struct{}{}
	return field, nil
}

func decodeString(decoder *json.Decoder, out *string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	value, ok := token.(string)
	if !ok {
		return errors.New("field value must be a string")
	}
	*out = value
	return nil
}

func unknownField(field string) error {
	return errors.New("json: unknown field " + strconv.Quote(field))
}

func requireFields(seen map[string]struct{}, fields ...string) error {
	for _, field := range fields {
		if _, exists := seen[field]; !exists {
			return errors.New("missing required field " + strconv.Quote(field))
		}
	}
	return nil
}

func finish(decoder *json.Decoder) error {
	_, err := decoder.Token()
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("catalog contains more than one JSON value")
}

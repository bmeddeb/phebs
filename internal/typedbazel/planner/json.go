package planner

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// ValidateJSONFields refuses encoding/json's case-insensitive struct aliases.
// Map keys retain their original case: Go import paths are case-sensitive.
// Callers separately bound bytes and reject duplicate exact object members.
func ValidateJSONFields(data []byte, dst any) error {
	var walk func(json.RawMessage, reflect.Type, int) error
	walk = func(raw json.RawMessage, typ reflect.Type, depth int) error {
		if depth > 32 {
			return errors.New("JSON field depth limit")
		}
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ == reflect.TypeFor[json.RawMessage]() {
			return nil
		}
		switch typ.Kind() {
		case reflect.Struct:
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				return err
			}
			fields := map[string]reflect.Type{}
			var collect func(reflect.Type)
			collect = func(t reflect.Type) {
				for i := 0; i < t.NumField(); i++ {
					f := t.Field(i)
					if f.PkgPath != "" {
						continue
					}
					tag := strings.Split(f.Tag.Get("json"), ",")[0]
					if tag == "-" {
						continue
					}
					if f.Anonymous && tag == "" && f.Type.Kind() == reflect.Struct {
						collect(f.Type)
						continue
					}
					if tag == "" {
						tag = f.Name
					}
					fields[tag] = f.Type
				}
			}
			collect(typ)
			for name, value := range object {
				field, ok := fields[name]
				if !ok {
					return errors.New("unknown or case-aliased JSON field")
				}
				if err := walk(value, field, depth+1); err != nil {
					return err
				}
			}
		case reflect.Slice, reflect.Array:
			if typ.Elem().Kind() == reflect.Uint8 {
				return nil
			}
			var values []json.RawMessage
			if err := json.Unmarshal(raw, &values); err != nil {
				return err
			}
			for _, value := range values {
				if err := walk(value, typ.Elem(), depth+1); err != nil {
					return err
				}
			}
		case reflect.Map:
			var object map[string]json.RawMessage
			if err := json.Unmarshal(raw, &object); err != nil {
				return err
			}
			for _, value := range object {
				if err := walk(value, typ.Elem(), depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(data, reflect.TypeOf(dst), 0)
}

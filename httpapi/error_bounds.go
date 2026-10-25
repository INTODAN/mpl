package httpapi

import (
	"fmt"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// MaxErrorDetails is the most entries a problem response lists in errors. A
// longer list keeps its first MaxErrorDetails-1 entries and ends with one entry
// that counts the rest.
const MaxErrorDetails = 100

var errorDetailsType = reflect.TypeFor[[]*huma.ErrorDetail]()

// BoundErrorDetails is a Huma transformer that keeps problem responses small.
// Huma's "unexpected property" error echoes the whole enclosing object as its
// value, so an object with n unknown keys would otherwise answer with n copies
// of itself, which is quadratic in the request size. The transformer drops
// every object or array value, since each location already points into the
// request, and caps the errors list.
//
// It accepts any struct pointer with an Errors []*huma.ErrorDetail field, so it
// also handles the copies that Huma's schema link transformer makes. Running it
// twice changes nothing.
func BoundErrorDetails(_ huma.Context, _ string, v any) (any, error) {
	pointer := reflect.ValueOf(v)
	if pointer.Kind() != reflect.Pointer || pointer.IsNil() || pointer.Elem().Kind() != reflect.Struct {
		return v, nil
	}
	field := pointer.Elem().FieldByName("Errors")
	if !field.IsValid() || field.Type() != errorDetailsType {
		return v, nil
	}
	details := field.Interface().([]*huma.ErrorDetail)
	bounded, changed := boundDetails(details)
	if !changed {
		return v, nil
	}
	// Copy the response rather than editing it, since a handler may return a
	// shared error value.
	copied := reflect.New(pointer.Elem().Type())
	copied.Elem().Set(pointer.Elem())
	copied.Elem().FieldByName("Errors").Set(reflect.ValueOf(bounded))
	return copied.Interface(), nil
}

func boundDetails(details []*huma.ErrorDetail) ([]*huma.ErrorDetail, bool) {
	kept := details
	if len(details) > MaxErrorDetails {
		kept = details[:MaxErrorDetails-1]
	}
	changed := len(kept) != len(details)
	for _, detail := range kept {
		if echoesComposite(detail) {
			changed = true
			break
		}
	}
	if !changed {
		return details, false
	}
	bounded := make([]*huma.ErrorDetail, 0, len(kept)+1)
	for _, detail := range kept {
		if echoesComposite(detail) {
			detail = &huma.ErrorDetail{Message: detail.Message, Location: detail.Location}
		}
		bounded = append(bounded, detail)
	}
	if omitted := len(details) - len(kept); omitted > 0 {
		bounded = append(bounded, &huma.ErrorDetail{Message: fmt.Sprintf("and %d more errors", omitted)})
	}
	return bounded, true
}

// echoesComposite reports whether a detail's value is an object or array.
// Huma's object and array errors, such as unexpected or missing properties,
// echo the whole enclosing object or array as the value. Scalar values stay.
func echoesComposite(detail *huma.ErrorDetail) bool {
	if detail == nil || detail.Value == nil {
		return false
	}
	value := reflect.ValueOf(detail.Value)
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		return true
	default:
		return false
	}
}

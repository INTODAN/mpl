package httpapi

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"
)

// MaxErrorDetails is the most entries a problem's errors list holds. A longer
// list keeps its first MaxErrorDetails-1 problems and ends with one entry, with
// no location, that counts the rest: "and N more problems".
const MaxErrorDetails = 100

// MaxErrorValueLength is the most bytes of a string value an error detail
// echoes. A longer string is cut at a rune boundary to end with "..." within
// that length.
const MaxErrorValueLength = 256

var errorDetailsType = reflect.TypeFor[[]*huma.ErrorDetail]()

// BoundErrorDetails is a Huma transformer that keeps problem responses small,
// applying LimitErrorDetails to their errors list. Without it, Huma's
// "unexpected property" error echoes the whole enclosing object as its value,
// so an object with n unknown keys answers with n copies of itself, which is
// quadratic in the request size.
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
	if !needsLimit(details) {
		return v, nil
	}
	// Copy the response rather than editing it, since a handler may return a
	// shared error value.
	copied := reflect.New(pointer.Elem().Type())
	copied.Elem().Set(pointer.Elem())
	copied.Elem().FieldByName("Errors").Set(reflect.ValueOf(LimitErrorDetails(details)))
	return copied.Interface(), nil
}

// LimitErrorDetails bounds a list of problems for a response:
//   - It drops object and array values. Huma's object errors, such as an
//     unexpected or missing property, echo the whole enclosing object, and the
//     location already points into the request.
//   - It cuts string values to MaxErrorValueLength bytes.
//   - A list longer than MaxErrorDetails keeps its first MaxErrorDetails-1
//     problems, listing unexpected properties after every other problem, and
//     ends with an entry that counts the rest.
//
// It returns details itself when nothing needs to change, and never edits the
// details it is given.
func LimitErrorDetails(details []*huma.ErrorDetail) []*huma.ErrorDetail {
	if !needsLimit(details) {
		return details
	}
	kept := details
	if len(details) > MaxErrorDetails {
		// Unknown keys come in map order and can be numerous, so list them
		// last rather than letting them push out a real problem.
		kept = slices.Clone(details)
		slices.SortStableFunc(kept, func(first, second *huma.ErrorDetail) int {
			return cmp.Compare(unexpectedRank(first), unexpectedRank(second))
		})
		kept = kept[:MaxErrorDetails-1]
	}
	limited := make([]*huma.ErrorDetail, 0, len(kept)+1)
	for _, detail := range kept {
		if value, changed := limitValue(detail); changed {
			detail = &huma.ErrorDetail{Message: detail.Message, Location: detail.Location, Value: value}
		}
		limited = append(limited, detail)
	}
	if omitted := len(details) - len(kept); omitted > 0 {
		limited = append(limited, &huma.ErrorDetail{Message: fmt.Sprintf("and %d more problems", omitted)})
	}
	return limited
}

func needsLimit(details []*huma.ErrorDetail) bool {
	if len(details) > MaxErrorDetails {
		return true
	}
	for _, detail := range details {
		if _, changed := limitValue(detail); changed {
			return true
		}
	}
	return false
}

func unexpectedRank(detail *huma.ErrorDetail) int {
	if detail != nil && detail.Message == validation.MsgUnexpectedProperty {
		return 1
	}
	return 0
}

// limitValue returns the value a detail may echo, and whether that differs
// from its own value.
func limitValue(detail *huma.ErrorDetail) (any, bool) {
	if detail == nil || detail.Value == nil {
		return nil, false
	}
	value := reflect.ValueOf(detail.Value)
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return detail.Value, false
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Map, reflect.Slice, reflect.Array, reflect.Struct:
		return nil, true
	case reflect.String:
		text := value.String()
		if len(text) <= MaxErrorValueLength {
			return detail.Value, false
		}
		cut := MaxErrorValueLength - len("...")
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		return text[:cut] + "...", true
	default:
		return detail.Value, false
	}
}

// Package optional handles detached copies and value equality for nullable fields.
package optional

func Copy[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func Equal[T comparable](left, right *T) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

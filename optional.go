package epostix

import "encoding/json"

type Optional[T any] struct {
	present bool
	null    bool
	value   T
}

func Set[T any](value T) Optional[T] {
	return Optional[T]{present: true, value: value}
}

func Null[T any]() Optional[T] {
	return Optional[T]{present: true, null: true}
}

func (o Optional[T]) IsZero() bool {
	return !o.present
}

func (o Optional[T]) IsNull() bool {
	return o.present && o.null
}

func (o Optional[T]) Get() (T, bool) {
	if !o.present || o.null {
		var zero T
		return zero, false
	}

	return o.value, true
}

func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if o.null || !o.present {
		return []byte("null"), nil
	}

	return json.Marshal(o.value)
}

func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.present = true

	if string(data) == "null" {
		o.null = true
		return nil
	}

	o.null = false

	return json.Unmarshal(data, &o.value)
}

package epostix

import (
	"reflect"
	"strings"
	"testing"
)

func methodNamesOf(value reflect.Value, into map[string]bool) {
	valueType := value.Type()

	for i := 0; i < valueType.NumMethod(); i++ {
		into[strings.ToLower(valueType.Method(i).Name)] = true
	}
}

func TestEveryOperationIsReachableFromTheClient(t *testing.T) {
	client := New("tix_test_abc")

	reachable := map[string]bool{}

	value := reflect.ValueOf(client).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() != reflect.Ptr || field.IsNil() || !value.Type().Field(i).IsExported() {
			continue
		}

		methodNamesOf(field, reachable)

		nested := field.Elem()
		if nested.Kind() != reflect.Struct {
			continue
		}

		for j := 0; j < nested.NumField(); j++ {
			child := nested.Field(j)
			if child.Kind() != reflect.Ptr || child.IsNil() || !nested.Type().Field(j).IsExported() {
				continue
			}

			methodNamesOf(child, reachable)
		}
	}

	specs := reflect.ValueOf(operations)
	missing := []string{}

	for i := 0; i < specs.NumField(); i++ {
		spec := specs.Field(i).Interface().(operationSpec)
		if !reachable[strings.ToLower(spec.ID)] {
			missing = append(missing, spec.ID)
		}
	}

	if len(missing) > 0 {
		t.Fatalf("unreachable operations: %s", strings.Join(missing, ", "))
	}
}

func TestTheOperationTableCoversEveryOperation(t *testing.T) {
	if count := reflect.ValueOf(operations).NumField(); count != 79 {
		t.Fatalf("operation table has %d entries, want 79", count)
	}
}

func TestOnlyDeduplicatedOperationsAdvertiseAnIdempotencyKey(t *testing.T) {
	specs := reflect.ValueOf(operations)

	idempotent := []string{}

	for i := 0; i < specs.NumField(); i++ {
		spec := specs.Field(i).Interface().(operationSpec)

		if spec.Idempotent {
			idempotent = append(idempotent, spec.ID)
		}

		if spec.RetryClass == RetryClassExcludedMutation && spec.Idempotent {
			t.Errorf("%s mints a credential and must not advertise an idempotency key", spec.ID)
		}
	}

	if len(idempotent) != 7 {
		t.Fatalf("expected 7 idempotent operations, got %d: %s", len(idempotent), strings.Join(idempotent, ", "))
	}
}

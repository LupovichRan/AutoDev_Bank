// Package testutil is the in-house assertion helper used by all tests (no testify).
package testutil

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func NoError(t testing.TB, err error, msg ...string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%sunexpected error: %v", prefix(msg), err)
	}
}

func Error(t testing.TB, err error, msg ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%sexpected an error, got nil", prefix(msg))
	}
}

func ErrorIs(t testing.TB, err, target error, msg ...string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%sexpected error %v, got %v", prefix(msg), target, err)
	}
}

func ErrorContains(t testing.TB, err error, substr string, msg ...string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), substr) {
		t.Fatalf("%sexpected error containing %q, got %v", prefix(msg), substr, err)
	}
}

func Equal[T any](t testing.TB, want, got T, msg ...string) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%swant %#v, got %#v", prefix(msg), want, got)
	}
}

func True(t testing.TB, cond bool, msg ...string) {
	t.Helper()
	if !cond {
		t.Fatalf("%sexpected condition to be true", prefix(msg))
	}
}

func False(t testing.TB, cond bool, msg ...string) {
	t.Helper()
	if cond {
		t.Fatalf("%sexpected condition to be false", prefix(msg))
	}
}

func Len[T any](t testing.TB, s []T, n int, msg ...string) {
	t.Helper()
	if len(s) != n {
		t.Fatalf("%swant len %d, got %d (%v)", prefix(msg), n, len(s), s)
	}
}

func prefix(msg []string) string {
	if len(msg) == 0 {
		return ""
	}
	return strings.Join(msg, " ") + ": "
}

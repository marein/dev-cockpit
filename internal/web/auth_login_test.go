package web

import (
	"testing"
)

// Both ways in carry the destination, so whichever one you take you land where
// you were sent to the login from.
func TestWithNext(t *testing.T) {
	cases := []struct{ path, next, want string }{
		{"/login", "/", "/login"},
		{"/login", "", "/login"},
		{"/login", "/projects", "/login?next=%2Fprojects"},
		{"/login?method=password", "/projects", "/login?method=password&next=%2Fprojects"},
		{"/login?method=passkey", "/coders/x?a=b", "/login?method=passkey&next=%2Fcoders%2Fx%3Fa%3Db"},
	}
	for _, tc := range cases {
		if got := withNext(tc.path, tc.next); got != tc.want {
			t.Errorf("withNext(%q, %q) = %q, want %q", tc.path, tc.next, got, tc.want)
		}
	}
}

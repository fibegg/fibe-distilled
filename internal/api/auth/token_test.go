package auth

import "testing"

func TestAuthorizedRequiresBearerTokenMatch(t *testing.T) {
	for _, tt := range []struct {
		name   string
		header string
		token  string
		want   bool
	}{
		{name: "matching bearer", header: "Bearer secret-token", token: "secret-token", want: true},
		{name: "wrong secret", header: "Bearer other-token", token: "secret-token"},
		{name: "missing bearer prefix", header: "secret-token", token: "secret-token"},
		{name: "lowercase scheme", header: "bearer secret-token", token: "secret-token"},
		{name: "empty token still requires exact bearer value", header: "Bearer ", token: "", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Authorized(tt.header, tt.token); got != tt.want {
				t.Fatalf("Authorized(%q, %q) = %v, want %v", tt.header, tt.token, got, tt.want)
			}
		})
	}
}

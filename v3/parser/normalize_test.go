package parser

import "testing"

// A Go method expression names the method like T.M does.
func TestNormalizeMethodExpression(t *testing.T) {
	for in, want := range map[string]string{"(*Repo).GetDays": "Repo.GetDays", "(*repository.Repo).GetDays": "repository.Repo.GetDays", "App\\User::find": "App.User.find"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

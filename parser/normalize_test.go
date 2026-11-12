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

// A route path is one segment kept as written: pkg.Load./ is not pkg.Load,
// pkg.Load./users is not pkg.Load.users, and no name suffix cuts into a path.
// Relative paths (./x, ../x) are not route paths.
func TestNormalizeKeepsRoutePath(t *testing.T) {
	for in, want := range map[string]string{
		"example.com/app/routes.Load./": "example.com.app.routes.Load./",
		"example.com/app.Load./users":   "example.com.app.Load./users",
		`App\Providers.map./`:           "App.Providers.map./",
		"/dashboard/users":              "/dashboard/users",
		"./utils.foo":                   "utils.foo",
		"../x/y.f":                      "x.y.f",
		"pkg/foo.Bar":                   "pkg.foo.Bar",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	for _, c := range []struct {
		qn, q string
		want  bool
	}{
		{"example.com/app.Load./", "example.com/app.Load", false},
		{"example.com/app.Load./", "Load", false},
		{"example.com/app.Load./users", "users", false},
		{"example.com/app.Load./users", "/users", true},
		{"example.com/app.Load./users", "Load./users", true},
		{"example.com/app.main./static/app.js", "js", false},
		{"example.com/app.main./static/app.js", "/static/app.js", true},
	} {
		if got := MatchQualified(c.qn, c.q); got != c.want {
			t.Errorf("MatchQualified(%q, %q) = %v, want %v", c.qn, c.q, got, c.want)
		}
	}
}

package prboard

import "testing"

func TestCategory(t *testing.T) {
	const web = "acme/web"
	cases := []struct{ title, repo, want string }{
		{"Checkout [web] Cart page: show the totals", web, ""},
		{"[search] Hide the legacy filters behind the flag", web, "search"},
		{"[web | design system] Move the settings pages onto the new kit", web, "design system"},
		{"[web] Remove the retired coupon editor (WEB-7062)", web, ""},
		{"[OPS-6993] Bump the image renderer", "acme/infra", ""},
		{"[Design system] Move the account pages onto the kit", "acme/api", "Design system"},
		{"[Checkout] [web] X", web, "Checkout"},
		{"[web] [Checkout] X", web, "Checkout"},
		{"[WEB] repo prefix in another case", web, ""},
		{"[acme/web] owner/repo prefix", web, ""},
		{"[WEB-1 | Billing migration] key then category", web, "Billing migration"},
		{"[required:#12] never a category", web, ""},
		{"[ Required: web#12 ] [Checkout] spaced required token", web, "Checkout"},
		{"[] [ | ] empty brackets", web, ""},
		{"no brackets at all", web, ""},
		{"[unclosed bracket", web, ""},
	}
	for _, c := range cases {
		if got := Category(c.title, c.repo); got != c.want {
			t.Errorf("%q in %s: got %q want %q", c.title, c.repo, got, c.want)
		}
	}
}

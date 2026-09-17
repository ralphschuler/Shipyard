package updates

import "testing"

func TestCompareOnlyReportsNewerSemanticRelease(t *testing.T) {
	for _, tc := range []struct{ current, release, want string }{
		{"1.2.0", "1.3.0", "update_available"},
		{"1.3.0", "1.3.0", "up_to_date"},
		{"1.3.0", "1.2.9", "up_to_date"},
		{"development", "1.3.0", "unverified"},
	} {
		if got := Compare(tc.current, tc.release); got != tc.want {
			t.Errorf("Compare(%q,%q) = %q, want %q", tc.current, tc.release, got, tc.want)
		}
	}
}

func TestValidateReleaseRequiresFullTrustChainMetadata(t *testing.T) {
	r := Release{Version: "v1.3.0", Commit: "0123456789012345678901234567890123456789", URL: "https://github.com/ralphschuler/Shipyard/releases/tag/v1.3.0", Verified: true, Compatible: true, Checksum: "0123456789012345678901234567890123456789012345678901234567890123"}
	if err := ValidateRelease(r, "ralphschuler/Shipyard"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Release){"short commit": func(x *Release) { x.Commit = "abc123" }, "short checksum": func(x *Release) { x.Checksum = "abc" }, "wrong host": func(x *Release) { x.URL = "https://example.com/release" }, "unverified": func(x *Release) { x.Verified = false }} {
		t.Run(name, func(t *testing.T) {
			c := r
			mutate(&c)
			if ValidateRelease(c, "ralphschuler/Shipyard") == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

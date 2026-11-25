package constants

import (
	"regexp"
	"testing"

	"github.com/Masterminds/semver/v3"
)

// Release tags are v<GarceslabVersion> and the release workflow
// (.github/scripts/release-check.sh) accepts only MAJOR.MINOR.PATCH.
func TestGarceslabVersionIsPlainSemver(t *testing.T) {
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(GarceslabVersion) {
		t.Fatalf("GarceslabVersion %q is not MAJOR.MINOR.PATCH", GarceslabVersion)
	}
	v := semver.MustParse(GarceslabVersion)
	if v.LessThan(semver.MustParse("1.0.0")) {
		t.Fatalf("GarceslabVersion %q is below 1.0.0, where the project's own versioning starts", GarceslabVersion)
	}
}

// Version is Seanime's version and stays on the 3.x line: extensions' semver
// constraints, Nakama and config migrations compare it, and the TV app refuses servers
// below its MIN_SERVER_VERSION (3.8.0). Garceslab Anime's own 1.x version lives in
// GarceslabVersion instead.
func TestSeanimeVersionStaysCompatible(t *testing.T) {
	v, err := semver.NewVersion(Version)
	if err != nil {
		t.Fatalf("Version %q is not semver: %v", Version, err)
	}
	if v.Major() != 3 || v.LessThan(semver.MustParse("3.8.0")) {
		t.Fatalf("Version %q must stay Seanime's 3.x (>= 3.8.0) for client and extension compatibility", Version)
	}
	if Version == GarceslabVersion {
		t.Fatalf("Version and GarceslabVersion must be separate (both are %q)", Version)
	}
}

package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/HazelnutParadise/jelly-cms/internal/safehttp"
	"github.com/HazelnutParadise/jelly-cms/internal/version"
)

// Size limits for metadata responses.
const maxJSONBytes = 4 << 20

// ghRelease is the part of a GitHub release object the updater reads.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Check asks the release source for the newest version on channel and records
// the result in the state file. The request carries only the running version
// and the operating system, and no cookies or account data.
func Check(ctx context.Context, channel string) (Release, error) {
	var rel Release
	switch channel {
	case ChannelStable:
		var gh ghRelease
		if err := getJSON(ctx, fmt.Sprintf("/repos/%s/%s/releases/latest", repoOwner, repoName), &gh); err != nil {
			return Release{}, err
		}
		if gh.Prerelease || gh.Draft {
			return Release{}, ErrNoRelease
		}
		rel = toRelease(gh, channel)
	case ChannelPreview:
		var list []ghRelease
		if err := getJSON(ctx, fmt.Sprintf("/repos/%s/%s/releases?per_page=30", repoOwner, repoName), &list); err != nil {
			return Release{}, err
		}
		best := -1
		var bestVersion semver
		for i, gh := range list {
			if gh.Draft {
				continue
			}
			v, err := parseVersion(gh.TagName)
			if err != nil {
				continue
			}
			if best < 0 || compareVersions(v, bestVersion) > 0 {
				best, bestVersion = i, v
			}
		}
		if best < 0 {
			return Release{}, ErrNoRelease
		}
		rel = toRelease(list[best], channel)
	default:
		return Release{}, fmt.Errorf("update: unknown channel %q", channel)
	}
	if _, err := parseVersion(rel.Version); err != nil {
		return Release{}, ErrNoRelease
	}
	checked := time.Now().UTC()
	err := mutateState(func(st *State) bool {
		st.Current = version.Version
		st.Latest = rel.Version
		st.CheckedAt = checked
		return true
	})
	return rel, err
}

// findRelease looks up the release with the tag target among the recent releases.
func findRelease(ctx context.Context, target string) (Release, error) {
	var list []ghRelease
	if err := getJSON(ctx, fmt.Sprintf("/repos/%s/%s/releases?per_page=100", repoOwner, repoName), &list); err != nil {
		return Release{}, err
	}
	want := normalizeVersion(target)
	for _, gh := range list {
		if !gh.Draft && normalizeVersion(gh.TagName) == want {
			return toRelease(gh, ""), nil
		}
	}
	return Release{}, ErrNoRelease
}

func toRelease(gh ghRelease, channel string) Release {
	rel := Release{
		Version:     normalizeVersion(gh.TagName),
		Channel:     channel,
		PublishedAt: gh.PublishedAt,
	}
	for _, a := range gh.Assets {
		rel.Assets = append(rel.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return rel
}

// asset returns the release asset with the given name.
func (r Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// userAgent carries the only identifying data a check sends: the running
// version and the operating system.
func userAgent() string {
	return fmt.Sprintf("jelly-cms/%s (%s)", version.Version, runtime.GOOS)
}

// httpClient returns a safehttp client for release traffic. Redirects must stay
// on the allow list, and no response body may exceed maxBytes.
func httpClient(maxBytes int64, timeout time.Duration) *http.Client {
	c := safehttp.Client(safehttp.Options{Timeout: timeout, MaxBytes: maxBytes, AllowHosts: AllowedHosts})
	next := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := allowURL(req.URL.String()); err != nil {
			return err
		}
		return next(req, via)
	}
	return c
}

// getJSON reads a GitHub API response into out. The request carries no
// credentials of any kind.
func getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase()+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient(maxJSONBytes, 30*time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("update: request release source: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return fmt.Errorf("update: release source answered %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("update: decode release source response: %w", err)
	}
	return nil
}

// normalizeVersion drops surrounding space and a leading "v" from a tag.
func normalizeVersion(raw string) string {
	return strings.TrimPrefix(strings.TrimSpace(raw), "v")
}

// ValidVersion reports whether raw names a semantic version such as v1.2.3 or
// 1.2.3-rc.1.
func ValidVersion(raw string) error {
	_, err := parseVersion(raw)
	return err
}

// IsNewer reports whether latest is a newer version than current. A current
// value that is not a semantic version (a development build) is older than any
// valid latest version.
func IsNewer(latest, current string) bool {
	l, err := parseVersion(latest)
	if err != nil {
		return false
	}
	c, err := parseVersion(current)
	if err != nil {
		return true
	}
	return compareVersions(l, c) > 0
}

// semver is a parsed MAJOR.MINOR.PATCH[-PRERELEASE] version. Build metadata is ignored.
type semver struct {
	core [3]int
	pre  []string
}

var errBadVersion = errors.New("update: not a semantic version")

func parseVersion(raw string) (semver, error) {
	s := normalizeVersion(raw)
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var pre string
	hasPre := false
	if i := strings.IndexByte(s, '-'); i >= 0 {
		pre, hasPre = s[i+1:], true
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return semver{}, errBadVersion
	}
	var v semver
	for i, p := range parts {
		if p == "" || (len(p) > 1 && p[0] == '0') {
			return semver{}, errBadVersion
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return semver{}, errBadVersion
		}
		v.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return semver{}, errBadVersion
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" {
				return semver{}, errBadVersion
			}
			for _, r := range id {
				ok := r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
				if !ok {
					return semver{}, errBadVersion
				}
			}
			v.pre = append(v.pre, id)
		}
	}
	return v, nil
}

// compareVersions orders two parsed versions by semantic version precedence.
func compareVersions(a, b semver) int {
	for i := range a.core {
		if a.core[i] != b.core[i] {
			if a.core[i] < b.core[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, xerr := strconv.Atoi(x)
		yn, yerr := strconv.Atoi(y)
		xNum, yNum := xerr == nil, yerr == nil
		switch {
		case xNum && yNum:
			if xn != yn {
				if xn < yn {
					return -1
				}
				return 1
			}
		case xNum:
			return -1
		case yNum:
			return 1
		case x != y:
			if x < y {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

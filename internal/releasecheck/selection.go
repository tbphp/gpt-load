package releasecheck

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const supportedMajor uint64 = 2

const maxSafeInteger int64 = 1<<53 - 1

var releaseVersionPattern = regexp.MustCompile(
	`^(?:v)?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`,
)

// Release is the public GitHub metadata used to select a newer version.
type Release struct {
	TagName     string
	HTMLURL     string
	PublishedAt time.Time
	Draft       bool
}

// Update is the immutable public result exposed to the control plane.
type Update struct {
	Version       string
	ReleaseURL    string
	PublishedAtMS int64
}

// SelectUpdate returns the highest eligible V2 release newer than current.
func SelectUpdate(current string, releases []Release) *Update {
	currentVersion, ok := parseVersion(current)
	if !ok || currentVersion.major != supportedMajor {
		return nil
	}

	var selected *Release
	var selectedVersion semanticVersion
	for index := range releases {
		candidate := &releases[index]
		candidateVersion, valid := validCandidate(*candidate)
		if !valid || candidateVersion.major != supportedMajor ||
			compareVersions(candidateVersion, currentVersion) <= 0 ||
			(currentVersion.stable() && !candidateVersion.stable()) {
			continue
		}
		if selected == nil || compareVersions(candidateVersion, selectedVersion) > 0 {
			selected = candidate
			selectedVersion = candidateVersion
		}
	}
	if selected == nil {
		return nil
	}
	return &Update{
		Version:       selected.TagName,
		ReleaseURL:    selected.HTMLURL,
		PublishedAtMS: selected.PublishedAt.UnixMilli(),
	}
}

type semanticVersion struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []prereleaseIdentifier
}

type prereleaseIdentifier struct {
	raw     string
	number  uint64
	numeric bool
}

func (version semanticVersion) stable() bool {
	return len(version.prerelease) == 0
}

func parseVersion(raw string) (semanticVersion, bool) {
	matches := releaseVersionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if matches == nil {
		return semanticVersion{}, false
	}
	major, ok := parseVersionNumber(matches[1])
	if !ok {
		return semanticVersion{}, false
	}
	minor, ok := parseVersionNumber(matches[2])
	if !ok {
		return semanticVersion{}, false
	}
	patch, ok := parseVersionNumber(matches[3])
	if !ok {
		return semanticVersion{}, false
	}
	result := semanticVersion{major: major, minor: minor, patch: patch}
	if matches[4] == "" {
		return result, true
	}
	for _, rawIdentifier := range strings.Split(matches[4], ".") {
		identifier := prereleaseIdentifier{raw: rawIdentifier}
		if allASCIIBytes(rawIdentifier, '0', '9') {
			if len(rawIdentifier) > 1 && rawIdentifier[0] == '0' {
				return semanticVersion{}, false
			}
			identifier.number, ok = parseVersionNumber(rawIdentifier)
			if !ok {
				return semanticVersion{}, false
			}
			identifier.numeric = true
		}
		result.prerelease = append(result.prerelease, identifier)
	}
	return result, true
}

func parseVersionNumber(raw string) (uint64, bool) {
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}

func allASCIIBytes(value string, minimum, maximum byte) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		if value[index] < minimum || value[index] > maximum {
			return false
		}
	}
	return true
}

func compareVersions(left, right semanticVersion) int {
	for _, pair := range [][2]uint64{
		{left.major, right.major},
		{left.minor, right.minor},
		{left.patch, right.patch},
	} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if left.stable() {
		if right.stable() {
			return 0
		}
		return 1
	}
	if right.stable() {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		leftIdentifier := left.prerelease[index]
		rightIdentifier := right.prerelease[index]
		switch {
		case leftIdentifier.numeric && rightIdentifier.numeric:
			if leftIdentifier.number < rightIdentifier.number {
				return -1
			}
			if leftIdentifier.number > rightIdentifier.number {
				return 1
			}
		case leftIdentifier.numeric:
			return -1
		case rightIdentifier.numeric:
			return 1
		case leftIdentifier.raw < rightIdentifier.raw:
			return -1
		case leftIdentifier.raw > rightIdentifier.raw:
			return 1
		}
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}

func validCandidate(release Release) (semanticVersion, bool) {
	version, ok := parseVersion(release.TagName)
	if !ok || release.Draft || !validPublishedAt(release.PublishedAt) ||
		!validReleaseURL(release.HTMLURL, release.TagName) {
		return semanticVersion{}, false
	}
	return version, true
}

func validPublishedAt(publishedAt time.Time) bool {
	if publishedAt.IsZero() {
		return false
	}
	milliseconds := publishedAt.UnixMilli()
	return milliseconds >= 0 && milliseconds <= maxSafeInteger
}

func validReleaseURL(raw, tag string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return parsed.Path == "/tbphp/gpt-load/releases/tag/"+tag
}

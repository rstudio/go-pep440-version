package version

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/rstudio/go-version/pkg/part"
)

var (
	// The compiled regular expression used to test the validity of a version.
	versionRegex *regexp.Regexp

	// https://github.com/pypa/packaging/blob/a6407e3a7e19bd979e93f58cfc7f6641a7378c46/packaging/version.py#L459-L464
	preReleaseAliases = map[string]string{
		"a":       "a",
		"alpha":   "a",
		"b":       "b",
		"beta":    "b",
		"rc":      "rc",
		"c":       "rc",
		"pre":     "rc",
		"preview": "rc",
	}

	// https://github.com/pypa/packaging/blob/a6407e3a7e19bd979e93f58cfc7f6641a7378c46/packaging/version.py#L465-L466
	postReleaseAliases = map[string]string{
		"post": "post",
		"rev":  "post",
		"r":    "post",
	}
)

const (
	// The raw regular expression string used for testing the validity of a version.
	regex = `v?` +
		`(?:` +
		`(?:(?P<epoch>[0-9]+)!)?` + // epoch
		`(?P<release>[0-9]+(?:\.[0-9]+)*)` + // release segment
		`(?P<pre>[-_\.]?(?P<pre_l>(a|b|c|rc|alpha|beta|pre|preview))[-_\.]?(?P<pre_n>[0-9]+)?)?` + // pre-release
		`(?P<post>(?:-(?P<post_n1>[0-9]+))|(?:[-_\.]?(?P<post_l>post|rev|r)[-_\.]?(?P<post_n2>[0-9]+)?))?` + // post release
		`(?P<dev>[-_\.]?(?P<dev_l>dev)[-_\.]?(?P<dev_n>[0-9]+)?)?)` + // dev release
		`(?:\+(?P<local>[a-z0-9]+(?:[-_\.][a-z0-9]+)*))?` // local version`
)

// Version represents a single version.
type Version struct {
	epoch              part.BigInt
	release            []part.BigInt
	pre                letterNumber
	post               letterNumber
	dev                letterNumber
	local              string
	key                key
	preReleaseIncluded bool
	original           string
}

type key struct {
	epoch   part.BigInt
	release part.Parts
	pre     part.Part
	post    part.Part
	dev     part.Part
	local   part.Part
}

func (k key) compare(o key) int {
	p1 := part.Parts{k.epoch, k.release, k.pre, k.post, k.dev, k.local}
	p2 := part.Parts{o.epoch, o.release, o.pre, o.post, o.dev, o.local}
	return p1.Compare(p2)
}

type letterNumber struct {
	letter part.String
	number part.BigInt
}

func (ln letterNumber) isNull() bool {
	return ln.letter.IsNull() && ln.number.IsNull()
}

func init() {
	versionRegex = regexp.MustCompile(`(?i)^\s*` + regex + `\s*$`)
}

// MustParse is like Parse but panics if the version cannot be parsed.
func MustParse(v string) Version {
	ver, err := Parse(v)
	if err != nil {
		panic(err)
	}
	return ver
}

// Parse parses the given version and returns a new Version.
func Parse(v string) (Version, error) {
	matches := versionRegex.FindStringSubmatch(v)
	if matches == nil {
		return Version{}, fmt.Errorf("malformed version: %s", v)
	}

	var epoch, preN, postN, devN part.BigInt
	var preL, postL, devL part.String
	var release []part.BigInt
	var local string
	var err error

	for i, name := range versionRegex.SubexpNames() {
		m := matches[i]
		if m == "" {
			continue
		}

		switch name {
		case "epoch":
			epoch, err = part.NewBigInt(m)
		case "release":
			for _, str := range strings.Split(m, ".") {
				val, err := part.NewBigInt(str)
				if err != nil {
					return Version{}, fmt.Errorf("error parsing version: %w", err)
				}

				release = append(release, val)
			}
		case "pre_l":
			preL = part.String(preReleaseAliases[strings.ToLower(m)])
		case "pre_n":
			preN, err = part.NewBigInt(m)
		case "post_l":
			postL = part.String(postReleaseAliases[strings.ToLower(m)])
		case "post_n1", "post_n2":
			// https://github.com/pypa/packaging/blob/a6407e3a7e19bd979e93f58cfc7f6641a7378c46/packaging/version.py#L469-L472
			if postL == "" {
				postL = "post"
			}
			postN, err = part.NewBigInt(m)
		case "dev_l":
			devL = part.String(strings.ToLower(m))
		case "dev_n":
			devN, err = part.NewBigInt(m)
		case "local":
			local = strings.ToLower(m)
		}
		if err != nil {
			return Version{}, fmt.Errorf("failed to parse version (%s): %w", v, err)
		}
	}

	pre := letterNumber{
		letter: preL,
		number: preN,
	}
	post := letterNumber{
		letter: postL,
		number: postN,
	}
	dev := letterNumber{
		letter: devL,
		number: devN,
	}

	return Version{
		epoch:    epoch,
		release:  release,
		pre:      pre,
		post:     post,
		dev:      dev,
		local:    local,
		key:      cmpkey(epoch, release, pre, post, dev, local),
		original: v,
	}, nil
}

// ref. https://github.com/pypa/packaging/blob/a6407e3a7e19bd979e93f58cfc7f6641a7378c46/packaging/version.py#L495
func cmpkey(epoch part.BigInt, release []part.BigInt, pre, post, dev letterNumber, local string) key {
	// Set default values
	k := key{
		epoch: epoch,
		pre:   part.Parts{pre.letter, pre.number},
		post:  part.Parts{post.letter, post.number},
		dev:   part.Parts{dev.letter, dev.number},
		local: part.NegativeInfinity,
	}

	// Remove trailing zeros
	k.release = part.BigIntSliceToParts(release).Normalize()

	// https://github.com/pypa/packaging/blob/a6407e3a7e19bd979e93f58cfc7f6641a7378c46/packaging/version.py#L514-L517
	if pre.isNull() && post.isNull() && !dev.isNull() {
		k.pre = part.NegativeInfinity
	} else if pre.isNull() {
		k.pre = part.Infinity
	}

	// Versions without a post segment should sort before those with one.
	if post.isNull() {
		k.post = part.NegativeInfinity
	}

	// Versions without a development segment should sort after those with one.
	if dev.isNull() {
		k.dev = part.Infinity
	}

	// Versions with a local segment need that segment parsed to implement the sorting rules in PEP440.
	//   - Alpha numeric segments sort before numeric segments
	//   - Alpha numeric segments sort lexicographically
	//   - Numeric segments sort numerically
	//   - Shorter versions sort before longer versions when the prefixes match exactly
	if local != "" {
		var parts part.Parts
		for _, l := range strings.Split(local, ".") {
			if p, err := part.NewBigInt(l); err == nil {
				parts = append(parts, p)
			} else {
				parts = append(parts, part.NewPreString(l))
			}
		}
		k.local = parts
	}

	return k
}

// Compare compares this version to another version. This
// returns -1, 0, or 1 if this version is smaller, equal,
// or larger than the other version, respectively.
func (v Version) Compare(other Version) int {
	// A quick, efficient equality check
	if v.String() == other.String() {
		return 0
	}

	k1 := v.key
	k2 := other.key

	k1.release = k1.release.Padding(len(k2.release), part.Zero)
	k2.release = k2.release.Padding(len(k2.release), part.Zero)

	return k1.compare(k2)
}

// Equal tests if two versions are equal.
func (v Version) Equal(o Version) bool {
	return v.Compare(o) == 0
}

// GreaterThan tests if this version is greater than another version.
func (v Version) GreaterThan(o Version) bool {
	return v.Compare(o) > 0
}

// GreaterThanOrEqual tests if this version is greater than or equal to another version.
func (v Version) GreaterThanOrEqual(o Version) bool {
	return v.Compare(o) >= 0
}

// LessThan tests if this version is less than another version.
func (v Version) LessThan(o Version) bool {
	return v.Compare(o) < 0
}

// LessThanOrEqual tests if this version is less than or equal to another version.
func (v Version) LessThanOrEqual(o Version) bool {
	return v.Compare(o) <= 0
}

// String returns the full version string included pre-release
// and metadata information.
func (v Version) String() string {
	var buf bytes.Buffer

	// Epoch
	if v.epoch.Compare(part.Zero) == 1 {
		fmt.Fprintf(&buf, "%s!", v.epoch)
	}

	// Release segment
	fmt.Fprintf(&buf, "%s", v.release[0])
	for _, r := range v.release[1:len(v.release)] {
		fmt.Fprintf(&buf, ".%s", r)
	}

	// Pre-release
	if !v.pre.isNull() {
		fmt.Fprintf(&buf, "%s%s", v.pre.letter, v.pre.number)
	}

	// Post-release
	if !v.post.isNull() {
		fmt.Fprintf(&buf, ".post%s", v.post.number)
	}

	// Development release
	if !v.dev.isNull() {
		fmt.Fprintf(&buf, ".dev%s", v.dev.number)
	}

	// Local version segment
	if v.local != "" {
		fmt.Fprintf(&buf, "+%s", v.local)
	}

	return buf.String()
}

// BaseVersion returns the base version
func (v Version) BaseVersion() string {
	var buf bytes.Buffer

	// Epoch
	if v.epoch.Compare(part.Zero) == 1 {
		fmt.Fprintf(&buf, "%s!", v.epoch.String())
	}

	// Release segment
	fmt.Fprintf(&buf, "%s", v.release[0].String())
	for _, r := range v.release[1:len(v.release)] {
		fmt.Fprintf(&buf, ".%s", r.String())
	}

	return buf.String()
}

// Original returns the original parsed version as-is, including any
// potential whitespace, `v` prefix, etc.
func (v Version) Original() string {
	return v.original
}

// Local returns the local version
func (v Version) Local() string {
	return v.local
}

// Public returns the public version
func (v Version) Public() string {
	return strings.SplitN(v.String(), "+", 2)[0]
}

// IsPreRelease returns if it is a pre-release
func (v Version) IsPreRelease() bool {
	if v.preReleaseIncluded {
		return false
	}
	return !v.pre.isNull() || !v.dev.isNull()
}

// IsPostRelease returns if it is a post-release
func (v Version) IsPostRelease() bool {
	return !v.post.isNull()
}

type SortedVersions []Version

func (s SortedVersions) Len() int {
	return len(s)
}
func (s SortedVersions) Less(i, j int) bool {
	a := s[i]
	b := s[j]

	return a.LessThan(b)
}
func (s SortedVersions) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

package version

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/xerrors"
)

const (
	specifierRegex = `(?P<operator>(~=|===|==|!=|<=|>=|<|>))\s*(?P<version>[^\s]*)`
)

var (
	specifierOperators = map[string]operatorFunc{
		"==":  specifierEqual,
		"!=":  specifierNotEqual,
		">":   specifierGreaterThan,
		"<":   specifierLessThan,
		">=":  specifierGreaterThanEqual,
		"<=":  specifierLessThanEqual,
		"===": specifierArbitrary,
		"~=":  specifierCompatible,
	}

	specifierRegexp = regexp.MustCompile(`(?i)^\s*` + specifierRegex + `\s*$`)
	prefixRegexp    = regexp.MustCompile(`^([0-9]+)((?:a|b|c|rc)[0-9]+)$`)
)

type operatorFunc func(v Version, c string) bool

type Constraints struct {
	constraints [][]constraint
}

type constraint struct {
	version  string
	operator operatorFunc
	original string
}

// NewConstraints parses a given constraint and returns a new instance of Constraints
func NewConstraints(v string) (Constraints, error) {
	var css [][]constraint
	for _, vv := range strings.Split(v, "||") {
		var cs []constraint
		for _, single := range strings.Split(vv, ",") {
			c, err := newConstraint(single)
			if err != nil {
				return Constraints{}, err
			}
			cs = append(cs, c)
		}
		css = append(css, cs)
	}

	return Constraints{
		constraints: css,
	}, nil

}

func newConstraint(c string) (constraint, error) {
	m := specifierRegexp.FindStringSubmatch(c)
	if m == nil {
		return constraint{}, xerrors.Errorf("improper constraint: %s", c)
	}

	operator := m[specifierRegexp.SubexpIndex("operator")]
	version := m[specifierRegexp.SubexpIndex("version")]

	if operator != "===" {
		if err := validate(operator, version); err != nil {
			return constraint{}, err
		}
	}

	return constraint{
		version:  version,
		operator: specifierOperators[operator],
		original: c,
	}, nil
}

func validate(operator, version string) error {
	hasWildcard := false
	if strings.HasSuffix(version, ".*") {
		hasWildcard = true
		version = strings.TrimSuffix(version, ".*")
	}
	v, err := Parse(version)
	if err != nil {
		return xerrors.Errorf("version parse error (%s): %w", v, err)
	}

	switch operator {
	case "==", "!=":
		if hasWildcard && (!v.dev.isNull() || v.local != "") {
			return xerrors.New("the (non)equality operators don't allow to use a wild card and a dev" +
				" or local version together")
		}
	case "~=":
		if hasWildcard {
			return xerrors.New("a wild card is not allowed")
		} else if len(v.release) < 2 {
			return xerrors.New("the compatible operator requires at least two digits in the release segment")
		} else if v.local != "" {
			return xerrors.New("local versions cannot be specified")
		}
	default:
		if hasWildcard {
			return xerrors.New("a wild card is not allowed")
		} else if v.local != "" {
			return xerrors.New("local versions cannot be specified")
		}
	}
	return nil
}

// Check tests if a version satisfies all the constraints.
func (cs Constraints) Check(v Version) bool {
	for _, c := range cs.constraints {
		if andCheck(v, c) {
			return true
		}
	}

	return false
}

func (c constraint) check(v Version) bool {
	return c.operator(v, c.version)
}

func andCheck(v Version, constraints []constraint) bool {
	for _, c := range constraints {
		if !c.check(v) {
			return false
		}
	}
	return true
}

func versionSplit(version string) []string {
	var result []string
	for _, v := range strings.Split(version, ".") {
		m := prefixRegexp.FindStringSubmatch(v)
		if m != nil {
			result = append(result, m[1:]...)
		} else {
			result = append(result, v)
		}
	}
	return result
}

func isDigist(s string) bool {
	if _, err := strconv.Atoi(s); err == nil {
		return true
	}
	return false
}

func padVersion(left, right []string) ([]string, []string) {
	var leftRelease, rightRelease []string
	for _, l := range left {
		if isDigist(l) {
			leftRelease = append(leftRelease, l)
		}
	}

	for _, r := range right {
		if isDigist(r) {
			rightRelease = append(rightRelease, r)
		}
	}

	// Get the rest of our versions
	leftRest := left[len(leftRelease):]
	rightRest := left[len(rightRelease):]

	for i := 0; i < len(leftRelease)-len(rightRelease); i++ {
		rightRelease = append(rightRelease, "0")
	}
	for i := 0; i < len(rightRelease)-len(leftRelease); i++ {
		leftRelease = append(leftRelease, "0")
	}

	return append(leftRelease, leftRest...), append(rightRelease, rightRest...)
}

//-------------------------------------------------------------------
// Specifier functions
//-------------------------------------------------------------------

func specifierCompatible(prospective Version, spec string) bool {
	// Compatible releases have an equivalent combination of >= and ==. That is that ~=2.2 is equivalent to >=2.2,==2.*.
	// This allows us to implement this in terms of the other specifiers instead of implementing it ourselves.
	// The only thing we need to do is construct the other specifiers.

	var prefixElements []string
	for _, s := range versionSplit(spec) {
		if strings.HasPrefix(s, "post") || strings.HasPrefix(s, "dev") {
			break
		}
		prefixElements = append(prefixElements, s)
	}

	// We want everything but the last item in the version, but we want to ignore post and dev releases and
	// we want to treat the pre-release as it's own separate segment.
	prefix := strings.Join(prefixElements[:len(prefixElements)-1], ".")

	// Add the prefix notation to the end of our string
	prefix += ".*"

	return specifierGreaterThanEqual(prospective, spec) && specifierEqual(prospective, prefix)
}

func specifierEqual(prospective Version, spec string) bool {
	// https://github.com/pypa/packaging/blob/a6407e3a7e19bd979e93f58cfc7f6641a7378c46/packaging/specifiers.py#L476
	// We need special logic to handle prefix matching
	if strings.HasSuffix(spec, ".*") {
		// In the case of prefix matching we want to ignore local segment.
		prospective = MustParse(prospective.Public())

		// Split the spec out by dots, and pretend that there is an implicit
		// dot in between a release segment and a pre-release segment.
		splitSpec := versionSplit(strings.TrimSuffix(spec, ".*"))

		// Split the prospective version out by dots, and pretend that there is an implicit dot
		//  in between a release segment and a pre-release segment.
		splitProspective := versionSplit(prospective.String())

		// Shorten the prospective version to be the same length as the spec
		// so that we can determine if the specifier is a prefix of the
		// prospective version or not.
		if len(splitProspective) > len(splitSpec) {
			splitProspective = splitProspective[:len(splitSpec)]
		}

		paddedSpec, paddedProspective := padVersion(splitSpec, splitProspective)
		return reflect.DeepEqual(paddedSpec, paddedProspective)
	}

	specVersion := MustParse(spec)
	if specVersion.local == "" {
		prospective = MustParse(prospective.Public())
	}

	return specVersion.Equal(prospective)
}

func specifierNotEqual(prospective Version, spec string) bool {
	return !specifierEqual(prospective, spec)
}

func specifierLessThan(prospective Version, spec string) bool {
	// Convert our spec to a Version instance, since we'll want to work with it as a version.
	s := MustParse(spec)

	// Check to see if the prospective version is less than the spec version.
	// If it's not we can short circuit and just return False now instead of doing extra unneeded work.
	if !prospective.LessThan(s) {
		return false
	}

	// This special case is here so that, unless the specifier itself includes is a pre-release version,
	// that we do not accept pre-release versions for the version mentioned in the specifier
	// (e.g. <3.1 should not match 3.1.dev0, but should match 3.0.dev0).
	if !s.IsPreRelease() && prospective.IsPreRelease() {
		if MustParse(prospective.BaseVersion()).Equal(MustParse(s.BaseVersion())) {
			return false
		}
	}
	return true
}

func specifierGreaterThan(prospective Version, spec string) bool {
	// Convert our spec to a Version instance, since we'll want to work with it as a version.
	s := MustParse(spec)

	// Check to see if the prospective version is greater than the spec version.
	// If it's not we can short circuit and just return False now instead of doing extra unneeded work.
	if !prospective.GreaterThan(s) {
		return false
	}

	// This special case is here so that, unless the specifier itself includes is a post-release version,
	// that we do not accept post-release versions for the version mentioned in the specifier
	// (e.g. >3.1 should not match 3.0.post0, but should match 3.2.post0).
	if !s.IsPostRelease() && prospective.IsPostRelease() {
		if MustParse(prospective.BaseVersion()).Equal(MustParse(s.BaseVersion())) {
			return false
		}
	}

	// Ensure that we do not allow a local version of the version mentioned
	//  in the specifier, which is technically greater than, to match.
	if prospective.local != "" {
		if MustParse(prospective.BaseVersion()).Equal(MustParse(s.BaseVersion())) {
			return false
		}
	}
	return true
}

func specifierArbitrary(prospective Version, spec string) bool {
	return strings.EqualFold(prospective.String(), spec)
}

func specifierLessThanEqual(prospective Version, spec string) bool {
	p := MustParse(prospective.Public())
	s := MustParse(spec)
	return p.LessThanOrEqual(s)
}

func specifierGreaterThanEqual(prospective Version, spec string) bool {
	p := MustParse(prospective.Public())
	s := MustParse(spec)
	return p.GreaterThanOrEqual(s)
}
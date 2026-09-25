package tenant

import (
	"regexp"
)

var companyCodeRegex = regexp.MustCompile(`^[A-Z0-9_]{1,20}$`)

func IsValidCompanyCode(code string) bool {
	return companyCodeRegex.MatchString(code)
}

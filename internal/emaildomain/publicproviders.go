package emaildomain

import "strings"

// publicProviders are email domains where anyone can sign up for an address.
// No organization owns everyone at one of these, so none can be offered
// membership by email domain, even if a project somehow has a verified Domain
// for it. Domain verification is the main control; this list is a backstop
// and doesn't try to be complete.
var publicProviders = map[string]bool{
	"126.com":        true,
	"163.com":        true,
	"aim.com":        true,
	"aol.com":        true,
	"duck.com":       true,
	"fastmail.com":   true,
	"gmail.com":      true,
	"gmx.com":        true,
	"gmx.de":         true,
	"gmx.net":        true,
	"googlemail.com": true,
	"hey.com":        true,
	"hotmail.co.uk":  true,
	"hotmail.com":    true,
	"hotmail.de":     true,
	"hotmail.fr":     true,
	"hushmail.com":   true,
	"icloud.com":     true,
	"live.com":       true,
	"mac.com":        true,
	"mail.com":       true,
	"mail.ru":        true,
	"me.com":         true,
	"msn.com":        true,
	"naver.com":      true,
	"outlook.com":    true,
	"pm.me":          true,
	"proton.me":      true,
	"protonmail.com": true,
	"qq.com":         true,
	"rocketmail.com": true,
	"tuta.io":        true,
	"tutanota.com":   true,
	"web.de":         true,
	"yahoo.co.jp":    true,
	"yahoo.co.uk":    true,
	"yahoo.com":      true,
	"yahoo.de":       true,
	"yahoo.fr":       true,
	"yandex.com":     true,
	"yandex.ru":      true,
	"ymail.com":      true,
	"zoho.com":       true,
	"zohomail.com":   true,
}

// IsPublicProvider reports whether domain is, or is a subdomain of, a public
// email provider such as gmail.com.
func IsPublicProvider(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	for domain != "" {
		if publicProviders[domain] {
			return true
		}
		dot := strings.Index(domain, ".")
		if dot < 0 {
			return false
		}
		domain = domain[dot+1:]
	}
	return false
}

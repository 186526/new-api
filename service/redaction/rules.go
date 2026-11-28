package redaction

import (
	"net/netip"
	"regexp"
	"time"
)

var builtin = []rule{
	{id: "pem_private_key", label: "PRIVATE_KEY", pattern: regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----`)},
	{id: "jwt_bearer", label: "BEARER", pattern: regexp.MustCompile(`(?i)\bBearer[ \t]+([A-Za-z0-9._~+/=-]+)`), group: 1},
	{id: "jwt_bearer", label: "JWT", pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)},
	{id: "dsn_secret", label: "DSN_SECRET", pattern: regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s/:@]+:([^\s/@]+)@`), group: 1},
	{id: "api_keys", label: "API_KEY", pattern: regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{20,}|xai-[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{35}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|glpat-[A-Za-z0-9_-]{16,}|xox[baprs]-[A-Za-z0-9-]{10,})`)},
	{id: "cloud_tokens", label: "CLOUD_KEY", pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{id: "cloud_tokens", label: "CLOUD_SECRET", pattern: regexp.MustCompile(`(?i)\baws_secret_access_key[ \t]*[:=][ \t]*["']?([A-Za-z0-9/+=]{40})`), group: 1},
	{id: "email", label: "EMAIL", pattern: regexp.MustCompile(`(?i)[A-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[A-Z0-9-]+(?:\.[A-Z0-9-]+)+`), restorable: true},
	{id: "cn_id", label: "CN_ID", pattern: regexp.MustCompile(`\b[1-9][0-9]{16}[0-9Xx]\b`), valid: validCNID},
	{id: "credit_card", label: "CARD", pattern: regexp.MustCompile(`\b[0-9](?:[ -]?[0-9]){12,18}\b`), valid: validCard},
	{id: "phone", label: "PHONE", pattern: regexp.MustCompile(`(?:\+[1-9][0-9]{0,2}[ -]?)?\b1[3-9][0-9]{9}\b|\+[1-9][0-9]{7,14}\b`), restorable: true},
	{id: "private_ip", label: "IP", pattern: regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`), valid: privateIPv4, restorable: true},
	{id: "all_ip", label: "IP", pattern: regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`), valid: validIPv4, restorable: true},
}

var builtinIDs = func() map[string]bool {
	ids := make(map[string]bool, len(builtin))
	for _, item := range builtin {
		ids[item.id] = true
	}
	return ids
}()

func privateIPv4(value string) bool {
	addr, err := netip.ParseAddr(value)
	return err == nil && addr.Is4() && (addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast())
}
func validIPv4(value string) bool {
	addr, err := netip.ParseAddr(value)
	return err == nil && addr.Is4()
}

func validCard(value string) bool {
	sum, count := 0, 0
	first := byte(0)
	different := false
	for index := len(value) - 1; index >= 0; index-- {
		digit := value[index]
		if digit < '0' || digit > '9' {
			continue
		}
		if count == 0 {
			first = digit
		} else if first != digit {
			different = true
		}
		n := int(digit - '0')
		if count%2 == 1 {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		count++
		sum += n
	}
	return different && count >= 13 && count <= 19 && sum%10 == 0
}

func validCNID(value string) bool {
	switch value[:2] {
	case "11", "12", "13", "14", "15", "21", "22", "23", "31", "32", "33", "34", "35", "36", "37", "41", "42", "43", "44", "45", "46", "50", "51", "52", "53", "54", "61", "62", "63", "64", "65", "71", "81", "82":
	default:
		return false
	}
	birth, err := time.Parse("20060102", value[6:14])
	if err != nil || birth.Year() < 1800 || birth.After(time.Now()) {
		return false
	}
	weights := [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	sum := 0
	for index, weight := range weights {
		sum += int(value[index]-'0') * weight
	}
	actual := value[17]
	if actual == 'x' {
		actual = 'X'
	}
	return actual == "10X98765432"[sum%11]
}

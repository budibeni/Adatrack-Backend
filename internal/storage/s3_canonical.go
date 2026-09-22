package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// canonicalHeaders builds the canonical-headers block + the signed-header list
// (sorted lowercase names). Only headers that participate in the signature are
// covered: `host` plus every x-amz-* header and the few standard ones the client
// sets (content-type/range) — anything else is left unsigned so a proxy may add
// headers without breaking the signature.
func canonicalHeaders(req *http.Request, host string) (string, string) {
	headers := map[string]string{"host": host}
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") || lower == "content-type" || lower == "range" {
			headers[lower] = strings.TrimSpace(strings.Join(values, ","))
		}
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	var block strings.Builder
	for _, name := range names {
		block.WriteString(name)
		block.WriteString(":")
		block.WriteString(headers[name])
		block.WriteString("\n")
	}
	return block.String(), strings.Join(names, ";")
}

// canonicalQuery renders the canonical query string (sorted, escaped).
func canonicalQuery(u *url.URL) string { return canonicalQueryValues(u.Query()) }

// canonicalQueryValues sorts + escapes a query set the SigV4 way.
func canonicalQueryValues(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(values))
	for _, k := range keys {
		vs := append([]string(nil), values[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, s3Escape(k, true)+"="+s3Escape(v, true))
		}
	}
	return strings.Join(parts, "&")
}

// s3Escape applies the AWS SigV4 URI-encoding rules (RFC 3986 unreserved set;
// '/' is kept when encodeSlash is false).
func s3Escape(raw string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		switch {
		case ch >= 'A' && ch <= 'Z', ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9',
			ch == '-', ch == '_', ch == '.', ch == '~':
			b.WriteByte(ch)
		case ch == '/' && !encodeSlash:
			b.WriteByte(ch)
		default:
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{ch})))
		}
	}
	return b.String()
}

// s3EscapePath escapes a path but keeps the segment separators.
func s3EscapePath(path string) string { return s3Escape(path, false) }

// hmacSHA256 is the SigV4 HMAC primitive.
func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// sha256Hex is the payload hash of a request body.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

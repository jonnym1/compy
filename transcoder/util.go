package transcoder

import (
	"net/http"
	"strings"
)

func SupportsWebP(headers http.Header) bool {
	v := headers.Get("Accept")
	if strings.Contains(v, "webp") {
		return true
	}
	if v == "" {
                return true
        }
	return false
}

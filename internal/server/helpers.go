package server

import (
	"strings"

	"github.com/bevelgacom/wapipedia/pkg/wikipedia"
	"github.com/labstack/echo/v4"
)

// isNokia7110 checks if the User-Agent indicates a Nokia 7110
// The Nokia 7110 has limited WML support (no tables in early firmware)
func isNokia7110(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	return strings.Contains(ua, "nokia7110/1.0")
}

// getRenderOptions returns rendering options based on the device
func getRenderOptions(c echo.Context) wikipedia.RenderOptions {
	userAgent := c.Request().Header.Get("User-Agent")

	// Nokia 7110 doesn't support WML tables
	if isNokia7110(userAgent) {
		return wikipedia.RenderOptions{SupportsTables: false}
	}

	// Most other WAP browsers support tables
	return wikipedia.RenderOptions{SupportsTables: true}
}

// escapeWMLAttr escapes a string for use in WML attributes
func escapeWMLAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "$", "$$")
	return s
}

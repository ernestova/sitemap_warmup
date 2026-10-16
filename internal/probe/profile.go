package probe

import (
	"fmt"
	"net/http"
)

// ChromeMajor is the Chrome stable version the presets copy. It is updated
// with each release so the headers keep matching what real browsers send.
const ChromeMajor = "155"

// DefaultAcceptLanguage is sent when the operator does not configure one.
const DefaultAcceptLanguage = "en-US,en;q=0.9"

// RepoURL identifies the tool in the User-Agent token.
const RepoURL = "https://github.com/ernestova/sitemap_warmup"

// Profile is one way of requesting a URL: GET plus a fixed set of headers.
type Profile struct {
	Name   string
	Header http.Header
	// Cookie is sent only to the configured cookie hosts and only over HTTPS.
	Cookie string
}

const (
	desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + ChromeMajor + ".0.0.0 Safari/537.36"
	mobileUA  = "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/" + ChromeMajor + ".0.0.0 Mobile Safari/537.36"
	secCHUA   = `"Chromium";v="` + ChromeMajor + `", "Google Chrome";v="` + ChromeMajor + `", "Not_A Brand";v="99"`
)

// Presets returns the default desktop and mobile profiles: the navigation
// headers of Chrome stable on Windows and Android, with the sitewarm token
// appended to the User-Agent.
func Presets(version, acceptLanguage string) []Profile {
	if acceptLanguage == "" {
		acceptLanguage = DefaultAcceptLanguage
	}
	token := fmt.Sprintf(" sitewarm/%s (+%s)", version, RepoURL)
	return []Profile{
		{Name: "desktop", Header: chromeHeaders(desktopUA+token, acceptLanguage, "?0", `"Windows"`)},
		{Name: "mobile", Header: chromeHeaders(mobileUA+token, acceptLanguage, "?1", `"Android"`)},
	}
}

func chromeHeaders(ua, acceptLanguage, mobile, platform string) http.Header {
	h := http.Header{}
	h.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	h.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	h.Set("Accept-Language", acceptLanguage)
	h.Set("Sec-Ch-Ua", secCHUA)
	h.Set("Sec-Ch-Ua-Mobile", mobile)
	h.Set("Sec-Ch-Ua-Platform", platform)
	h.Set("Sec-Fetch-Dest", "document")
	h.Set("Sec-Fetch-Mode", "navigate")
	h.Set("Sec-Fetch-Site", "none")
	h.Set("Sec-Fetch-User", "?1")
	h.Set("Upgrade-Insecure-Requests", "1")
	h.Set("User-Agent", ua)
	return h
}

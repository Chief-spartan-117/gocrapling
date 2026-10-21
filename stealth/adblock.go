package stealth

// trackerDomains is a compact list of common ad, analytics and tracking
// hosts. Blocking them speeds up page loads and shrinks the fingerprinting
// surface. Subdomains are covered. Extend with Options.BlockDomains.
var trackerDomains = []string{
	"doubleclick.net", "googlesyndication.com", "googleadservices.com", "google-analytics.com",
	"googletagmanager.com", "googletagservices.com", "adservice.google.com", "pagead2.googlesyndication.com",
	"facebook.net", "connect.facebook.net", "analytics.twitter.com", "ads-twitter.com",
	"adsrvr.org", "adnxs.com", "adform.net", "criteo.com", "criteo.net", "taboola.com", "outbrain.com",
	"scorecardresearch.com", "quantserve.com", "chartbeat.com", "chartbeat.net", "hotjar.com",
	"hotjar.io", "mixpanel.com", "segment.io", "segment.com", "amplitude.com", "fullstory.com",
	"mouseflow.com", "crazyegg.com", "optimizely.com", "newrelic.com", "nr-data.net", "bugsnag.com",
	"sentry.io", "branch.io", "appsflyer.com", "adjust.com", "moatads.com", "pubmatic.com",
	"rubiconproject.com", "openx.net", "casalemedia.com", "smartadserver.com", "3lift.com",
	"bidswitch.net", "contextweb.com", "sharethrough.com", "advertising.com", "yieldmo.com",
	"media.net", "amazon-adsystem.com", "bat.bing.com", "clarity.ms", "px.ads.linkedin.com",
	"snap.licdn.com", "ads.linkedin.com", "tiqcdn.com", "demdex.net", "omtrdc.net", "everesttech.net",
	"krxd.net", "bluekai.com", "exelator.com", "agkn.com", "rlcdn.com", "mathtag.com", "serving-sys.com",
	"zedo.com", "teads.tv", "ml314.com", "tapad.com", "liadm.com", "id5-sync.com", "permutive.com",
	"onetrust.com", "cookielaw.org", "cookiebot.com", "trustarc.com", "usercentrics.eu",
	"intercom.io", "drift.com", "hs-analytics.net", "hs-scripts.com", "hsadspixel.net", "pardot.com",
}

// blockPatterns turns domains into Chrome URL block patterns.
func blockPatterns(extra []string, ads bool) []string {
	var domains []string
	if ads {
		domains = append(domains, trackerDomains...)
	}
	domains = append(domains, extra...)
	out := make([]string, 0, len(domains)*4)
	for _, d := range domains {
		// A pattern without a port does not match URLs that carry one.
		out = append(out, "*://"+d+"/*", "*://*."+d+"/*", "*://"+d+":*/*", "*://*."+d+":*/*")
	}
	return out
}

// resourcePatterns blocks fonts and media by extension. Images are turned
// off with a Chrome setting instead.
func resourcePatterns() []string {
	exts := []string{"woff", "woff2", "ttf", "otf", "eot", "mp4", "webm", "ogg", "mp3", "wav", "m4a", "avi", "mov",
		"png", "jpg", "jpeg", "gif", "webp", "avif", "svg", "ico", "bmp"}
	out := make([]string, 0, len(exts)*2)
	for _, e := range exts {
		out = append(out, "*."+e, "*."+e+"?*")
	}
	return out
}

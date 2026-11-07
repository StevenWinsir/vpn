package api

import "github.com/gin-gonic/gin"

// robotsBody turns every crawler away from the API host (handoff_seo.md D16). The clients reach
// this host directly, so it cannot be hidden; it can only say it is not for search engines. The
// website has its own robots.txt on its own host.
const robotsBody = "User-agent: *\nDisallow: /\n"

func robotsTxt(c *gin.Context) {
	// Crawlers fetch robots.txt rarely; an hour of caching is what they expect.
	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(200, "text/plain; charset=utf-8", []byte(robotsBody))
}

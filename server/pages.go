package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// TODO cache info used by gui.

const (
	appName = "Decred Mapper"
)

// themeCookieMaxAge is how long the theme preference persists (one year).
const themeCookieMaxAge = 365 * 24 * 60 * 60

// defaultTheme is used when the visitor has expressed no preference.
const defaultTheme = "dark"

func getTheme(c *gin.Context) string {
	theme := c.Query("theme")
	if theme != "" {
		c.SetCookie("theme", theme, themeCookieMaxAge, "/", domain, false, false)
		return theme
	}

	if theme, _ = c.Cookie("theme"); theme != "" {
		return theme
	}

	return defaultTheme
}

// baseData returns the template data common to every page.
func baseData(c *gin.Context, activePage string) gin.H {
	return gin.H{
		"ActivePage": activePage,
		"Summary":    amgr.GetSummary(),
		"AppName":    appName,
		"Theme":      getTheme(c),
	}
}

func homepage(c *gin.Context) {
	c.HTML(http.StatusOK, "worldmap.html", baseData(c, "WorldMap"))
}

// mapMarker is the minimal node representation needed to plot a marker on the
// world map.
type mapMarker struct {
	IP  string  `json:"ip"`
	Lat float32 `json:"lat"`
	Lon float32 `json:"lon"`
}

func worldNodes(c *gin.Context) {
	nodes := amgr.AllGoodNodes()
	markers := make([]mapMarker, 0, len(nodes))
	for _, n := range nodes {
		if n.GeoData == nil {
			continue
		}
		markers = append(markers, mapMarker{
			IP:  n.IP.String(),
			Lat: n.GeoData.Lat,
			Lon: n.GeoData.Lon,
		})
	}
	c.JSON(http.StatusOK, markers)
}

func userAgents(c *gin.Context) {
	c.HTML(http.StatusOK, "user_agents.html", baseData(c, "UserAgents"))
}

func list(c *gin.Context) {
	count, nodes := amgr.PageOfNodes(0, 10)
	data := baseData(c, "AllNodes")
	data["Nodes"] = nodes
	data["GoodCount"] = count
	c.HTML(http.StatusOK, "list.html", data)
}

func node(c *gin.Context) {
	ip := c.Query("ip")
	node, good, ok := amgr.GetNode(ip)
	data := baseData(c, "")
	data["Good"] = good
	data["Node"] = node
	data["OK"] = ok
	data["SearchIP"] = ip
	c.HTML(http.StatusOK, "node.html", data)
}

package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type UA struct {
	Rank        int    `json:"rank"`
	AgentString string `json:"useragent"`
	Count       int    `json:"count"`
}

func api(c *gin.Context) {
	s := amgr.GetSummary()

	ua := make([]UA, len(s.UserAgents))
	for i, u := range s.UserAgents {
		ua[i] = UA{i + 1, u.Value, u.Count}
	}

	c.JSON(http.StatusOK, ua)
}

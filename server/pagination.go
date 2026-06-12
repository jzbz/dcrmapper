package server

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// maxPageSize caps how many nodes a single request may ask for.
const maxPageSize = 100

func getPaginationParams(r *http.Request) (first, last int, err error) {
	pageNumber, err := strconv.Atoi(r.FormValue("pageNumber"))
	if err != nil {
		return 0, 0, err
	}
	pageSize, err := strconv.Atoi(r.FormValue("pageSize"))
	if err != nil {
		return 0, 0, err
	}

	if pageNumber < 1 || pageSize < 1 || pageSize > maxPageSize {
		return 0, 0, errors.New("invalid number given for pagenumber or pagesize")
	}

	first = (pageNumber - 1) * pageSize
	last = first + pageSize

	return first, last, nil
}

func paginatedNodes(c *gin.Context) {
	first, last, err := getPaginationParams(c.Request)
	if err != nil {
		log.Printf("Rejected pagination request: %v", err)
		c.Status(http.StatusBadRequest)
		return
	}

	count, nodes := amgr.PageOfNodes(first, last)

	c.JSON(http.StatusOK, paginationPayload{
		Count: count,
		Data:  nodes,
	})
}

type paginationPayload struct {
	Data  interface{} `json:"data"`
	Count int         `json:"count"`
}

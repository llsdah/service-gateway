package handlers

import (
	"net/http"
	"service-gateway/model"

	"github.com/gin-gonic/gin"
)

func HelloGetHandler(c *gin.Context) {
	c.JSON(http.StatusOK, model.HelloData{})
}

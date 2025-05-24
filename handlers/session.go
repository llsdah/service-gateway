package handlers

import (
	"encoding/json"
	"net/http"
	"service-gateway/model"
	"service-gateway/redis"

	"github.com/gin-gonic/gin"
)

func SetSession(c *gin.Context) {
	var session model.SessionData
	ctx := c.Request.Context()

	if err := c.ShouldBindBodyWithJSON(&session); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
	}

	jsonValue, err := json.Marshal(session)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Encoding failed"})
		return
	}

	if err := redis.SaveSession(ctx, session.SessionKey, string(jsonValue), session.SessionTTL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Redis error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessionKey": session.SessionKey})

}

func GetSession(c *gin.Context) {
	var session model.SessionData
	ctx := c.Request.Context()

	if err := c.ShouldBindBodyWithJSON(&session); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON"})
	}

	if _, err := json.Marshal(session); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Encoding failed"})
		return
	}

	value, err := redis.GetSession(ctx, session.SessionKey)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Redis error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"sessionKey": session.SessionKey,
		"sessionValue": value})

}

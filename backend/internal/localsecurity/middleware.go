package localsecurity

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

var sessionSecret = os.Getenv("KANIVET_SESSION_SECRET")

func SetSessionSecret(secret string) {
	sessionSecret = secret
}

func SessionSecretMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if sessionSecret == "" {
			c.Next()
			return
		}

		clientSecret := c.GetHeader("X-Session-Secret")
		if clientSecret == "" {
			clientSecret = c.Query("session_secret")
		}

		if clientSecret == sessionSecret {
			c.Next()
			return
		}

		c.Header("X-Session-Refresh-Required", "true")
		if clientSecret == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":      "session_secret_missing",
				"message":    "Session secret not provided",
				"retry_hint": "refresh_session",
			})
			return
		}

		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error":      "session_secret_mismatch",
			"message":    "Session secret mismatch",
			"retry_hint": "refresh_session",
		})
	}
}

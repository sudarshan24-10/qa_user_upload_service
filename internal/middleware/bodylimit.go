package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// MaxBody caps API request bodies. Uploads go straight to MinIO via presigned URLs,
// so real JSON bodies are tiny and a large one is abuse.
func MaxBody(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The client declared a size over the limit: reject without reading anything.
		if c.Request.ContentLength > limit {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge,
				gin.H{"error": "request body too large"})
			return
		}
		// Size unknown (chunked) or within the limit: stop reading at limit bytes. A
		// handler's ShouldBindJSON then fails and the existing handlers answer 400.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}
